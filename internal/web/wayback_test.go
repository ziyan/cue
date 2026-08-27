package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// The control has to be on whatever is on the screen, because the situations
// it exists for are the ones where nothing else can be reached.
func TestTheWayBackIsOnEveryPageTheScreenShows(t *testing.T) {
	server, item := serverWithVideoItem(t, false)

	if body := player(t, server, item.Identifier).Body.String(); !strings.Contains(body, "__cueWayBack") {
		t.Error("the player page does not carry the way back")
	}

	welcome := do(server, "GET", "/welcome", nil, nil).Body.String()
	if !strings.Contains(welcome, "__cueWayBack") {
		t.Error("the welcome page does not carry the way back")
	}
}

// It must be hidden until somebody is there, for the same reason the mouse
// cursor is: a wall display with a button permanently on it has that button in
// every photograph of it.
func TestTheWayBackIsHiddenUntilSomebodyIsThere(t *testing.T) {
	script := WayBackScript()

	if !strings.Contains(script, "opacity:0") {
		t.Error("the control starts visible")
	}
	for _, sign := range []string{"mousemove", "touchstart", "keydown"} {
		if !strings.Contains(script, sign) {
			t.Errorf("the control does not appear on %s", sign)
		}
	}
	if !strings.Contains(script, "setTimeout(hide") {
		t.Error("the control never hides itself again")
	}
}

// A screen in a lobby must not be resettable by one stray click.
func TestTheWayBackAsksBeforeItActs(t *testing.T) {
	script := WayBackScript()
	if !strings.Contains(script, "Forget this network and show the setup code?") {
		t.Error("the control acts without asking")
	}
	// The request is only made from the confirming button.
	if before, _, _ := strings.Cut(script, "Forget this network"); strings.Contains(before, "/api/v1/wireless/reset") {
		t.Error("the reset is asked for before the question is put")
	}
}

// Somebody at the screen has demonstrated the access this grants. Somebody on
// the network has not.
func TestResettingIsRefusedToTheNetworkAndAllowedFromTheScreen(t *testing.T) {
	server := newTestServer(t, config.Default())
	device := server.device.(*fakeDevice)
	signedIn(t, server)

	if code := do(server, "POST", "/api/v1/wireless/reset", nil, nil).Code; code != http.StatusUnauthorized {
		t.Errorf("the network was allowed to reset the wireless: %d", code)
	}
	if device.forgotten != 0 {
		t.Error("the device forgot its network at the request of the network")
	}

	// A page this daemon served, on this device. That is the one case it is
	// for, and the browser sets the Origin itself.
	_, port, _ := net.SplitHostPort(server.Address())
	ours := httptest.NewRequest(http.MethodPost, "/api/v1/wireless/reset", nil)
	ours.RemoteAddr = "127.0.0.1:54321"
	ours.Header.Set("Origin", "http://127.0.0.1:"+port)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, ours)

	if response.Code != http.StatusOK {
		t.Fatalf("the screen's own browser was refused: %d", response.Code)
	}
	if device.forgotten != 1 {
		t.Errorf("the device was asked to forget its network %d times", device.forgotten)
	}
}

// The browser on this device spends its life showing pages other people wrote,
// and any one of them can ask the loopback for whatever it likes. A dashboard
// that took its own screen off the network would be an unpleasant surprise.
func TestAPageTheScreenMerelyDisplaysCannotResetTheWireless(t *testing.T) {
	server := newTestServer(t, config.Default())
	device := server.device.(*fakeDevice)
	signedIn(t, server)

	for _, origin := range []string{
		"https://dashboard.example.com",
		"http://127.0.0.1:9999",  // the loopback, but not this server
		"http://192.0.2.10:8080", // this device, but reached over the network
		"null",                   // a sandboxed frame
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/wireless/reset", nil)
		request.RemoteAddr = "127.0.0.1:54321"
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, request)

		if response.Code == http.StatusOK {
			t.Errorf("a page from %s was allowed to reset the wireless", origin)
		}
	}
	if device.forgotten != 0 {
		t.Errorf("the device forgot its network %d times at the request of a page "+
			"it merely displays", device.forgotten)
	}
}

// A request with no Origin is a command line, not a page, and a command line
// has the API and a password. This is not hypothetical: a stray curl during
// development took a device off its network.
func TestARequestWithNoOriginIsRefused(t *testing.T) {
	server := newTestServer(t, config.Default())
	device := server.device.(*fakeDevice)
	signedIn(t, server)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/wireless/reset", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)

	if response.Code == http.StatusOK {
		t.Error("a request with no Origin was allowed to reset the wireless")
	}
	if device.forgotten != 0 {
		t.Error("the device forgot its network at the request of something with no page")
	}
}

// Injecting the same script twice must not give a screen two buttons.
func TestTheWayBackOnlyInstallsItselfOnce(t *testing.T) {
	if !strings.Contains(WayBackScript(), "if (window.__cueWayBack) return;") {
		t.Error("the script does not guard against being added twice")
	}
}
