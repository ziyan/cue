package web

import (
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
