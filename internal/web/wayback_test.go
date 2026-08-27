package web

import (
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

	request := httptest.NewRequest(http.MethodPost, "/api/v1/wireless/reset", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("the screen's own browser was refused: %d", response.Code)
	}
	if device.forgotten != 1 {
		t.Errorf("the device was asked to forget its network %d times", device.forgotten)
	}
}

// Injecting the same script twice must not give a screen two buttons.
func TestTheWayBackOnlyInstallsItselfOnce(t *testing.T) {
	if !strings.Contains(WayBackScript(), "if (window.__cueWayBack) return;") {
		t.Error("the script does not guard against being added twice")
	}
}
