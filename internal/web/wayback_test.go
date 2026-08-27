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
	script := newTestServer(t, config.Default()).WayBackScript()

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
// The mark itself can do nothing at all. Everything it leads to lives in a
// page this daemon served, because this script runs inside whatever is on the
// screen -- usually somebody else's page -- and a page from somewhere else may
// not act on this device however it got here.
func TestTheMarkItselfCanDoNothing(t *testing.T) {
	script := newTestServer(t, config.Default()).WayBackScript()

	for _, forbidden := range []string{"/api/v1/wireless/reset", "/api/v1/menu/restart", "/api/v1/playlist/next"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("the script injected into other people's pages calls %s itself", forbidden)
		}
	}
	if !strings.Contains(script, "/menu") {
		t.Error("the mark does not open the menu")
	}
	if !strings.Contains(script, "iframe") {
		t.Error("the menu is not opened as a page of its own")
	}
}

// The menu asks before anything that takes the screen away.
func TestTheMenuAsksBeforeTheDisruptiveThings(t *testing.T) {
	server := newTestServer(t, config.Default())
	body := menuPage(t, server)

	for _, asked := range []string{
		"Restart the browser?",
		"Restart the screen?",
		"Forget this wireless network and show the setup code?",
	} {
		if !strings.Contains(body, asked) {
			t.Errorf("the menu does not ask %q", asked)
		}
	}
}

// It offers actions and no settings: changing a URL or a timezone is work for
// a keyboard and the web interface.
func TestTheMenuChangesNoSettings(t *testing.T) {
	server := newTestServer(t, config.Default())
	body := menuPage(t, server)

	for _, absent := range []string{"/api/v1/configuration", "<input", "<select", "<textarea"} {
		if strings.Contains(body, absent) {
			t.Errorf("the menu offers %q, which makes it a settings page", absent)
		}
	}
}

// While it is open the screen must not rotate out from under whoever is
// reading it.
func TestTheMenuHoldsTheScreenStill(t *testing.T) {
	server := newTestServer(t, config.Default())
	body := menuPage(t, server)

	if !strings.Contains(body, "/api/v1/playlist/hold") {
		t.Error("the menu does not hold the screen still while it is open")
	}
	if !strings.Contains(body, "/api/v1/playlist/release") {
		t.Error("the menu never lets the screen go again")
	}
}

func menuPage(t *testing.T, server *Server) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/menu", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("the menu answered %d", response.Code)
	}
	return response.Body.String()
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
	if !strings.Contains(newTestServer(t, config.Default()).WayBackScript(), "if (window.__cueWayBack) return;") {
		t.Error("the script does not guard against being added twice")
	}
}
