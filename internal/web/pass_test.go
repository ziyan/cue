package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
)

func TestPassStartsLiveAndUnelevated(t *testing.T) {
	held := newPasses()

	value, err := held.mint()
	if err != nil {
		t.Fatal(err)
	}

	live, elevated := held.check(value)
	if !live {
		t.Error("a pass just minted is not live")
	}
	if elevated {
		t.Error("a pass is elevated before anybody proved the password")
	}
}

func TestPassElevatesOnce(t *testing.T) {
	held := newPasses()
	value, _ := held.mint()

	if !held.elevate(value) {
		t.Fatal("a live pass refused to elevate")
	}
	if _, elevated := held.check(value); !elevated {
		t.Error("the pass did not stay elevated")
	}
	if held.elevate("something else entirely") {
		t.Error("a value that is not a pass elevated")
	}
}

// This is the whole reason a pass is remembered rather than signed. A signed
// token would still be good here.
func TestRevokingAPassEndsItImmediately(t *testing.T) {
	held := newPasses()
	value, _ := held.mint()
	held.elevate(value)

	held.revoke(value)

	live, elevated := held.check(value)
	if live || elevated {
		t.Error("a revoked pass is still worth something")
	}
}

func TestAPassDoesNotOutliveItsLifetime(t *testing.T) {
	held := newPasses()
	value, _ := held.mint()
	held.elevate(value)

	// Reach in and age it, rather than waiting a quarter of an hour.
	held.mutex.Lock()
	held.live[value].expires = time.Now().Add(-time.Second)
	held.mutex.Unlock()

	if live, elevated := held.check(value); live || elevated {
		t.Error("a pass past its lifetime is still worth something")
	}
}

func TestNothingIsNotAPass(t *testing.T) {
	held := newPasses()
	for _, value := range []string{"", "not a pass", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if live, elevated := held.check(value); live || elevated {
			t.Errorf("%q was accepted as a pass", value)
		}
	}
}

// The behaviour the whole change is for: the authority ends when the menu
// closes, so the next person to walk up to the screen is asked again.
//
// Before this, the gate posted the password to the ordinary sign-in endpoint,
// which set a twelve-hour cookie in the browser bolted to the wall. The menu
// closed and the cookie did not.
func TestClosingTheMenuEndsTheAuthority(t *testing.T) {
	server := newTestServer(t, config.Default())
	signedIn(t, server) // gives the device a password
	_, pass := openMenu(t, server)

	if code := passRequest(t, server, http.MethodPost, "/api/v1/menu/restart/browser", nil, pass); code == http.StatusOK {
		t.Fatal("the menu acted before anybody typed the password")
	}

	if code := passRequest(t, server, http.MethodPost, "/api/v1/screen/unlock",
		map[string]string{"password": testPassword}, pass); code != http.StatusOK {
		t.Fatalf("unlocking answered %d", code)
	}
	if code := passRequest(t, server, http.MethodPost, "/api/v1/menu/restart/browser", nil, pass); code != http.StatusOK {
		t.Fatalf("an unlocked menu could not act: %d", code)
	}

	if code := passRequest(t, server, http.MethodPost, "/api/v1/screen/close", nil, pass); code != http.StatusOK {
		t.Fatalf("closing answered %d", code)
	}

	if code := passRequest(t, server, http.MethodPost, "/api/v1/menu/restart/browser", nil, pass); code == http.StatusOK {
		t.Error("the pass still worked after the menu was closed")
	}
}

// The wrong password must not elevate, and must not be a way to tell whether
// a pass is valid either.
func TestTheWrongPasswordDoesNotOpenTheMenu(t *testing.T) {
	server := newTestServer(t, config.Default())
	signedIn(t, server)
	_, pass := openMenu(t, server)

	if code := passRequest(t, server, http.MethodPost, "/api/v1/screen/unlock",
		map[string]string{"password": "not the test password"}, pass); code != http.StatusUnauthorized {
		t.Errorf("the wrong password answered %d", code)
	}
	if code := passRequest(t, server, http.MethodPost, "/api/v1/menu/restart/browser", nil, pass); code == http.StatusOK {
		t.Error("the menu acted after the wrong password")
	}
}

// A pass belongs to the page it was minted for. One menu's pass is not
// another's, and neither is a value somebody made up.
func TestAPassFromNowhereIsRefused(t *testing.T) {
	server := newTestServer(t, config.Default())
	signedIn(t, server)

	for _, pass := range []string{"", "invented"} {
		if code := passRequest(t, server, http.MethodPost, "/api/v1/screen/unlock",
			map[string]string{"password": testPassword}, pass); code != http.StatusUnauthorized {
			t.Errorf("a request with pass %q answered %d", pass, code)
		}
	}
}

// Once a device has a password, choosing a new one from the screen is not on
// offer. Otherwise the gate would be a way past itself.
func TestTheFirstPasswordCannotBeSetTwice(t *testing.T) {
	server := newTestServer(t, config.Default())
	signedIn(t, server)
	_, pass := openMenu(t, server)

	code := passRequest(t, server, http.MethodPost, "/api/v1/screen/password",
		map[string]string{"password": "a different test password"}, pass)
	if code != http.StatusConflict {
		t.Errorf("setting a second first password answered %d, want %d", code, http.StatusConflict)
	}
	if code := passRequest(t, server, http.MethodPost, "/api/v1/menu/restart/browser", nil, pass); code == http.StatusOK {
		t.Error("the refused attempt elevated the pass anyway")
	}
}

// Eight characters is the floor the setup wizard applies, and one floor is
// enough. Two would mean the lower of them was the one that mattered.
func TestAShortFirstPasswordIsRefused(t *testing.T) {
	server := newTestServer(t, config.Default())
	_, pass := openMenu(t, server)

	if code := passRequest(t, server, http.MethodPost, "/api/v1/screen/password",
		map[string]string{"password": "test"}, pass); code != http.StatusBadRequest {
		t.Errorf("a five character password answered %d", code)
	}
	if server.isSetUp() {
		t.Error("the short password was kept")
	}
}
