package web

import (
	"regexp"
	"strconv"

	"github.com/ziyan/cue/internal/network"
	"github.com/ziyan/cue/internal/util/qr"
	"net"
	"strings"
	"testing"
)

// The welcome page is what somebody standing in front of a new screen reads.
// If the address on it is wrong, there is no way to set the device up at all:
// the screen is the only output the device has before it is configured.
func TestTheAddressesOfferedAreOnesAnotherMachineCanReach(t *testing.T) {
	addresses := machineAddresses()

	for _, address := range addresses {
		parsed := net.ParseIP(address)
		if parsed == nil {
			t.Errorf("%q is not an address at all", address)
			continue
		}
		if parsed.IsLoopback() {
			t.Errorf("%s is a loopback address; typing it into a laptop reaches that laptop", address)
		}
		if parsed.IsLinkLocalUnicast() {
			t.Errorf("%s is link-local; it is not something anybody can type", address)
		}
		if strings.Contains(address, ":") {
			t.Errorf("%s has colons in it; nobody types that at a screen, and it needs brackets in a URL", address)
		}
	}
}

func TestTheWelcomePageNamesTheDeviceAndAnAddress(t *testing.T) {
	server := newTestServer(t, defaultConfigurationForTest())

	response := do(server, "GET", "/welcome", nil, nil)
	if response.Code != 200 {
		t.Fatalf("/welcome answered %d", response.Code)
	}

	body := response.Body.String()
	if !strings.Contains(body, server.store.Current().Device.Name) {
		t.Error("the page does not name the device")
	}
	if !strings.Contains(body, server.store.Current().Device.Identifier) {
		t.Error("the page does not show the identifier, which is what somebody reads out over the phone")
	}
	// Before it is set up, the page has to say so — otherwise somebody looks
	// at a screen with an address on it and no idea what to do.
	if !strings.Contains(body, "Not set up yet") {
		t.Error("a device that has not been set up does not say so")
	}
	if !strings.Contains(body, "http://") {
		t.Error("the page offers no address to open")
	}
}

func TestTheWelcomePageIsServedWithoutASession(t *testing.T) {
	// It is what the browser on the device itself loads, and that browser has
	// no password. It is reachable only from the machine's own screen anyway.
	server := newTestServer(t, defaultConfigurationForTest())
	if response := do(server, "GET", "/welcome", nil, nil); response.Code != 200 {
		t.Errorf("/welcome answered %d without a session", response.Code)
	}
}

func TestTheDeviceNameIsEscapedRatherThanRendered(t *testing.T) {
	// The name is typed by a person and ends up in a page. A device called
	// "<script>…" must be text, not markup.
	configuration := defaultConfigurationForTest()
	configuration.Device.Name = `<script>alert(1)</script>`
	server := newTestServer(t, configuration)

	body := do(server, "GET", "/welcome", nil, nil).Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("the device name was rendered as markup")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("the device name does not appear escaped either, so it was dropped rather than shown")
	}
}

// The code on the screen has to be the code for the address written under it.
//
// Decoding a QR code needs a decoder, which is a much larger thing than the
// encoder and would only be in the tree for this. Instead the page's own SVG
// is read back into a matrix and compared against what the encoder produces
// for that address: if the page carried a code for anything else -- a stale
// address, an empty string, the device name -- the matrices would differ.
func TestTheQRCodeOnTheWelcomePageIsTheCodeForTheAddress(t *testing.T) {
	server := newTestServer(t, defaultConfigurationForTest())

	body := do(server, "GET", "/welcome", nil, nil).Body.String()
	if !strings.Contains(body, "<svg") {
		t.Skip("this machine has no address to put in a code, so the page has none")
	}

	address := firstAddressOn(t, body)
	wanted, err := qr.Encode(address)
	if err != nil {
		t.Fatalf("encoding %q: %s", address, err)
	}
	drawn := matrixFromSVG(t, body, len(wanted))

	for row := range wanted {
		for column := range wanted[row] {
			if drawn[row][column] != wanted[row][column] {
				t.Fatalf("the code drawn on the page differs from the code for %q "+
					"at row %d column %d", address, row, column)
			}
		}
	}
}

// firstAddressOn pulls the address out of the rendered page, which is the one
// the code is supposed to carry.
func firstAddressOn(t *testing.T, body string) string {
	t.Helper()
	found := regexp.MustCompile(`<div class="address">([^<]+)</div>`).FindStringSubmatch(body)
	if found == nil {
		t.Fatal("the page shows a code but no address, so there is nothing to compare it with")
	}
	return found[1]
}

// matrixFromSVG reads the drawn code back out of the page.
func matrixFromSVG(t *testing.T, body string, size int) [][]bool {
	t.Helper()

	box := regexp.MustCompile(`viewBox="0 0 (\d+) (\d+)"`).FindStringSubmatch(body)
	if box == nil {
		t.Fatal("the SVG has no viewBox, so its size is unknown")
	}
	if box[1] != box[2] {
		t.Fatalf("the code is %s by %s, and a QR code is square", box[1], box[2])
	}
	drawnSize, _ := strconv.Atoi(box[1])
	if drawnSize != size {
		t.Fatalf("the page draws a %d module code where the address needs %d", drawnSize, size)
	}

	matrix := make([][]bool, size)
	for row := range matrix {
		matrix[row] = make([]bool, size)
	}
	// Every dark module is one black rect. The white ground is a rect too, and
	// it is skipped by matching only the black ones.
	for _, rect := range regexp.MustCompile(`<rect x="(\d+)" y="(\d+)"[^>]*fill="#000"`).FindAllStringSubmatch(body, -1) {
		column, _ := strconv.Atoi(rect[1])
		row, _ := strconv.Atoi(rect[2])
		if row >= size || column >= size {
			t.Fatalf("the page draws a module at row %d column %d, outside the %d module code", row, column, size)
		}
		matrix[row][column] = true
	}
	return matrix
}

// While the device is running its own setup network, the code on the screen
// has to be the one that joins that network -- not the device's web address,
// which nobody can reach yet.
func TestTheScreenShowsTheCodeThatJoinsTheSetupNetwork(t *testing.T) {
	server := newTestServer(t, defaultConfigurationForTest())
	device := server.device.(*fakeDevice)
	device.setupNetwork = network.Credentials{SSID: "cue-4k2p9x", Passphrase: "hd7Rk2m9Qw4x"}
	device.onboarding = true

	body := do(server, "GET", "/welcome", nil, nil).Body.String()

	wanted, err := qr.Encode(device.setupNetwork.JoinCode())
	if err != nil {
		t.Fatalf("encoding the join code: %s", err)
	}
	drawn := matrixFromSVG(t, body, len(wanted))
	for row := range wanted {
		for column := range wanted[row] {
			if drawn[row][column] != wanted[row][column] {
				t.Fatalf("the code on the screen is not the one that joins %q "+
					"(differs at row %d column %d)", device.setupNetwork.SSID, row, column)
			}
		}
	}

	// The name is shown as text as well, because a phone that has joined shows
	// a network name and somebody has to be able to tell it is the right one.
	if !strings.Contains(body, "cue-4k2p9x") {
		t.Error("the page does not name the network the phone is about to join")
	}
	// And the address, which reaches nothing during setup, is not offered.
	if strings.Contains(body, "Open this address") {
		t.Error("the page tells somebody to open an address while it has no network")
	}
}

// The passphrase is on the screen inside the code and must not be anywhere a
// reader of the page source could pick it up without being in the room. The
// code itself is unavoidable -- that is the point -- but writing it out as
// text as well would put it in a screenshot, a log, or a photograph of a
// support ticket.
func TestThePassphraseIsNotWrittenOutAsTextOnTheScreen(t *testing.T) {
	server := newTestServer(t, defaultConfigurationForTest())
	device := server.device.(*fakeDevice)
	device.setupNetwork = network.Credentials{SSID: "cue-4k2p9x", Passphrase: "hd7Rk2m9Qw4x"}
	device.onboarding = true

	body := do(server, "GET", "/welcome", nil, nil).Body.String()
	if strings.Contains(body, device.setupNetwork.Passphrase) {
		t.Error("the passphrase appears as readable text on the page")
	}
}
