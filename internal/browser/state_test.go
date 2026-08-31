package browser

import (
	"testing"

	"github.com/ziyan/cue/internal/config"
)

func TestAChangedCommandLineNeedsARestart(t *testing.T) {
	// Every one of these is fixed when Chromium starts, so a change to it
	// that does not restart the browser is a setting the operator saved,
	// which the interface then shows as saved, and which is not in force.
	for name, change := range map[string]func(*config.Browser){
		"the dark mode":          func(browser *config.Browser) { browser.DarkMode = !browser.DarkMode },
		"the sandbox":            func(browser *config.Browser) { browser.Sandbox = !browser.Sandbox },
		"the certificate errors": func(browser *config.Browser) { browser.IgnoreCertificateErrors = true },
		"an extra argument": func(browser *config.Browser) {
			browser.ExtraArguments = []string{"--enable-features=WebContentsForceDark"}
		},
		"a different extra argument": func(browser *config.Browser) {
			browser.ExtraArguments = []string{"--something-else"}
		},
		"a certificate": func(browser *config.Browser) {
			browser.CertificateAuthorities = []string{"-----BEGIN CERTIFICATE-----"}
		},
	} {
		previous := config.Default()
		previous.Browser.ExtraArguments = []string{"--already-here"}
		updated := previous.Clone()
		change(&updated.Browser)

		if !restartNeeded(previous, updated) {
			t.Errorf("changing %s did not ask for a restart, so the change would not be in force", name)
		}
	}
}

func TestAChangeThatIsNotOnTheCommandLineDoesNotRestart(t *testing.T) {
	// Restarting blanks the screen for several seconds. An operator editing a
	// playlist should not see that.
	previous := config.Default()
	updated := previous.Clone()
	updated.Playlist.Items = append(updated.Playlist.Items, config.Item{URL: "https://example.com/"})

	if restartNeeded(previous, updated) {
		t.Error("adding a page to the playlist restarted the browser")
	}
}

// Changing the language restarts the browser, because Chromium reads it once.
//
// Without this the setting would be accepted, written to the file, applied to
// the menu, and do nothing at all to the pages on the screen until something
// else happened to restart the browser -- which on a wall display is a
// watchdog or a power cut. Accepted and silently ignored is the worst of the
// three possible answers.
func TestChangingTheLanguageRestartsTheBrowser(t *testing.T) {
	before := config.Default()
	before.Device.Language = "en"
	after := before.Clone()
	after.Device.Language = "ja"

	if !restartNeeded(before, after) {
		t.Error("the language changed and the browser was left running with the old one")
	}
	// And nothing needless: the same language does not blank the screen.
	if restartNeeded(before, before.Clone()) {
		t.Error("an unchanged configuration restarts the browser")
	}
}
