package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// Two upgrades at once is not a slow upgrade, it is a dead device.
//
// Starting a second helper force-removes the first, and the first may be
// between stopping the old container and starting the new one -- so what is
// left is a machine with no cue on it and a dark screen on a wall. Pressing
// the button twice, or pressing it on the page and then in the on-screen
// menu, is an ordinary thing for somebody to do.
func TestOnlyOneUpgradeRunsAtATime(t *testing.T) {
	server := readyToUpgradeServer(t)
	session := signedIn(t, server)

	// As if one were already under way. Starting a real one here would need a
	// Docker daemon and would try to replace this test's own container.
	if !server.claimUpgrade("9.9.9") {
		t.Fatal("could not claim the upgrade")
	}

	response := do(server, "POST", "/api/v1/upgrade", nil, session)
	if response.Code != http.StatusConflict {
		t.Errorf("a second upgrade answered %d, want %d", response.Code, http.StatusConflict)
	}
	if !strings.Contains(response.Body.String(), "already under way") {
		t.Errorf("it does not say why: %s", response.Body)
	}
}

// readyToUpgradeServer is a server that would actually upgrade if asked: it
// has the setting, something that looks like the Docker socket, and a checker
// that has heard of a newer release.
func readyToUpgradeServer(t *testing.T) *Server {
	t.Helper()

	configuration := config.Default()
	configuration.Upgrade.AllowApply = true
	server := newTestServer(t, configuration)

	socket := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	previous := upgrade.SocketPath
	upgrade.SocketPath = socket
	t.Cleanup(func() { upgrade.SocketPath = previous })

	// A stand-in for GitHub, so this says nothing about the real releases.
	releases := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"tag_name":"v9.9.9","body":"### Fixed\n\n- Something.",` +
			`"published_at":"2026-08-28T00:00:00Z"}`))
	}))
	t.Cleanup(releases.Close)

	checker := upgrade.NewChecker(upgrade.Repository, "0.1.0")
	checker.API = releases.URL
	if _, err := checker.Check(context.Background()); err != nil {
		t.Fatalf("the stand-in release check failed: %s", err)
	}
	return server.WithUpgrades(checker)
}

// And the guard must be the *last* question, not the first: a device that
// cannot upgrade at all should be told that, rather than being told something
// is already running when nothing is.
func TestTheReasonGivenIsTheRealOne(t *testing.T) {
	server := newTestServer(t, config.Default())
	server = server.WithUpgrades(upgrade.NewChecker(upgrade.Repository, "0.1.0"))
	session := signedIn(t, server)
	if !server.claimUpgrade("9.9.9") {
		t.Fatal("could not claim the upgrade")
	}

	// This device has no socket and no allowApply, which is the more useful
	// thing to say.
	response := do(server, "POST", "/api/v1/upgrade", nil, session)
	if response.Code == http.StatusConflict {
		t.Error("it complained about a running upgrade when the device cannot upgrade at all")
	}
	if response.Code != http.StatusForbidden {
		t.Errorf("answered %d, want %d", response.Code, http.StatusForbidden)
	}
}

// An upgrade takes minutes and takes the screen away in the middle of them.
// A page that cannot say one is running shows the button again on every
// reload, which is an invitation to press it twice -- and twice is the one
// thing that must not happen.
func TestThePageCanTellAnUpgradeIsRunning(t *testing.T) {
	server := readyToUpgradeServer(t)
	session := signedIn(t, server)

	if !server.claimUpgrade("9.9.9") {
		t.Fatal("could not claim the upgrade")
	}
	server.upgradeSaying("Fetching the image")

	body := do(server, "GET", "/api/v1/upgrade", nil, session).Body.String()
	for _, expected := range []string{`"running":true`, `"version":"9.9.9"`, `"stage":"Fetching the image"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("the page cannot see %s: %s", expected, body)
		}
	}
}

// And when one fails, the reason stays. Somebody who reloads afterwards must
// find out what happened rather than an interface that looks as though nothing
// was ever tried -- which is exactly what the first failed attempt looked
// like.
func TestAFailedUpgradeSaysSoAfterwards(t *testing.T) {
	server := readyToUpgradeServer(t)
	session := signedIn(t, server)

	if !server.claimUpgrade("9.9.9") {
		t.Fatal("could not claim the upgrade")
	}
	server.upgradeFailed("the upgrade did not happen: No help topic for 'upgrade-swap'")

	body := do(server, "GET", "/api/v1/upgrade", nil, session).Body.String()
	if !strings.Contains(body, `"running":false`) {
		t.Errorf("it still says an upgrade is running: %s", body)
	}
	if !strings.Contains(body, "No help topic") {
		t.Errorf("the reason was lost: %s", body)
	}

	// And the claim is back, so somebody can try again.
	if !server.claimUpgrade("9.9.9") {
		t.Error("a failed upgrade kept the claim, so nobody can try again")
	}
}
