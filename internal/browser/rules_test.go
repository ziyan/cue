package browser

import (
	"strings"
	"testing"

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
