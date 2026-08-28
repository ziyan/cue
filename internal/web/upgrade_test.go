package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/upgrade"
)

// The upgrade button is management, not proximity. Standing in front of a
// screen authorises changing what it shows and how it reaches the network; it
// does not authorise replacing the software on the machine.
func TestUpgradingNeedsThePasswordAndNotJustTheScreen(t *testing.T) {
	server := newTestServer(t, config.Default())
	signedIn(t, server)
	_, pass := openMenu(t, server)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if code := passRequest(t, server, method, "/api/v1/upgrade", nil, pass); code == http.StatusOK ||
			code == http.StatusAccepted {
			t.Errorf("%s /api/v1/upgrade was allowed from the screen's own pass: %d", method, code)
		}
		if code := do(server, method, "/api/v1/upgrade", nil, nil).Code; code != http.StatusUnauthorized {
			t.Errorf("%s /api/v1/upgrade from the network answered %d, want 401", method, code)
		}
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
