package browser

import (
	"fmt"
	"os/user"
	"strconv"
	"strings"

	"github.com/ziyan/cue/internal/audio"
	"github.com/ziyan/cue/internal/input"
)

// arguments builds Chromium's command line.
//
// Every flag here is answering a specific way a kiosk goes wrong, and the
// comments say which, because in six months the list looks arbitrary and
// somebody will tidy it.
// languageTag returns the configured language if it is one, and nothing if it
// is not.
//
// Checked rather than trusted because this goes on a command line. A value out
// of a configuration file with a space in it would become another flag, and
// the flags next to it are the ones turning the sandbox on.
func languageTag(configured string) string {
	tag := strings.TrimSpace(configured)
	if tag == "" || len(tag) > 16 {
		return ""
	}
	for _, letter := range tag {
		switch {
		case letter >= 'a' && letter <= 'z':
		case letter >= 'A' && letter <= 'Z':
		case letter == '-':
		default:
			return ""
		}
	}
	return tag
}

// acceptLanguages is what the browser asks servers for: the language chosen,
// and English behind it. A site with nothing in the first still answers.
func acceptLanguages(tag string) string {
	if strings.EqualFold(tag, "en") || strings.HasPrefix(strings.ToLower(tag), "en-") {
		return tag
	}
	return tag + ",en"
}

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

		// The language this screen speaks, asked for in the pages it shows.
		//
		// Without this the setting reached the menu and nothing else: a screen
		// set to Japanese opened the menu in Japanese and every dashboard in
		// whatever Chromium happened to default to, which on a fresh profile
		// is the machine's locale and usually English. --lang is Chromium's
		// own furniture and --accept-lang is what it asks servers for; a site
		// that speaks more than one language reads the second.
		//
		// Added below rather than here, because a language nobody has chosen
		// should leave the command line alone.
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
		// Zero asks Chromium to pick a free port and write it into the
		// profile, which is the only way to be sure the daemon is talking to
		// the browser it started; see resolveClient.
		"--remote-debugging-port=0",
	}

	self.mutex.Lock()
	width, height := self.screenWidth, self.screenHeight
	self.mutex.Unlock()
	if width > 0 && height > 0 {
		arguments = append(arguments, fmt.Sprintf("--window-size=%d,%d", width, height))
	}

	// Chromium keeps only the last --enable-features it is given, so every
	// feature this daemon wants is collected here and emitted once at the
	// end. Two of these flags on one command line means the first is silently
	// discarded, which is how a setting appears to be applied and is not.
	var features []string

	// Scrollbars that float over the page rather than taking a strip of it,
	// and that fade out when nothing is scrolling. A dashboard laid out for a
	// screen loses a column to a permanent scrollbar, and on a wall nobody is
	// going to scroll anyway.
	features = append(features,
		"OverlayScrollbar",
		"OverlayScrollbarFlashAfterAnyScrollUpdate",
		"OverlayScrollbarFlashWhenMouseEnter",
	)

	if settings.DarkMode {
		// One flag, and it was three. Chromium ignores a switch it does not
		// know without a word, so --force-prefers-color-scheme and the
		// WebUIDarkMode feature — both inherited from older setups, neither
		// present in this Chromium — looked like they were doing the work and
		// were doing nothing at all. Checked against the binary before being
		// written here.
		//
		// This one does the job on its own: it darkens the browser's own
		// pages and makes prefers-color-scheme report dark, which is verified
		// against a live browser in TestDarkModeReachesThePage.
		arguments = append(arguments, "--force-dark-mode")

		if settings.ForceDarkContent {
			// Telling a page we prefer dark only helps if the page is
			// listening. Plenty are not — they have a theme of their own, set
			// somewhere in an account, and they render light on a wall in a
			// dark room whatever the browser says. This darkens them anyway,
			// by inverting the page's own colours.
			features = append(features, "WebContentsForceDark")
		}
	}

	if settings.DeviceScaleFactor > 0 {
		// Without this the browser scales the page by whatever the X server
		// says the panel's DPI is, and a screen on a wall wants the pixels it
		// has. See config.Browser.DeviceScaleFactor.
		arguments = append(arguments,
			fmt.Sprintf("--force-device-scale-factor=%g", settings.DeviceScaleFactor))
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

	// The language the screen speaks. See the note beside the flag list: a
	// language nobody has chosen leaves the command line alone, so a device
	// that has never been told keeps whatever Chromium would have done.
	if tag := languageTag(self.configuration.Device.Language); tag != "" {
		arguments = append(arguments,
			"--lang="+tag,
			"--accept-lang="+acceptLanguages(tag))
	}

	// Anything the operator added by hand goes last, so it wins — except a
	// --enable-features of their own, which is merged rather than allowed to
	// replace the list above.
	extra, extraFeatures := separateFeatures(settings.ExtraArguments)
	features = append(features, extraFeatures...)
	arguments = append(arguments, "--enable-features="+strings.Join(deduplicate(features), ","))
	arguments = append(arguments, extra...)
	return arguments
}

// separateFeatures pulls the feature names out of any --enable-features the
// operator wrote, returning the rest of their arguments unchanged.
func separateFeatures(arguments []string) (rest, features []string) {
	const flag = "--enable-features="
	for _, argument := range arguments {
		if names, found := strings.CutPrefix(argument, flag); found {
			for _, name := range strings.Split(names, ",") {
				if name = strings.TrimSpace(name); name != "" {
					features = append(features, name)
				}
			}
			continue
		}
		rest = append(rest, argument)
	}
	return rest, features
}

func deduplicate(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
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
