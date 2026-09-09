package browser

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
)

// A playlist that came from the service names a credential; the device holds
// it. That is what lets one playlist be applied to forty screens without a
// dashboard password travelling through the service.
func TestALoginResolvesANamedCredential(t *testing.T) {
	configuration := config.Default()
	configuration.Credentials = []config.Credential{{
		Name:     "the-dashboard",
		Username: "screen",
		Password: config.Secret("a-test-password"),
	}}
	browser := &Browser{configuration: configuration}

	resolved, err := browser.resolveLogin(&config.Login{Credential: "the-dashboard"})
	if err != nil {
		t.Fatalf("resolving: %s", err)
	}
	if resolved.Username != "screen" {
		t.Errorf("username is %q", resolved.Username)
	}
	if resolved.Password.Reveal() != "a-test-password" {
		t.Error("the password did not come from the credential")
	}
}

// A name this device does not hold must fail rather than sign in with nothing.
// An empty password submitted on every rotation is a wrong-credential attempt
// repeated for ever, which is how an account gets locked out -- and it is why
// Login.MinimumInterval exists.
func TestALoginNamingAnUnknownCredentialRefusesToTry(t *testing.T) {
	browser := &Browser{configuration: config.Default()}

	resolved, err := browser.resolveLogin(&config.Login{
		Credential: "not-on-this-device",
		Username:   "someone",
		Password:   config.Secret("an-inline-test-password"),
	})
	if err == nil {
		t.Fatalf("it signed in anyway, as %q", resolved.Username)
	}
	if !strings.Contains(err.Error(), "not-on-this-device") {
		t.Errorf("the error does not name the credential: %s", err)
	}
}

// A playlist written before credentials existed goes on working. Devices in
// service have passwords in their playlists, and a version that stopped
// honouring them would take working screens off their dashboards on upgrade.
func TestAnInlineLoginStillWorks(t *testing.T) {
	browser := &Browser{configuration: config.Default()}

	resolved, err := browser.resolveLogin(&config.Login{
		Username: "someone",
		Password: config.Secret("an-inline-test-password"),
	})
	if err != nil {
		t.Fatalf("resolving: %s", err)
	}
	if resolved.Password.Reveal() != "an-inline-test-password" {
		t.Error("an inline password stopped working")
	}
}

// A value spliced into one of these scripts cannot end the statement it is in.
//
// json.Marshal handles the quote and the backslash. What it does not handle is
// that JSON is not JavaScript: U+2028 and U+2029 sit happily inside a JSON
// string and were line terminators in JavaScript before ES2019, so a value
// carrying one used to close the statement and let what followed run as code.
// These values used to be typed by whoever owned the screen; a playlist can now
// bring them from a service.
func TestAQuotedValueCannotEndTheStatement(t *testing.T) {
	for what, value := range map[string]string{
		"a double quote":   `"; alert(1); "`,
		"a backslash":      `\"; alert(1)`,
		"a line separator": "a b",
		"a paragraph mark": "a b",
		"a newline":        "a\nb",
		"a closing brace":  `"}); alert(1); ({"`,
	} {
		t.Run(what, func(t *testing.T) {
			quoted := quote(value)

			if strings.ContainsAny(quoted[1:len(quoted)-1], "  \n\r") {
				t.Errorf("%s survived into the literal: %q", what, quoted)
			}
			// It still has to mean the same thing, or the rule silently stops
			// matching what somebody wrote.
			var back string
			if err := json.Unmarshal([]byte(quoted), &back); err != nil {
				t.Fatalf("the literal is not readable: %s", err)
			}
			if back != value {
				t.Errorf("became %q, want %q", back, value)
			}
		})
	}
}

// reloadEvery reaches the case item.Reload cannot: a screen showing one thing,
// where nothing ever comes round and the page is loaded once and never again.
//
// The wall this was written for ran six days without a reload, and six of its
// seven camera streams were black. The page was alive the whole time -- it
// answered, it drew, the watchdog was satisfied -- and what it showed was
// nothing.
func TestReloadEveryIsDueOnItsOwnClock(t *testing.T) {
	browser := &Browser{
		configuration: config.Default(),
		lastReload:    map[string]time.Time{},
	}
	item := config.Item{Identifier: "one", URL: "https://example.com/", ReloadEvery: config.Duration(30 * time.Minute)}

	// The first sighting starts the clock rather than reloading: a device that
	// has just started must not reload the page it has only just loaded.
	if browser.reloadDue("one", item) {
		t.Error("it reloaded a page it had only just loaded")
	}
	if _, started := browser.lastReload["one"]; !started {
		t.Fatal("the clock did not start")
	}

	// Not yet.
	browser.lastReload["one"] = time.Now().Add(-29 * time.Minute)
	if browser.reloadDue("one", item) {
		t.Error("it reloaded after 29 minutes of a 30 minute period")
	}

	// Now.
	browser.lastReload["one"] = time.Now().Add(-31 * time.Minute)
	if !browser.reloadDue("one", item) {
		t.Error("it did not reload after 31 minutes of a 30 minute period")
	}

	// And the clock restarts, so it does not reload again immediately.
	if browser.reloadDue("one", item) {
		t.Error("it reloaded twice in a row")
	}
}

// An item that does not ask for it is never reloaded on a timer.
func TestAnItemWithoutReloadEveryIsLeftAlone(t *testing.T) {
	browser := &Browser{configuration: config.Default(), lastReload: map[string]time.Time{}}
	item := config.Item{Identifier: "one", URL: "https://example.com/"}

	browser.lastReload["one"] = time.Now().Add(-100 * time.Hour)
	if browser.reloadDue("one", item) {
		t.Error("an item with no reloadEvery was reloaded anyway")
	}
}
