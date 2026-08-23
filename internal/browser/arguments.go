package browser

import (
	"fmt"
	"os/user"
	"strconv"

	"github.com/ziyan/cue/internal/audio"
	"github.com/ziyan/cue/internal/input"
)

// arguments builds Chromium's command line.
//
// Every flag here is answering a specific way a kiosk goes wrong, and the
// comments say which, because in six months the list looks arbitrary and
// somebody will tidy it.
func (self *Browser) arguments() []string {
	settings := self.configuration.Browser

	arguments := []string{
		// Full screen, no tab strip, no address bar, and no way for somebody
		// walking past to leave the page.
		"--kiosk",
		"--start-fullscreen",
		"--window-position=0,0",

		// A first run on a fresh profile otherwise shows a welcome page, asks
		// to be the default browser, and offers to import bookmarks, all on
		// the screen in the lobby.
		"--no-first-run",
		"--no-default-browser-check",
		"--noerrdialogs",
		"--disable-infobars",

		// After a power cut, "Chrome didn't shut down correctly" appears
		// across the top of the dashboard. The profile flag is cleared before
		// each start as well; these suppress what is left.
		"--disable-session-crashed-bubble",
		"--hide-crash-restore-bubble",

		// A kiosk has no back button and nobody to press it, but a stray
		// two-finger swipe on a touchscreen still navigates away.
		"--disable-pinch",
		"--overscroll-history-navigation=0",

		// This is the one that matters most on a screen showing several
		// dashboards. Chromium throttles timers in tabs that are not visible,
		// so a dashboard that polls every ten seconds updates once a minute
		// while it is in the background and then shows stale numbers for the
		// first few seconds after it comes round.
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-renderer-backgrounding",

		// Video and camera feeds must start on their own: there is nobody to
		// click play.
		"--autoplay-policy=no-user-gesture-required",

		// Chromium looks for a system keyring to store passwords in and
		// blocks for several seconds when there is not one.
		"--password-store=basic",

		// Nothing here should phone home, download an update, or record how
		// the device is used.
		"--disable-component-update",
		"--disable-breakpad",
		"--disable-sync",
		"--metrics-recording-only",
		"--disable-domain-reliability",

		// A white flash between pages is very visible on a large screen in a
		// dark room.
		"--default-background-color=FF000000",

		// The dialog Chromium shows when a page stops responding would sit on
		// the screen until somebody dismissed it. The daemon's watchdog is
		// what handles an unresponsive page here.
		"--disable-hang-monitor",

		"--user-data-dir=" + self.profileDirectory(),
		"--disk-cache-dir=" + self.cacheDirectory(),

		// The debugging port is how the daemon drives all of this. It is bound
		// to the loopback interface and nothing outside the container can
		// reach it.
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(settings.DebuggingPort),
	}

	self.mutex.Lock()
	width, height := self.screenWidth, self.screenHeight
	self.mutex.Unlock()
	if width > 0 && height > 0 {
		arguments = append(arguments, fmt.Sprintf("--window-size=%d,%d", width, height))
	}

	if !settings.Sandbox {
		// Spelled out rather than buried: without the sandbox, a bug in the
		// renderer is a bug in whatever account the browser runs as, and this
		// browser renders whatever the network serves it.
		arguments = append(arguments, "--no-sandbox")
	}
	if settings.IgnoreCertificateErrors {
		arguments = append(arguments, "--ignore-certificate-errors")
	}

	// Shared memory in a container defaults to 64 megabytes, which Chromium
	// exhausts and then crashes tabs it cannot allocate for. Using files
	// instead is slower and always works; the compose file also raises the
	// limit, and this is the belt to that pair of braces.
	arguments = append(arguments, "--disable-dev-shm-usage")

	// Where the sound comes out, or whether there is any. See internal/audio.
	arguments = append(arguments, audio.OutputArguments(&self.configuration.Audio)...)

	// A screen somebody touches. Chromium decides at start-up whether touch
	// is available, and gets it wrong inside a container often enough to be
	// worth telling it: without this a dashboard renders its desktop layout
	// and its buttons are too small for a finger.
	if devices, err := input.Devices(); err == nil && input.HasTouchscreen(devices) {
		log.Noticef("this machine has a touchscreen; enabling touch in the browser")
		arguments = append(arguments, "--touch-events=enabled")
	}

	arguments = append(arguments, settings.ExtraArguments...)
	return arguments
}

// lookupAccount turns an account name into the user and group id that own the
// browser's files.
func lookupAccount(name string) (int, int, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return 0, 0, fmt.Errorf("browser: no account named %q on this system: %w", name, err)
	}
	userId, err := strconv.Atoi(account.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("browser: account %q has a user id that is not a number: %w", name, err)
	}
	groupId, err := strconv.Atoi(account.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("browser: account %q has a group id that is not a number: %w", name, err)
	}
	return userId, groupId, nil
}
