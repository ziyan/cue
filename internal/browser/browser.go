// Package browser owns Chromium: starting it, keeping one tab per playlist
// item, rotating between them, keeping pages logged in, getting rid of the
// dialogs that appear on top of them, and answering "what is on the screen
// right now" with a picture.
//
// Everything here is done over the DevTools protocol rather than through a
// browser extension. See internal/util/cdp for why.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/util/cdp"
	"github.com/ziyan/cue/internal/util/executable"
)

var log = logging.MustGetLogger("browser")

// Browser is the running Chromium and everything the daemon does with it.
type Browser struct {
	configuration *config.Configuration
	client        *cdp.Client

	displayName       string
	authorityFilename string
	screenWidth       int
	screenHeight      int

	mutex sync.Mutex

	// browserSession is the connection to Chromium itself, used to open,
	// close and switch tabs.
	browserSession *cdp.Session

	// sessions are the connections to individual tabs, kept open because the
	// rules are evaluated every few seconds and reattaching each time would
	// be most of the work.
	sessions map[string]*cdp.Session

	// tabs maps a playlist item's identifier to the tab showing it.
	tabs map[string]string

	current      string
	currentSince time.Time
	lastLogin    map[string]time.Time
	loginCount   map[string]int
	dismissCount map[string]int
	ready        bool
}

// New returns a browser for the given configuration. Nothing starts until the
// daemon supervises the Settings it returns.
func New(configuration *config.Configuration, displayName, authorityFilename string) *Browser {
	return &Browser{
		configuration:     configuration,
		client:            cdp.New("127.0.0.1:" + strconv.Itoa(configuration.Browser.DebuggingPort)),
		displayName:       displayName,
		authorityFilename: authorityFilename,
		sessions:          map[string]*cdp.Session{},
		tabs:              map[string]string{},
		lastLogin:         map[string]time.Time{},
		loginCount:        map[string]int{},
		dismissCount:      map[string]int{},
	}
}

// SetScreenSize tells the browser how large the screen is, so that the window
// can be sized to it. The daemon calls this after the display has been
// arranged and again whenever it changes.
func (self *Browser) SetScreenSize(width, height int) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.screenWidth, self.screenHeight = width, height
}

// Client is the DevTools client, for the parts of the daemon that need to ask
// the browser something directly.
func (self *Browser) Client() *cdp.Client {
	return self.client
}

// Settings builds the supervisor settings for Chromium.
func (self *Browser) Settings() *supervise.Settings {
	// The browser's output is always captured, because the reason it would
	// not start is only ever in it, and a supervisor that discards that
	// leaves nothing to look at but "exited before it was ready". What the
	// setting changes is how loudly it is logged: at DEBUG it is out of the
	// way until somebody turns the level up, and log.browserOutput raises it
	// for a device being investigated from a distance.
	outputLevel := logging.DEBUG
	if self.configuration.Log.BrowserOutput {
		outputLevel = logging.INFO
	}

	binary, err := executable.Resolve(self.configuration.Browser.Binary, knownBrowsers...)
	if err != nil {
		// Reported rather than returned: the supervisor will try to start it,
		// fail, and say so on a backoff, which is the same shape as every
		// other reason the browser will not start.
		log.Errorf("%s", err)
		binary = self.configuration.Browser.Binary
	}

	return &supervise.Settings{
		Name:          "chromium",
		Path:          binary,
		Arguments:     self.arguments(),
		Restart:       true,
		User:          self.configuration.Browser.User,
		BeforeStart:   self.prepare,
		Ready:         self.probe,
		ReadyTimeout:  60 * time.Second,
		AfterReady:    self.afterReady,
		CaptureOutput: true,
		OutputLevel:   outputLevel,
		StopTimeout:   10 * time.Second,
		Environment: supervise.Environ(supervise.Inherit(), map[string]string{
			"DISPLAY":    self.displayName,
			"XAUTHORITY": self.authorityFilename,
			"HOME":       self.profileParent(),
			// Chromium looks for a keyring to store passwords in and stalls
			// for several seconds when there is not one. Saying plainly that
			// there is no desktop environment skips the search.
			"XDG_CURRENT_DESKTOP": "X-Generic",
			"XDG_RUNTIME_DIR":     self.configuration.Paths.Runtime,
		}),
	}
}

// knownBrowsers are where a real Chromium executable is found on the systems
// this runs on. The image ships the first. They are tried only when the
// configured name is not a program that can be run — which on Debian it never
// is, /usr/bin/chromium being a shell script.
var knownBrowsers = []string{
	"/usr/lib/chromium/chromium",
	"/usr/lib/chromium-browser/chromium-browser",
	"/opt/google/chrome/chrome",
	"/usr/lib/chrome/chrome",
}

func (self *Browser) profileParent() string {
	return filepath.Join(self.configuration.Paths.State, "browser")
}

func (self *Browser) profileDirectory() string {
	return filepath.Join(self.profileParent(), "profile")
}

func (self *Browser) cacheDirectory() string {
	if self.configuration.Browser.EphemeralCache {
		return filepath.Join(self.configuration.Paths.Runtime, "browser-cache")
	}
	return filepath.Join(self.profileParent(), "cache")
}

// probe is the readiness check: the browser answers on its debugging port.
func (self *Browser) probe(ctx context.Context) error {
	version, err := self.client.Version(ctx)
	if err != nil {
		return err
	}
	log.Debugf("the browser is %s", version.Browser)
	return nil
}

// prepare makes the directories the browser needs and clears the two things
// that stop a kiosk coming back cleanly after a power cut.
func (self *Browser) prepare(ctx context.Context) error {
	self.forgetSessions()

	for _, directory := range []string{self.profileParent(), self.profileDirectory()} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("browser: create %s: %w", directory, err)
		}
	}

	cache := self.cacheDirectory()
	if self.configuration.Browser.EphemeralCache {
		// A corrupted cache produces a page that will not load while
		// everything else looks healthy, and it survives every restart, so it
		// presents as a device that has to be reimaged. Making the cache
		// disposable removes the whole class of fault; on a dashboard served
		// from the local network, fetching the assets again costs nothing.
		if err := os.RemoveAll(cache); err != nil {
			log.Warningf("cannot empty the browser cache at %s: %s", cache, err)
		}
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return fmt.Errorf("browser: create %s: %w", cache, err)
	}

	if err := self.clearCrashFlag(); err != nil {
		log.Warningf("%s", err)
	}

	if err := self.giveProfileToBrowserUser(); err != nil {
		log.Warningf("%s", err)
	}

	return nil
}

// clearCrashFlag rewrites the two fields Chromium uses to decide whether it
// shut down cleanly. A device that lost power shows a "Chrome didn't shut down
// correctly / Restore pages?" bar across the top of the dashboard, and on a
// screen nobody touches it stays there until somebody walks over with a mouse.
func (self *Browser) clearCrashFlag() error {
	filename := filepath.Join(self.profileDirectory(), "Default", "Preferences")
	content, err := os.ReadFile(filename)
	if err != nil {
		// There is no profile yet, which is the case on a first run.
		return nil
	}

	var preferences map[string]interface{}
	if err := json.Unmarshal(content, &preferences); err != nil {
		return fmt.Errorf("browser: cannot read %s, so the crash bar may appear: %w", filename, err)
	}

	profile, _ := preferences["profile"].(map[string]interface{})
	if profile == nil {
		profile = map[string]interface{}{}
		preferences["profile"] = profile
	}
	if profile["exit_type"] == "Normal" && profile["exited_cleanly"] == true {
		return nil
	}
	profile["exit_type"] = "Normal"
	profile["exited_cleanly"] = true

	updated, err := json.Marshal(preferences)
	if err != nil {
		return fmt.Errorf("browser: cannot rewrite %s: %w", filename, err)
	}
	if err := os.WriteFile(filename, updated, 0o600); err != nil {
		return fmt.Errorf("browser: cannot write %s: %w", filename, err)
	}
	log.Debugf("cleared the browser's unclean-shutdown flag")
	return nil
}

// giveProfileToBrowserUser hands the profile and cache to the account the
// browser runs as. The daemon is root and creates them; the browser is not and
// has to own them.
func (self *Browser) giveProfileToBrowserUser() error {
	name := self.configuration.Browser.User
	if name == "" {
		return nil
	}
	userId, groupId, err := lookupAccount(name)
	if err != nil {
		return err
	}
	for _, root := range []string{self.profileParent(), self.cacheDirectory()} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			return os.Lchown(path, userId, groupId)
		})
		if err != nil {
			return fmt.Errorf("browser: cannot give %s to %q: %w", root, name, err)
		}
	}
	return nil
}
