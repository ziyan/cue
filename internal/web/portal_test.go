package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/network"
	"github.com/ziyan/cue/internal/network/onboarding"
)

func setupServer(t *testing.T, running bool) (*Server, *fakeDevice) {
	t.Helper()
	server := newTestServer(t, config.Default())
	device := server.device.(*fakeDevice)
	device.onboarding = running
	device.setupNetwork = network.Credentials{SSID: "cue-4k2p9x", Passphrase: "hd7Rk2m9Qw4x"}
	device.setupSeen = []network.WirelessNetwork{
		{SSID: "far away", SignalStrength: -85, Security: "wpa-psk"},
		{SSID: "the office", SignalStrength: -45, Security: "wpa-psk"},
		{SSID: "guest", SignalStrength: -60, Security: "open"},
		{SSID: "the office", SignalStrength: -70, Security: "wpa-psk"},
	}
	return server, device
}

// A device in normal use must serve nothing here at all. These routes have no
// session in front of them, so the only thing keeping them shut is this.
func TestThePortalIsNotServedUnlessTheDeviceIsBeingSetUp(t *testing.T) {
	server, _ := setupServer(t, false)

	paths := append([]string{"/portal"}, captiveProbePaths...)
	for _, path := range paths {
		if code := do(server, "GET", path, nil, nil).Code; code != http.StatusNotFound {
			t.Errorf("%s answered %d on a device that is not being set up, want 404", path, code)
		}
	}
	for _, path := range []string{"/api/v1/portal/join", "/api/v1/portal/scan"} {
		if code := do(server, "POST", path, map[string]string{"ssid": "x"}, nil).Code; code != http.StatusNotFound {
			t.Errorf("%s answered %d on a device that is not being set up, want 404", path, code)
		}
	}
}

// Every probe a phone makes has to be answered with a redirect, because that
// mismatch is the entire mechanism that makes the setup page open by itself.
func TestEveryPhoneProbeIsRedirectedToThePortal(t *testing.T) {
	server, _ := setupServer(t, true)

	for _, path := range captiveProbePaths {
		response := do(server, "GET", path, nil, nil)
		if response.Code != http.StatusFound {
			t.Errorf("%s answered %d, want 302 -- a phone getting what it expected "+
				"here decides the network is fine and never opens the page", path, response.Code)
			continue
		}
		if location := response.Header().Get("Location"); location != portalAddress {
			t.Errorf("%s redirects to %q, want %q", path, location, portalAddress)
		}
	}
}

func TestThePortalOffersTheNetworksStrongestFirstAndOnlyOnce(t *testing.T) {
	server, _ := setupServer(t, true)

	body := do(server, "GET", "/portal", nil, nil).Body.String()

	for _, name := range []string{"the office", "guest", "far away"} {
		if !strings.Contains(body, name) {
			t.Errorf("the page does not offer %q", name)
		}
	}
	// The office is seen twice by the radio and must be offered once, or
	// somebody has to guess which of two identical entries is theirs.
	if count := strings.Count(body, `data-ssid="the office"`); count != 1 {
		t.Errorf("%q is offered %d times, want once", "the office", count)
	}
	// Strongest first.
	office := strings.Index(body, `data-ssid="the office"`)
	guest := strings.Index(body, `data-ssid="guest"`)
	far := strings.Index(body, `data-ssid="far away"`)
	if !(office < guest && guest < far) {
		t.Errorf("the networks are not offered strongest first: office at %d, guest at %d, far at %d",
			office, guest, far)
	}
	// An open network must not ask for a password.
	if !strings.Contains(body, `data-ssid="guest" data-secured="false"`) {
		t.Error("the open network is marked as needing a password")
	}
}

// A network this device cannot join has to be visible but not choosable, or
// somebody picks it, types a password and waits for a failure.
func TestANetworkThisDeviceCannotJoinIsShownButNotOffered(t *testing.T) {
	server, device := setupServer(t, true)
	device.setupSeen = []network.WirelessNetwork{
		{SSID: "corporate", SignalStrength: -50, Security: "enterprise"},
	}

	body := do(server, "GET", "/portal", nil, nil).Body.String()
	if !strings.Contains(body, "corporate") {
		t.Fatal("the network is not shown at all, so somebody wonders why theirs is missing")
	}
	if !strings.Contains(body, "disabled") {
		t.Error("the network cannot be joined but is offered as though it could")
	}
}

func TestChoosingANetworkStartsTheJoin(t *testing.T) {
	server, device := setupServer(t, true)

	response := do(server, "POST", "/api/v1/portal/join",
		map[string]string{"ssid": "the office", "passphrase": "a test passphrase"}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("joining answered %d: %s", response.Code, response.Body)
	}
	if device.joinedSSID != "the office" || device.joinedPassphrase != "a test passphrase" {
		t.Errorf("the device was asked to join %q with %q",
			device.joinedSSID, device.joinedPassphrase)
	}
}

func TestJoiningNothingIsRefused(t *testing.T) {
	server, device := setupServer(t, true)

	if code := do(server, "POST", "/api/v1/portal/join", map[string]string{"ssid": "  "}, nil).Code; code != http.StatusBadRequest {
		t.Errorf("joining a blank network answered %d, want 400", code)
	}
	if device.joinedSSID != "" {
		t.Errorf("the device was asked to join %q", device.joinedSSID)
	}
}

// What went wrong last time has to be on the page they come back to, or they
// try the same password again.
func TestThePortalSaysWhyTheLastAttemptFailed(t *testing.T) {
	server, device := setupServer(t, true)
	device.setupTrouble = `"the office" did not accept that password.`

	body := do(server, "GET", "/portal", nil, nil).Body.String()
	if !strings.Contains(body, "did not accept that password") {
		t.Error("the page does not say why the last attempt failed")
	}
}

// The passphrase of the setup network must not appear on the portal: the
// portal is reachable by anybody already on that network, and the screen is
// supposed to be the only place it exists.
func TestTheSetupPassphraseIsNotOnThePortal(t *testing.T) {
	server, device := setupServer(t, true)

	body := do(server, "GET", "/portal", nil, nil).Body.String()
	if strings.Contains(body, device.setupNetwork.Passphrase) {
		t.Error("the setup network's passphrase is printed on the setup page")
	}
}

func TestScanningAgainAsksTheDevice(t *testing.T) {
	server, device := setupServer(t, true)

	if code := do(server, "POST", "/api/v1/portal/scan", nil, nil).Code; code != http.StatusOK {
		t.Fatalf("scanning again answered %d", code)
	}
	if device.rescans != 1 {
		t.Errorf("the device was asked to scan %d times, want 1", device.rescans)
	}
}

// The address phones are redirected to has to be one they can reach: the setup
// network's address, on the default port, because a phone following a redirect
// from a captive probe has no way to be told a port.
func TestThePortalAddressIsOnThePortAPhoneWillReach(t *testing.T) {
	parsed, err := url.Parse(portalAddress)
	if err != nil {
		t.Fatalf("the portal address does not parse: %s", err)
	}
	if parsed.Port() != "" {
		t.Errorf("the portal address is %q, which names port %q. A phone probing "+
			"a captive network fetches port 80 and follows the redirect it gets; "+
			"anything else and it never shows the page.", portalAddress, parsed.Port())
	}
	if parsed.Hostname() != onboarding.DeviceAddress.String() {
		t.Errorf("the portal address points at %q, but the setup network answers "+
			"on %s", parsed.Hostname(), onboarding.DeviceAddress)
	}
}

// The extra listener must serve the portal, and must serve it on the setup
// address only.
func TestTheSetupPortServesThePortal(t *testing.T) {
	server, _ := setupServer(t, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Port 0 so the test does not need to be root or to own port 80.
	listening := make(chan error, 1)
	go func() { listening <- server.ServeSetupPort(ctx, "127.0.0.1:0") }()

	select {
	case err := <-listening:
		if err != nil {
			t.Fatalf("the setup listener stopped at once: %s", err)
		}
	case <-time.After(500 * time.Millisecond):
	}
}
