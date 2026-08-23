package browser

import (
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

func newTestBrowser(t *testing.T, change func(*config.Configuration)) *Browser {
	t.Helper()
	configuration := config.Default()
	configuration.Paths.State = t.TempDir()
	configuration.Paths.Runtime = t.TempDir()
	// The account does not exist on a developer's machine, and nothing here
	// starts a process anyway.
	configuration.Browser.User = ""
	if change != nil {
		change(configuration)
	}
	configuration.Normalize()

	return New(configuration, ":9", "/run/cue/Xauthority")
}

func commandLine(t *testing.T, browser *Browser) string {
	t.Helper()
	return strings.Join(browser.arguments(), " ")
}

func TestTheFlagsThatMakeItAKioskAreThere(t *testing.T) {
	line := commandLine(t, newTestBrowser(t, nil))

	for flag, why := range map[string]string{
		"--kiosk":                                    "otherwise there is a tab strip and an address bar on the wall",
		"--no-first-run":                             "a fresh profile otherwise shows a welcome page on the screen in the lobby",
		"--disable-session-crashed-bubble":           "after a power cut, a restore bar sits across the top of the dashboard",
		"--overscroll-history-navigation=0":          "a stray swipe on a touchscreen navigates away from the page",
		"--autoplay-policy=no-user-gesture-required": "there is nobody to click play",
		"--password-store=basic":                     "Chromium otherwise blocks for seconds looking for a keyring that is not there",
	} {
		if !strings.Contains(line, flag) {
			t.Errorf("%s is missing: %s", flag, why)
		}
	}
}

func TestBackgroundTabsAreNotThrottled(t *testing.T) {
	// The one that matters most on a screen showing several dashboards: a
	// throttled tab polls once a minute instead of every ten seconds, and
	// then shows stale numbers for the first seconds after it comes round.
	line := commandLine(t, newTestBrowser(t, nil))
	for _, flag := range []string{
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding",
	} {
		if !strings.Contains(line, flag) {
			t.Errorf("%s is missing; a dashboard in a background tab would stop updating", flag)
		}
	}
}

func TestTheSandboxIsOnlyGivenUpWhenAskedFor(t *testing.T) {
	on := commandLine(t, newTestBrowser(t, nil))
	if strings.Contains(on, "--no-sandbox") {
		t.Errorf("the sandbox was given up although it was not asked for:\n%s", on)
	}

	off := commandLine(t, newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Browser.Sandbox = false
	}))
	if !strings.Contains(off, "--no-sandbox") {
		t.Errorf("the sandbox was asked to be off and was not:\n%s", off)
	}
}

func TestCertificatesAreOnlyIgnoredWhenAskedFor(t *testing.T) {
	// Turning this on removes the protection TLS was there to give, so it
	// must never appear by itself.
	on := commandLine(t, newTestBrowser(t, nil))
	if strings.Contains(on, "--ignore-certificate-errors") {
		t.Errorf("certificate errors are ignored by default:\n%s", on)
	}

	asked := commandLine(t, newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Browser.IgnoreCertificateErrors = true
	}))
	if !strings.Contains(asked, "--ignore-certificate-errors") {
		t.Error("certificate errors were asked to be ignored and were not")
	}
}

func TestTheCacheIsWhereTheConfigurationSaysAndIsEmptiedWhenEphemeral(t *testing.T) {
	ephemeral := newTestBrowser(t, nil)
	if !strings.HasPrefix(ephemeral.cacheDirectory(), ephemeral.configuration.Paths.Runtime) {
		t.Errorf("an ephemeral cache is at %s, which is not under the runtime directory and so survives a restart",
			ephemeral.cacheDirectory())
	}

	kept := newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Browser.EphemeralCache = false
	})
	if !strings.HasPrefix(kept.cacheDirectory(), kept.configuration.Paths.State) {
		t.Errorf("a kept cache is at %s, which is not under the state directory and so would not survive",
			kept.cacheDirectory())
	}
}

func TestSoundIsMutedWhenItIsSwitchedOff(t *testing.T) {
	// A screen showing camera feeds in an open-plan office wants exactly this.
	line := commandLine(t, newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Audio.Enabled = false
	}))
	if !strings.Contains(line, "--mute-audio") {
		t.Errorf("sound was switched off and the browser was not muted:\n%s", line)
	}
}

func TestAChosenSoundCardIsNamed(t *testing.T) {
	line := commandLine(t, newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Audio.Enabled = true
		configuration.Audio.Sink = "plughw:HDMI"
	}))
	if !strings.Contains(line, "--alsa-output-device=plughw:HDMI") {
		t.Errorf("the chosen sound card did not reach the command line:\n%s", line)
	}
}

func TestTheWindowIsSizedToTheScreenOnceItIsKnown(t *testing.T) {
	browser := newTestBrowser(t, nil)
	if line := commandLine(t, browser); strings.Contains(line, "--window-size") {
		t.Errorf("a window size was given before the screen was known:\n%s", line)
	}

	browser.SetScreenSize(3840, 2160)
	if line := commandLine(t, browser); !strings.Contains(line, "--window-size=3840,2160") {
		t.Errorf("the window was not sized to the screen:\n%s", line)
	}
}

func TestExtraArgumentsComeLastSoTheyCanOverride(t *testing.T) {
	browser := newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Browser.ExtraArguments = []string{"--force-device-scale-factor=2"}
	})
	list := browser.arguments()
	if list[len(list)-1] != "--force-device-scale-factor=2" {
		t.Errorf("the extra argument is not last: %v", list[len(list)-3:])
	}
}

func TestTheDebuggingPortIsBoundToThisMachineOnly(t *testing.T) {
	// It is an unauthenticated interface to the browser.
	line := commandLine(t, newTestBrowser(t, nil))
	if !strings.Contains(line, "--remote-debugging-address=127.0.0.1") {
		t.Errorf("the debugging port is not bound to the loopback address:\n%s", line)
	}
}

func TestARestartIsOnlyNeededForWhatCannotBeChangedWhileRunning(t *testing.T) {
	// Restarting the browser blanks the screen for several seconds, so
	// editing a playlist must not do it.
	previous := config.Default()
	previous.Normalize()

	playlistChanged := previous.Clone()
	playlistChanged.Playlist.Items = append(playlistChanged.Playlist.Items,
		config.Item{Identifier: "aaaaaaaaaaaaaaaa", URL: "https://example.com/"})
	if restartNeeded(previous, playlistChanged) {
		t.Error("changing the playlist would restart the browser and blank the screen")
	}

	commandLineChanged := previous.Clone()
	commandLineChanged.Browser.Sandbox = !previous.Browser.Sandbox
	if !restartNeeded(previous, commandLineChanged) {
		t.Error("changing the command line was accepted without a restart, so it would have done nothing")
	}
}

func TestDarkModeIsAskedForByDefaultAndCanBeTurnedOff(t *testing.T) {
	// A screen on a wall is usually in a room where a page of white at full
	// brightness is the brightest thing in it.
	on := commandLine(t, newTestBrowser(t, nil))
	for _, flag := range []string{"--force-dark-mode", "--force-prefers-color-scheme=dark"} {
		if !strings.Contains(on, flag) {
			t.Errorf("%s is missing; pages will render light by default", flag)
		}
	}

	off := commandLine(t, newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Browser.DarkMode = false
	}))
	if strings.Contains(off, "--force-dark-mode") {
		t.Errorf("dark mode was turned off and still asked for:\n%s", off)
	}
}
