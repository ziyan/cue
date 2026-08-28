package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/upgrade"
)

// Upgrading from the screen needs this device's password, not merely somebody
// standing at it.
//
// The menu asks for the password before it offers anything, so a pass that has
// been through that gate has proved what signing in to the web interface would
// have proved. A pass that has not is worth nothing here, and neither is a
// request from the network with no session.
func TestUpgradingFromTheScreenNeedsThePasswordFirst(t *testing.T) {
	server := newTestServer(t, config.Default())
	server = server.WithUpgrades(upgrade.NewChecker(upgrade.Repository, "0.1.0"))
	signedIn(t, server)
	_, pass := openMenu(t, server)

	// Before the password: refused, and refused as unauthorised rather than
	// for any reason about this device's settings.
	if code := passRequest(t, server, http.MethodPost, "/api/v1/menu/upgrade", nil, pass); code != http.StatusUnauthorized {
		t.Errorf("an unopened pass could upgrade: %d", code)
	}
	// From the network with nothing at all: likewise.
	if code := do(server, "POST", "/api/v1/menu/upgrade", nil, nil).Code; code != http.StatusUnauthorized {
		t.Errorf("the network could upgrade without signing in: %d", code)
	}

	// After the password it gets past the gate, and is then refused on the
	// merits -- this device has no Docker socket, which is the next question
	// and a different answer.
	if code := passRequest(t, server, http.MethodPost, "/api/v1/screen/unlock",
		map[string]string{"password": testPassword}, pass); code != http.StatusOK {
		t.Fatal("the password did not open the menu")
	}
	code := passRequest(t, server, http.MethodPost, "/api/v1/menu/upgrade", nil, pass)
	if code == http.StatusUnauthorized {
		t.Error("the password was proved and it still says who are you")
	}
	if code != http.StatusForbidden {
		t.Errorf("expected it to be refused for want of the socket, got %d", code)
	}
}

// Both things are needed, and the answer says which is missing. A device with
// neither still sees everything the page has to show.
func TestApplyingIsRefusedWithoutTheSettingAndTheSocket(t *testing.T) {
	server := newTestServer(t, config.Default())
	server = server.WithUpgrades(upgrade.NewChecker(upgrade.Repository, "0.1.0"))
	session := signedIn(t, server)

	response := do(server, "POST", "/api/v1/upgrade", nil, session)
	if response.Code != http.StatusForbidden {
		t.Fatalf("applying answered %d, want 403", response.Code)
	}
	if !strings.Contains(response.Body.String(), "allowApply") {
		t.Errorf("the refusal does not say what is missing: %s", response.Body)
	}
}

// The state is readable by anybody signed in, whatever the device can do about
// it: knowing a fix exists is useful even where the button is impossible.
func TestTheUpgradeStateIsReadableWithoutBeingAbleToApply(t *testing.T) {
	server := newTestServer(t, config.Default())
	server = server.WithUpgrades(upgrade.NewChecker(upgrade.Repository, "0.1.0"))
	session := signedIn(t, server)

	response := do(server, "GET", "/api/v1/upgrade", nil, session)
	if response.Code != http.StatusOK {
		t.Fatalf("reading the state answered %d: %s", response.Code, response.Body)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"running":"0.1.0"`) {
		t.Errorf("it does not say what is running: %s", body)
	}
	if !strings.Contains(body, `"canApply":false`) {
		t.Errorf("it does not say the button is impossible here: %s", body)
	}
}

// A daemon built without a checker must say so rather than looking up to date.
func TestADaemonNotCheckingSaysSoRatherThanLookingUpToDate(t *testing.T) {
	server := newTestServer(t, config.Default())
	session := signedIn(t, server)

	response := do(server, "GET", "/api/v1/upgrade", nil, session)
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("answered %d, want 503", response.Code)
	}
	if strings.Contains(response.Body.String(), `"newer":false`) {
		t.Error("it implied the device is up to date")
	}
}

// The page the screen is sent to while it is being replaced. It has to keep
// saying this after the process serving it has stopped, so it must not depend
// on anything from the daemon.
func TestTheScreenIsToldItIsBeingUpgraded(t *testing.T) {
	server := newTestServer(t, config.Default())

	// From this machine's own browser, which is the only thing that ever asks
	// for it.
	request := httptest.NewRequest(http.MethodGet, "/upgrading?version=0.2.0", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("the page answered %d", response.Code)
	}
	body := response.Body.String()

	for _, words := range []string{"Updating", "0.2.0", "come back on its own"} {
		if !strings.Contains(body, words) {
			t.Errorf("the screen does not say %q", words)
		}
	}
	// Nothing it fetches, because there will be nothing to fetch from.
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", "<script"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the page uses %q, which will not work once the daemon stops", forbidden)
		}
	}
}
