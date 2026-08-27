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
	"strings"
	"sync"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/util/cdp"
	"github.com/ziyan/cue/internal/util/devices"
	"github.com/ziyan/cue/internal/util/executable"
)

var log = logging.MustGetLogger("browser")

// Browser is the running Chromium and everything the daemon does with it.
type Browser struct {
	// OnEveryPage is a script added to every tab, now and on every page they
	// visit afterwards. It carries the control that puts this device back into
	// setup, which has to be reachable from whatever is on the screen.
	OnEveryPage string

	// SetupInProgress reports whether the device is being set up over the air.
	// While it is, the screen shows the daemon's own page rather than the
	// playlist, because that page carries the code somebody has to scan. The
	// daemon sets this; it is nil in tests and in tools, where it reads as
	// "not being set up".
	SetupInProgress func() bool

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

	// unexpectedTabs are the windows seen at the last check that this daemon
	// did not open. A window has to appear in two consecutive checks before
	// it is closed; see closeUnexpectedTabs.
	unexpectedTabs map[string]bool

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

// Client is the protocol client, for the parts of the daemon that need to ask
// the browser something directly. It is nil until the browser has started and
// said which port it is listening on.
func (self *Browser) Client() *cdp.Client {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.client
}

// activePortFilename is where Chromium writes the port it chose. The file is
// in the profile, so it belongs to this browser and to no other: reading it
// is how the daemon knows it is talking to the browser it started rather than
// to whatever else happens to be listening.
func (self *Browser) activePortFilename() string {
	return filepath.Join(self.profileDirectory(), "DevToolsActivePort")
}

// resolveClient works out where this browser is listening and points a client
// at it.
//
// The port always comes from DevToolsActivePort, which Chromium writes into
// its own profile, and never from the configuration — even when the
// configuration names one. The file is removed before each start, so a stale
// one from a browser that has gone cannot be mistaken for a live one, and the
// port in it can only have been written by the browser this daemon started.
//
// Reading it unconditionally is the whole point, and was learnt the hard way
// twice. The first time, the default was a fixed 9222 and another container on
// the host published 9222: the daemon connected to that container's Chromium
// and drove it for an afternoon. Changing the default to 0 fixed new devices
// and did nothing for the one already deployed, because the port it had been
// given was written into its configuration file and went on overriding the
// new default. A configured port is therefore a request made to the browser,
// not an address to connect to; if the browser ended up somewhere else, the
// browser is right.
func (self *Browser) resolveClient() (*cdp.Client, error) {
	content, err := os.ReadFile(self.activePortFilename())
	if err != nil {
		return nil, fmt.Errorf("browser: it has not said which port it is listening on yet: %w", err)
	}

	line, _, _ := strings.Cut(string(content), "\n")
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || port <= 0 {
		return nil, fmt.Errorf("browser: %s does not hold a port number", self.activePortFilename())
	}

	return cdp.New("127.0.0.1:" + strconv.Itoa(port)), nil
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

	// The browser runs as an unprivileged account, so it is only allowed to
	// open the graphics and sound devices if it is in the groups that own
	// them — and those group numbers come from the host, not from this
	// image. Without this Chromium renders in software, which on a screen
	// showing video is the difference between smooth and unwatchable, and
	// which reports itself only as a line in a log nobody reads.
	hardwareGroups := devices.Groups(devices.Hardware...)
	if self.configuration.Browser.User != "" {
		log.Noticef("%s", devices.Describe(devices.Hardware))
	}

	return &supervise.Settings{
		Name:        "chromium",
		Path:        binary,
		ExtraGroups: hardwareGroups,
		// Built before every start, not once: DarkMode, Sandbox,
		// IgnoreCertificateErrors and ExtraArguments are all only readable
		// from the command line, and all of them can be changed from the web
		// interface while the daemon is running.
		BuildArguments: self.arguments,
		Restart:        true,
		User:           self.configuration.Browser.User,
		BeforeStart:    self.prepare,
		Ready:          self.probe,
		OnStartFailure: self.explainFailure,
		ReadyTimeout:   60 * time.Second,
		AfterReady:     self.afterReady,
		CaptureOutput:  true,
		OutputLevel:    outputLevel,
		StopTimeout:    10 * time.Second,
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

// explainFailure turns the one failure an operator cannot be expected to
// decode into an instruction.
//
// With its own sandbox enabled, Chromium creates process and network
// namespaces, and a container's default seccomp policy refuses that unless
// the container has CAP_SYS_ADMIN. What Chromium says about it is "Failed to
// move to new namespace: PID namespaces supported, Network namespace
// supported, but failed: errno = Operation not permitted", which names
// neither the container nor the setting and sends people to look at the
// setuid bit on a helper binary that is perfectly correct.
func (self *Browser) explainFailure(process *supervise.Process) {
	for _, line := range process.RecentOutput() {
		if !strings.Contains(line, "Failed to move to new namespace") {
			continue
		}
		log.Errorf("the browser cannot start because its own sandbox needs permissions this container does not have. " +
			"Either give the container CAP_SYS_ADMIN, which is what lets the browser keep its sandbox and is what " +
			"deploy/docker-compose.yml does, or set browser.sandbox to false — which starts the browser but leaves a " +
			"bug in a web page one step closer to everything else in the container.")
		return
	}
}

// probe is the readiness check: the browser has said where it is listening,
// answers there, and has opened its first window.
//
// The second half matters more than it looks. Chromium answers on the port a
// moment before it has a page, and a daemon that asks for the tab list in that
// moment is told there are none — so instead of pointing the window that is
// already on the screen at the first playlist item, it opens another one. In
// kiosk mode the first window is full screen and the second is not, so the
// screen goes on showing the browser's own start page for ever while the
// daemon drives a window nobody can see. Everything reports itself healthy.
//
// This was found on a real device, by taking a picture of the screen from the
// X server and noticing it did not match what the browser said it was showing.
func (self *Browser) probe(ctx context.Context) error {
	client, err := self.resolveClient()
	if err != nil {
		return err
	}

	version, err := client.Version(ctx)
	if err != nil {
		return err
	}

	pages, err := client.Pages(ctx)
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		return fmt.Errorf("browser: %s is up but has not opened a window yet", version.Browser)
	}

	self.mutex.Lock()
	self.client = client
	self.mutex.Unlock()

	log.Debugf("the browser is %s on %s, with %d page(s) open", version.Browser, client.Address(), len(pages))
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

	if err := self.clearZoomLevels(); err != nil {
		log.Warningf("%s", err)
	}

	if err := self.clearProfileLock(); err != nil {
		log.Warningf("%s", err)
	}

	// A port file left by a browser that has gone would otherwise be read as
	// the live one, and the daemon would spend its time talking to a port
	// that answers for somebody else.
	if err := os.Remove(self.activePortFilename()); err != nil && !os.IsNotExist(err) {
		log.Warningf("cannot remove the stale debugging port file: %s", err)
	}

	// Before the profile is handed over, because the database has to be
	// readable by the account the browser runs as.
	if err := self.installCertificates(ctx); err != nil {
		log.Errorf("%s", err)
	}

	if err := self.giveProfileToBrowserUser(); err != nil {
		log.Warningf("%s", err)
	}

	return nil
}

// clearZoomLevels puts the browser back to a hundred per cent.
//
// Chromium remembers a zoom per host, in the profile, for ever. It is one
// keystroke to set — ctrl and minus, or ctrl and a scroll wheel — and on a
// screen on a wall there is nobody standing in front of it to notice or to
// undo it. It survives every restart, and it is not visible anywhere: the
// window is the right size, the screen is the right size, the mode is right,
// and the page is drawn shrunk into a corner with black down two sides.
//
// This is exactly how it was found. The profile on the first device held
// zoom_level -1.5778829311823859 for one host, which is 1.2 to that power, or
// three quarters, and devicePixelRatio reported 0.75 while every flag on the
// command line said 1.
//
// A deliberate zoom belongs in browser.deviceScaleFactor, where it is written
// down and survives a profile being thrown away. An accidental one belongs
// nowhere, so it is removed at every start.
func (self *Browser) clearZoomLevels() error {
	filename := filepath.Join(self.profileDirectory(), "Default", "Preferences")
	content, err := os.ReadFile(filename)
	if err != nil {
		// No profile yet, which is a first run.
		return nil
	}

	var preferences map[string]interface{}
	if err := json.Unmarshal(content, &preferences); err != nil {
		return fmt.Errorf("browser: cannot read %s, so a stored zoom may remain: %w", filename, err)
	}

	partition, _ := preferences["partition"].(map[string]interface{})
	if partition == nil {
		return nil
	}

	var removed []string
	for _, key := range []string{"per_host_zoom_levels", "default_zoom_level"} {
		if _, found := partition[key]; found {
			delete(partition, key)
			removed = append(removed, key)
		}
	}
	if len(removed) == 0 {
		return nil
	}

	updated, err := json.Marshal(preferences)
	if err != nil {
		return fmt.Errorf("browser: cannot rewrite %s: %w", filename, err)
	}
	if err := os.WriteFile(filename, updated, 0o600); err != nil {
		return fmt.Errorf("browser: cannot write %s: %w", filename, err)
	}
	log.Noticef("removed a zoom the browser had remembered (%s); pages are shown at their own size",
		strings.Join(removed, ", "))
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

// clearProfileLock removes the lock Chromium leaves in a profile to stop two
// browsers using it at once.
//
// The lock is a symbolic link naming the host and process that holds it, and
// Chromium refuses to start when it finds one from a different host: "The
// profile appears to be in use by another Chromium process (34) on another
// computer". Inside a container the host name is generated afresh for every
// container, so a profile that survives — which is the whole point of keeping
// it — carries a lock from a machine that, as far as Chromium can tell, is
// somebody else's. The browser then never starts again, and says so only in
// output that is discarded by default.
//
// Removing it is safe here in a way it would not be on a desktop: this daemon
// starts the only browser that uses this profile, and it is about to start it.
func (self *Browser) clearProfileLock() error {
	profile := self.profileDirectory()

	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		path := filepath.Join(profile, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("browser: cannot remove the stale profile lock %s: %w", path, err)
		}
	}
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
