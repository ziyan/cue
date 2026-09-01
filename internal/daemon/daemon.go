// Package daemon is the composition root: it builds the X server, the
// browser, the VNC server and the watchdog from one configuration, starts them
// in the order they depend on each other, keeps the display arranged as
// monitors come and go, and stops everything in reverse when asked.
//
// Nothing else in this project knows about more than one of those. Everything
// that has to know about all of them is here.
package daemon

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/audio"
	"github.com/ziyan/cue/internal/browser"
	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/display"
	"github.com/ziyan/cue/internal/link"
	"github.com/ziyan/cue/internal/media"
	"github.com/ziyan/cue/internal/network"
	setupnetwork "github.com/ziyan/cue/internal/network/onboarding"
	"github.com/ziyan/cue/internal/onboarding"
	"github.com/ziyan/cue/internal/service"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/timesync"
	"github.com/ziyan/cue/internal/upgrade"
	"github.com/ziyan/cue/internal/util/deferutil"
	"github.com/ziyan/cue/internal/util/picture"
	"github.com/ziyan/cue/internal/util/reaper"
	"github.com/ziyan/cue/internal/version"
	"github.com/ziyan/cue/internal/vncserver"
	"github.com/ziyan/cue/internal/watchdog"
	"github.com/ziyan/cue/internal/web"
	"github.com/ziyan/cue/internal/xserver"
)

var log = logging.MustGetLogger("daemon")

// Daemon is everything, running.
type Daemon struct {
	store *config.Store

	xserver    *xserver.Server
	browser    *browser.Browser
	vncserver  *vncserver.Server
	timesync   *timesync.Client
	network    *network.Manager
	onboarding *onboarding.Onboarding
	uploads    *media.Store
	linker     *link.Linker
	reporter   *service.Reporter
	upgrades   *upgrade.Checker

	// When this device last had an address that reached something, and when
	// it last stopped offering setup to try the real network again. Both are
	// about deciding that a network is gone rather than merely slow.
	lastReachable time.Time
	lastRetry     time.Time

	// canReach answers "does this device reach anything". It is a field so
	// that the deciding-a-network-is-gone logic can be tested for what it
	// does with an answer, rather than only on a machine that happens to give
	// the right one. Nil means ask the machine, which is what a daemon does.
	canReach func(*config.Configuration) bool
	watchdog *watchdog.Watchdog

	web *web.Server

	// startedWith is the paths this daemon's programs were started against.
	// They are fixed for the life of the process, and a change to them is
	// reported rather than applied.
	startedWith config.Paths

	xProcess        *supervise.Process
	browserProcess  *supervise.Process
	vncProcess      *supervise.Process
	timesyncProcess *supervise.Process

	mutex sync.Mutex

	// connectorFingerprint is what the machine's display connectors looked
	// like the last time the layout was applied. Comparing it is how a cable
	// being plugged in is noticed without asking the X server anything.
	connectorFingerprint string

	startedAt time.Time
}

// New builds the daemon from a configuration store. Nothing runs until Run.
func New(store *config.Store) (*Daemon, error) {
	configuration := store.Current()

	server, err := xserver.New(store)
	if err != nil {
		return nil, err
	}

	self := &Daemon{
		store:     store,
		xserver:   server,
		startedAt: time.Now(),
	}

	self.browser = browser.New(configuration, server.DisplayName(), server.AuthorityFilename())
	self.vncserver = vncserver.New(store, server.DisplayName(), server.AuthorityFilename())
	self.timesync = timesync.New(store)
	self.network = network.New(store)
	self.onboarding = onboarding.New(store, self.network)
	// While the device is being set up over the air, the screen shows the
	// page carrying the code to scan rather than the playlist.
	self.browser.SetupInProgress = self.onboarding.Running
	self.watchdog = watchdog.New(&configuration.Watchdog, watchdog.Remedies{
		ReloadPage:     self.browser.ReloadCurrent,
		RecreatePage:   self.browser.RecreateCurrent,
		ClearCache:     self.browser.ClearCache,
		RestartBrowser: self.restartBrowser,
		RestartDisplay: self.restartDisplay,
	})
	self.watchdog.AddProbe("X server", self.probeDisplay)
	self.watchdog.AddProbe("browser", self.browser.ProbeResponsive)
	self.watchdog.AddProbe("painting", self.browser.ProbePainting)

	return self, nil
}

// Browser is the running browser, for the web interface.
func (self *Daemon) Browser() *browser.Browser {
	return self.browser
}

// Watchdog is the running watchdog, for the web interface.
func (self *Daemon) Watchdog() *watchdog.Watchdog {
	return self.watchdog
}

// Network is the network manager, for the interface's Network page.
func (self *Daemon) Network() *network.Manager {
	return self.network
}

// TimeSync is the time client, for the interface's clock report.
func (self *Daemon) TimeSync() *timesync.Client {
	return self.timesync
}

// SetupNetwork is the temporary wireless network for setting this device up
// from a phone, and whether it is running.
func (self *Daemon) SetupNetwork() (network.Credentials, bool) {
	if self.onboarding == nil {
		return network.Credentials{}, false
	}
	return self.onboarding.Credentials(), self.onboarding.Running()
}

// SetupNetworks is what the radio saw before it became an access point, which
// is the list the setup portal offers.
func (self *Daemon) SetupNetworks() []network.WirelessNetwork {
	if self.onboarding == nil {
		return nil
	}
	return self.onboarding.Networks()
}

// SetupTrouble is what to tell somebody about the last attempt to join.
func (self *Daemon) SetupTrouble() string {
	if self.onboarding == nil {
		return ""
	}
	return self.onboarding.Trouble()
}

// JoinFromSetup leaves the setup network and joins the one somebody chose on
// the portal.
//
// It returns as soon as the attempt has started. The phone that asked is about
// to lose the network it asked over -- the radio cannot be an access point here
// and a station elsewhere at once -- so an answer that waited for the result
// would never arrive.
func (self *Daemon) JoinFromSetup(ssid, passphrase string) error {
	if self.onboarding == nil || !self.onboarding.Running() {
		return fmt.Errorf("daemon: this device is not being set up")
	}
	go func() {
		if err := self.onboarding.Join(context.Background(), ssid, passphrase); err != nil {
			log.Warningf("could not join %q: %s", ssid, err)
		}
	}()
	return nil
}

// RescanFromSetup looks again for networks in range.
func (self *Daemon) RescanFromSetup() error {
	if self.onboarding == nil {
		return fmt.Errorf("daemon: this device is not being set up")
	}
	return self.onboarding.Rescan(context.Background())
}

// VNCAddress is where the VNC server listens, for the web interface's bridge.
func (self *Daemon) VNCAddress() string {
	return self.vncserver.Address()
}

// StartedAt is when the daemon came up, which is the uptime the interface
// shows.
func (self *Daemon) StartedAt() time.Time {
	return self.startedAt
}

// XServer is the X server, for the display report and its log.
func (self *Daemon) XServer() *xserver.Server {
	return self.xserver
}

// Restart restarts one supervised program by name. It is what the interface's
// buttons do, and it is the same path the watchdog takes, so an operator
// pressing the button and the watchdog giving up produce the same sequence.
func (self *Daemon) Restart(ctx context.Context, name string) error {
	switch name {
	case "chromium", "browser":
		return self.restartBrowser(ctx)
	case "xorg", "xvfb", "display", "x":
		return self.restartDisplay(ctx)
	case "chronyd", "time", "clock":
		if self.timesyncProcess == nil {
			return fmt.Errorf("daemon: time synchronisation is switched off")
		}
		self.timesyncProcess.Restart()
		return nil
	case "x11vnc", "vnc":
		if self.vncProcess == nil {
			return fmt.Errorf("daemon: the VNC server is not running")
		}
		self.vncProcess.Restart()
		readyContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return self.vncProcess.WaitReady(readyContext)
	default:
		return fmt.Errorf("daemon: there is nothing here called %q to restart", name)
	}
}

// Run starts everything and returns when the context is cancelled or a signal
// asks the daemon to stop.
func (self *Daemon) Run(ctx context.Context) error {
	configuration := self.store.Current()

	if err := self.prepareDirectories(configuration); err != nil {
		return err
	}

	// The daemon is process 1 in its container, so orphaned children are its
	// responsibility. Chromium leaves several behind every time a renderer
	// crashes.
	reaper.Start(ctx)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	self.startedWith = self.store.Current().Paths

	// The web interface comes up before anything else, so that a device whose
	// X server will not start is still reachable to say so. That is the case
	// where somebody most needs to see the logs, and the case where a daemon
	// that started the interface last would be silent.
	self.linker = link.New(self.store)
	self.web = web.New(self.store, self)
	// Somebody standing in front of the screen can always open the menu,
	// whatever page is showing. It has to be set after the web server exists,
	// because only the server knows which port the menu answers on.
	self.browser.OnEveryPage = self.web.WayBackScript()
	// So that the tab the control opens is not swept up as a window nobody
	// asked for. Same reason the script is set here: only the server knows
	// which port it answers on.
	self.browser.OwnMenu = self.web.MenuAddress()
	// The wireless configurations used to sit loose in the state directory.
	// wpa_supplicant saves the networks it has joined into them, so leaving
	// them behind is a device that forgets every network it knew.
	network.AdoptOldFiles(configuration)

	// "videos" is what this directory was called before it held pictures too.
	media.Adopt(filepath.Join(configuration.Paths.State, "videos"),
		filepath.Join(configuration.Paths.State, "media"))
	if uploads, err := media.Open(filepath.Join(configuration.Paths.State, "media")); err != nil {
		log.Warningf("uploads cannot be stored on this device: %s", err)
	} else {
		self.uploads = uploads
		self.web = self.web.WithUploads(uploads)
		self.sweepUploads()
	}

	// Whether a newer release exists. Checked in the background from here on,
	// once a day, and whenever somebody opens the page. It reads a public API
	// and changes nothing; replacing this container with a newer one is a
	// separate thing that most devices are not set up to do.
	self.upgrades = upgrade.NewChecker(upgrade.Repository, version.Version())
	self.web = self.web.WithUpgrades(self.upgrades)
	go self.upgrades.Run(ctx)

	// Telling the service what is on the screen, for as long as this device is
	// linked to an account. A device that is not linked has no credential and
	// nothing to say, and this sits idle: there is no separate setting,
	// because being linked is the choice somebody already made.
	// What the service may do to this device once it is linked: an allow-list
	// of the management interface, served over the tunnel and nowhere else.
	self.reporter = service.New(self.store, self.photograph, self.describe).
		WithManagement(self.web.FromService()).
		WithScreen(self.screenForService)
	self.reporter.Start(ctx)
	self.web = self.web.WithReporter(self.reporter)

	// Clear away the container that replaced this one, if that is how this
	// daemon came to be running. The helper is left behind on purpose so that
	// a failed upgrade can be asked why; after a successful one, the daemon it
	// installed is the thing that tidies up.
	go upgrade.SweepHelpers(ctx, upgrade.SocketPath)
	// The setup page has to be reachable on port 80 of the setup network,
	// because that is the only place a phone looks.
	self.onboarding.ServePortalWith(self.web.ServeSetupPort)
	if err := self.web.Start(ctx); err != nil {
		// Not fatal: a screen that shows the right thing with no interface is
		// far better than one that shows nothing because a port was taken.
		log.Errorf("%s", err)
	}
	defer func() {
		stopContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		self.web.Stop(stopContext)
	}()

	// The clock comes up first and independently of the screen. A browser
	// cannot validate a certificate with a wrong clock, so on a device whose
	// battery has died this is what has to happen before anything else will
	// work — and it has nothing to wait for.
	if configuration.Time.Enabled {
		self.timesyncProcess = supervise.New(self.timesync.Settings())
		self.timesyncProcess.Start(ctx)
	}

	if devices, err := audio.Devices(); err == nil {
		log.Noticef("%s", audio.Describe(&configuration.Audio, devices))
	}

	go func() {
		defer deferutil.Recover()
	}()

	self.startProcesses(ctx)

	// SIGHUP re-reads the configuration file. The file is also watched, so an
	// edit is picked up without this; the signal is kept for anybody who has
	// it in their fingers, and for the case where watching could not be set
	// up.
	hangups := make(chan os.Signal, 1)
	signal.Notify(hangups, syscall.SIGHUP)
	defer signal.Stop(hangups)

	changes := self.store.Watch()

	go func() {
		defer deferutil.Recover()
		self.arrangeDisplay(ctx)
	}()

	go func() {
		defer deferutil.Recover()
		self.keepSomethingFocused(ctx)
	}()

	go func() {
		defer deferutil.Recover()
		self.network.Run(ctx)
	}()

	go func() {
		defer deferutil.Recover()
		self.considerOnboarding(ctx)
	}()

	go func() {
		defer deferutil.Recover()
		self.watchConfiguration(ctx)
	}()

	self.watchdog.Start(ctx)

	// Shows the mouse pointer while somebody is moving it and hides it again
	// when they stop; see watchPointer.
	go self.watchPointer(ctx)

	for {
		select {
		case <-ctx.Done():
			self.stopProcesses()
			return nil
		case <-hangups:
			log.Noticef("reloading the configuration")
			if err := self.store.Reload(); err != nil {
				log.Errorf("%s", err)
			}
		case updated := <-changes:
			self.apply(ctx, updated)
			// A playlist item being deleted is exactly when its video stops
			// being wanted, and a configuration change is where that happens.
			self.sweepUploads()
		}
	}
}

// startProcesses brings the three supervised programs up in the order they
// need each other: the X server first, because the other two connect to it.
func (self *Daemon) startProcesses(ctx context.Context) {
	self.xProcess = supervise.New(self.xserver.Settings())
	self.xProcess.Start(ctx)

	go func() {
		defer deferutil.Recover()

		readyContext, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		if err := self.xProcess.WaitReady(readyContext); err != nil {
			log.Errorf("the X server did not start: %s", err)
			if tail := self.xserver.LogTail(20); tail != "" {
				log.Errorf("the end of the X server's own log says:\n%s", tail)
			}
			return
		}

		// The browser's window is sized to the screen, so the layout has to
		// be arranged before it starts.
		self.applyLayout(ctx)

		self.browserProcess = supervise.New(self.browser.Settings())
		self.browserProcess.Start(ctx)

		// The configuration as it is now, not as it was when this goroutine
		// was launched. The X server can take the better part of a minute to
		// come up, and screen sharing turned on inside that window would
		// otherwise be missed by both ends: this snapshot still says off, and
		// applyVNC gave up early because there was no X server yet.
		self.applyVNC(ctx, self.store.Current())
	}()
}

// stopProcesses stops everything in the reverse of the order it was started,
// so that the X server is the last thing to go: the browser needs it to shut
// down cleanly, and a browser killed with its display gone leaves a profile
// that shows the crash bar on the next start.
func (self *Daemon) stopProcesses() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, process := range []*supervise.Process{self.timesyncProcess, self.vncProcess, self.browserProcess, self.xProcess} {
		if process != nil {
			process.Stop(ctx)
		}
	}
}

// Statuses reports what every supervised program is doing, for the interface.
func (self *Daemon) Statuses() []supervise.Status {
	statuses := make([]supervise.Status, 0, 6)
	for _, process := range []*supervise.Process{self.xProcess, self.browserProcess, self.vncProcess, self.timesyncProcess} {
		if process != nil {
			statuses = append(statuses, process.Status())
		}
	}
	// The wireless programs come and go with the interfaces they belong to,
	// so they are asked for rather than held here.
	if self.network != nil {
		statuses = append(statuses, self.network.Statuses()...)
	}
	return statuses
}

func (self *Daemon) prepareDirectories(configuration *config.Configuration) error {
	for _, directory := range []string{configuration.Paths.State, configuration.Paths.Runtime} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("daemon: create %s: %w", directory, err)
		}
	}
	return nil
}

// keepSomethingFocused makes sure a window has the keyboard.
//
// It runs for as long as the daemon does, because the browser opens its window
// after the display has been arranged and opens new ones whenever a page asks
// it to, and each time there is nobody else to hand the keyboard over. It is
// two questions and usually no answer, so it costs nothing.
func (self *Daemon) keepSomethingFocused(ctx context.Context) {
	const interval = 5 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		connection, err := display.Open(ctx, self.store.Current().Display.Number, self.xserver.Cookie())
		if err != nil {
			continue
		}

		focused, err := connection.FocusedWindow()
		// PointerRoot and None both mean nothing has it.
		if err == nil && focused > 1 {
			connection.Close()
			continue
		}
		if err := connection.FocusTopWindow(); err != nil {
			log.Debugf("%s", err)
		}
		connection.Close()
	}
}

// probeDisplay is the watchdog's first question: does the X server answer at
// all? A failure here means restarting the browser would be pointless.
func (self *Daemon) probeDisplay(ctx context.Context) error {
	connection, err := display.Open(ctx, self.store.Current().Display.Number, self.xserver.Cookie())
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Ping()
}

// restartBrowser is the watchdog's heavy remedy. It suspends the watchdog for
// the duration, so that a deliberate restart is never counted as a fault and
// escalated into restarting the X server as well.
func (self *Daemon) restartBrowser(ctx context.Context) error {
	if self.browserProcess == nil {
		return fmt.Errorf("daemon: the browser is not running")
	}
	resume := self.watchdog.Suspend()
	defer resume()

	self.browserProcess.Restart()

	readyContext, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return self.browserProcess.WaitReady(readyContext)
}

// restartDisplay is the heaviest remedy: the X server goes too, and the
// browser with it, because a browser whose display has been restarted has
// nothing to draw into.
func (self *Daemon) restartDisplay(ctx context.Context) error {
	if self.xProcess == nil {
		return fmt.Errorf("daemon: the X server is not running")
	}
	resume := self.watchdog.Suspend()
	defer resume()

	if self.browserProcess != nil {
		stopContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		self.browserProcess.Stop(stopContext)
		cancel()
	}

	self.xProcess.Restart()
	readyContext, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := self.xProcess.WaitReady(readyContext); err != nil {
		return err
	}

	self.applyLayout(ctx)

	if self.browserProcess != nil {
		self.browserProcess = supervise.New(self.browser.Settings())
		self.browserProcess.Start(ctx)
	}
	return nil
}

// considerOnboarding decides whether this device should offer to be set up
// over the air, and does it if so.
//
// The decision is deliberately conservative, because the failure it guards
// against is severe: a screen on a wall that loses its network for a minute
// must not respond by tearing down its connection and broadcasting a setup
// network to the street. All of these have to be true.
//
//   - It has not been switched off in the configuration.
//   - No network is configured for any interface. A device that has been told
//     what to join is a device somebody has already set up.
//   - Nothing has a usable address. This is the one that keeps a working
//     device out of setup mode.
//   - A wireless radio exists that can run a network of its own.
//
// The check runs once at startup and then every reconcile interval, so a
// device that is unplugged from ethernet and left alone with wireless hardware
// eventually offers itself for setup -- and one that gets its network back
// stops offering.
func (self *Daemon) considerOnboarding(ctx context.Context) {
	configuration := self.store.Current()
	interval := configuration.Network.ReconcileInterval.Duration()
	if interval <= 0 {
		interval = time.Minute
	}

	for {
		self.reconsiderOnboarding(ctx)

		select {
		case <-ctx.Done():
			self.onboarding.Stop(context.Background())
			return
		case <-time.After(interval):
		}
	}
}

func (self *Daemon) reconsiderOnboarding(ctx context.Context) {
	configuration := self.store.Current()
	wanted := configuration.Network.Onboarding

	if wanted == config.OnboardingOff {
		if self.onboarding.Running() {
			log.Noticef("setting up over the air has been switched off; taking the setup network down")
			self.onboarding.Finish(ctx)
			self.browser.Refresh(ctx)
		}
		return
	}

	if self.onboarding.Running() {
		// Once it is up it stays up until somebody finishes setting the device
		// up, which is what takes it down. Stopping it because an address
		// appeared would take the network away from the phone that is at that
		// moment being told the setup worked.
		if wanted == config.OnboardingAuto && self.hasSomewhereToBe(configuration) {
			log.Noticef("this device has a network now; taking the setup network down")
			self.onboarding.Finish(ctx)
			self.browser.Refresh(ctx)
			return
		}
		self.retryTheRealNetwork(ctx, configuration)
		return
	}

	if wanted == config.OnboardingAuto && !self.networkLooksLost(configuration) {
		return
	}

	interfaceName := network.AccessPointCapableInterface()
	if interfaceName == "" {
		log.Debugf("no wireless hardware here can run a network of its own, so this " +
			"device cannot be set up over the air")
		return
	}
	if err := self.onboarding.Start(ctx, interfaceName); err != nil {
		log.Warningf("cannot offer to be set up over the air: %s", err)
		return
	}
	// The screen has to change to the page carrying the code, and nothing
	// else will notice that it should.
	self.browser.Refresh(ctx)
}

// hasSomewhereToBe reports whether this device can reach anything.
//
// It used to answer "yes" when an interface merely had a network written down
// for it, which is a different question and the wrong one. A device whose
// wireless passphrase had been changed underneath it was configured, could
// join nothing, had no address, and was therefore -- by that reading --
// settled: it never offered its setup code again and nobody could reach it to
// say so. Being told about a network is not the same as being on one.
func (self *Daemon) hasSomewhereToBe(configuration *config.Configuration) bool {
	interfaces, err := network.Interfaces()
	if err != nil {
		// Unable to tell, so assume it is fine. Guessing the other way turns
		// an unreadable interface list into a device broadcasting a setup
		// network, which is much the worse mistake.
		return true
	}
	for _, one := range interfaces {
		if !one.Physical {
			continue
		}
		// The setup network's own address does not count. While setup is
		// running it sits on the very interface being asked about, and
		// counting it would mean the device deciding it already had a network
		// the moment it started offering to be given one -- and switching
		// setup off again a second later.
		if network.HasUsableAddressOtherThan(one.Name, setupnetwork.DeviceAddress) {
			return true
		}
	}
	return false
}

// sweepUploads deletes uploaded pictures and videos that no playlist item
// refers to.
//
// It runs at startup and after every accepted change to the configuration,
// because deleting an item is exactly when its video stops being wanted. A
// device nobody logs into would otherwise accumulate every video ever put on
// it until its disk filled, and the first anybody would know of it is a screen
// that stopped working.
func (self *Daemon) sweepUploads() {
	if self.uploads == nil {
		return
	}

	var wanted []string
	for _, item := range self.store.Current().Playlist.Items {
		if item.Media != nil && item.Media.File != "" {
			wanted = append(wanted, item.Media.File)
		}
	}

	removed, err := self.uploads.Sweep(wanted)
	if err != nil {
		log.Warningf("cannot tidy up unused uploads: %s", err)
		return
	}
	if len(removed) > 0 {
		log.Noticef("removed %d upload(s) nothing refers to any more", len(removed))
	}
}

// networkLooksLost reports whether this device has been unable to reach
// anything for long enough to give up and offer itself for setup again.
//
// "Long enough" matters in both directions. Too short and a router being
// rebooted takes a wall display off its dashboards; too long and a device
// whose network has really gone is unreachable for that whole time. It can be
// on the short side because falling back is not final: while the setup network
// is up the device keeps trying the one it was told about.
func (self *Daemon) networkLooksLost(configuration *config.Configuration) bool {
	if self.reachable(configuration) {
		self.mutex.Lock()
		self.lastReachable = time.Now()
		self.mutex.Unlock()
		return false
	}

	self.mutex.Lock()
	since := self.lastReachable
	if since.IsZero() {
		// Nothing has ever been reachable, and the clock starts now rather
		// than at the epoch -- otherwise a device that boots with no network
		// decides it is lost before its interfaces have finished coming up.
		self.lastReachable = time.Now()
		since = self.lastReachable
	}
	self.mutex.Unlock()

	lost := configuration.Network.LostAfter.Duration()
	if lost <= 0 {
		lost = 10 * time.Minute
	}
	if time.Since(since) < lost {
		return false
	}

	log.Noticef("nothing has been reachable for %s, so this device is offering "+
		"itself for setup again", time.Since(since).Round(time.Second))
	return true
}

// retryTheRealNetwork puts the setup network down for a minute now and then to
// see whether the network this device was told about has come back.
//
// Without this a device that fell back because a router was rebooting would
// show a setup code until somebody visited it, which is most of the cost this
// feature is meant to save. The configured network is never forgotten by
// falling back, precisely so that there is something to retry.
func (self *Daemon) retryTheRealNetwork(ctx context.Context, configuration *config.Configuration) {
	self.mutex.Lock()
	last := self.lastRetry
	if time.Since(last) < retryTheNetworkEvery {
		self.mutex.Unlock()
		return
	}
	self.lastRetry = time.Now()
	self.mutex.Unlock()

	if !anyConfiguredNetwork(configuration) {
		// Nothing was ever configured, so there is nothing to go back to and
		// this device is simply waiting to be set up.
		return
	}

	log.Noticef("putting the setup network down for a moment to see whether the " +
		"network this device was told about has come back")
	self.onboarding.Stop(ctx)

	self.network.ReconcileNow(ctx)

	deadline := time.Now().Add(retryTheNetworkFor)
	for time.Now().Before(deadline) {
		if self.hasSomewhereToBe(self.store.Current()) {
			log.Noticef("it has; going back to normal")
			self.onboarding.Finish(ctx)
			self.browser.Refresh(ctx)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}

	log.Noticef("it has not; the setup code is coming back")
	interfaceName := network.AccessPointCapableInterface()
	if interfaceName == "" {
		return
	}
	if err := self.onboarding.Start(ctx, interfaceName); err != nil {
		log.Warningf("cannot bring the setup network back: %s", err)
		return
	}
	self.browser.Refresh(ctx)
}

// How often to stop offering setup and try the real network again, and how
// long to give it.
const (
	retryTheNetworkEvery = 5 * time.Minute
	retryTheNetworkFor   = 60 * time.Second
)

// anyConfiguredNetwork reports whether this device has been told about a
// wireless network at all.
func anyConfiguredNetwork(configuration *config.Configuration) bool {
	for _, one := range configuration.Network.Interfaces {
		if one.Wireless != nil && one.Wireless.SSID != "" {
			return true
		}
	}
	return false
}

// reachable is hasSomewhereToBe, or whatever a test has put in its place.
func (self *Daemon) reachable(configuration *config.Configuration) bool {
	if self.canReach != nil {
		return self.canReach(configuration)
	}
	return self.hasSomewhereToBe(configuration)
}

// ForgetWireless takes this device off the network it was told to join and
// offers itself for setup again.
//
// This is what the control on the screen does. Unlike falling back, which
// keeps the network in the configuration so that it can be retried, this
// forgets it: somebody standing in front of the screen has said that network
// is not the one, and retrying it would put the device straight back where
// they did not want it.
//
// The playlist, the password, the timezone and everything uploaded are left
// alone. Losing a screen's content because its wireless changed would be a
// poor trade.
func (self *Daemon) ForgetWireless() error {
	if self.onboarding == nil {
		return fmt.Errorf("daemon: this device cannot be set up over the air")
	}

	interfaceName := network.AccessPointCapableInterface()
	if interfaceName == "" {
		return fmt.Errorf("daemon: no wireless hardware here can run a network of its own")
	}

	err := self.store.Update(func(configuration *config.Configuration) error {
		kept := configuration.Network.Interfaces[:0]
		for _, one := range configuration.Network.Interfaces {
			if one.Wireless == nil || one.Wireless.SSID == "" {
				kept = append(kept, one)
			}
		}
		configuration.Network.Interfaces = kept
		return nil
	})
	if err != nil {
		return fmt.Errorf("daemon: cannot forget the wireless network: %w", err)
	}
	log.Noticef("somebody at the screen asked to set the wireless up again")

	// Started in the background: the request came from the page on the screen,
	// and starting the setup network navigates that page away.
	go func() {
		defer deferutil.Recover()

		ctx := context.Background()
		self.network.ReconcileNow(ctx)

		self.mutex.Lock()
		self.lastReachable = time.Time{}
		self.mutex.Unlock()

		if err := self.onboarding.Start(ctx, interfaceName); err != nil {
			log.Errorf("cannot offer to be set up again: %s", err)
			return
		}
		self.browser.Refresh(ctx)
	}()
	return nil
}

// Linker attaches this device to an account on the hosted service.
func (self *Daemon) Linker() *link.Linker {
	return self.linker
}

// Reporter tells the service what this screen is showing.
func (self *Daemon) Reporter() *service.Reporter {
	return self.reporter
}

// reportedScreenshotWidth is what the service is sent. Smaller than the
// screen: this goes out every half minute over whatever network the device
// has, and the picture is looked at in a list beside other devices rather
// than read.
const reportedScreenshotWidth = 960

// screenForService opens a connection to this device's VNC server for the
// service to be spliced to.
//
// What travels over it is RFB, the same bytes a viewer on the local network
// would exchange, so the service pipes it to a browser and noVNC never learns
// there was a tunnel. The device does no bridging: it has one for its own
// interface and this deliberately does not use it, because reaching a
// websocket endpoint through a tunnel would put an HTTP upgrade in the middle
// of a stream that is already a tunnel.
//
// Offered because the device is linked. Somebody chose to attach this screen
// to an account, and being managed from that account is what they chose.
func (self *Daemon) screenForService(ctx context.Context) (net.Conn, error) {
	configuration := self.store.Current()
	if !configuration.VNC.Enabled {
		return nil, fmt.Errorf("this device is not running a VNC server, so there is no screen to watch")
	}

	// The same address the local viewer uses, and the same correction: a
	// server listening on every interface is reached here on the loopback one.
	address := self.VNCAddress()
	if host, port, err := net.SplitHostPort(address); err == nil &&
		(host == "" || host == "0.0.0.0" || host == "::") {
		address = net.JoinHostPort("127.0.0.1", port)
	}

	dialer := net.Dialer{Timeout: 10 * time.Second}
	viewer, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("this device's screen cannot be reached: %w", err)
	}
	return viewer, nil
}

// describe says what this screen is showing, for the account that owns it.
//
// Deliberately small. The service stores this without reading it, so it could
// carry everything the status page knows -- and then every field this device
// ever reports would be something somebody could come to depend on. What is
// here is what an owner looking at a list of screens wants: which one this is,
// what it is showing, whether it is well, and what it is running.
func (self *Daemon) describe(ctx context.Context) (any, error) {
	configuration := self.store.Current()

	// The browser is asked, and asking involves talking to it, so it gets a
	// deadline of its own: a wedged browser must not stop a device reporting
	// that it is wedged.
	browserContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	showing := self.browser.State(browserContext)

	description := map[string]any{
		"version":  version.Version(),
		"name":     configuration.Device.Name,
		"location": configuration.Device.Location,
		"uptime":   time.Since(self.startedAt).Round(time.Second).String(),
		"showing": map[string]any{
			"item":  showing.Current,
			"title": showing.CurrentTitle,
			"url":   showing.CurrentURL,
			"since": showing.CurrentSince,
			"ready": showing.Ready,
		},
	}
	// The screen's shape, when the X server will say. Opening a connection for
	// it is cheap next to the photograph that goes with this report.
	if connection, err := display.Open(ctx, configuration.Display.Number, self.xserver.Cookie()); err == nil {
		screen := connection.Screen()
		connection.Close()
		description["screen"] = map[string]any{"width": screen.Width, "height": screen.Height}
	}
	return description, nil
}

// photograph takes the picture the reporter sends.
//
// A JPEG rather than the lossless PNG the interface can ask for. Most of what
// is on these screens is video from a camera, which PNG stores appallingly: on
// the first real device this ran on a lossless 4K frame was 5.6 megabytes,
// which is over what the service accepts as well as being wasteful of a
// connection somebody else is paying for.
func (self *Daemon) photograph(ctx context.Context) ([]byte, string, error) {
	configuration := self.store.Current()
	connection, err := display.Open(ctx, configuration.Display.Number, self.xserver.Cookie())
	if err != nil {
		return nil, "", fmt.Errorf("the X server cannot be reached: %w", err)
	}
	defer connection.Close()

	screen, err := connection.Capture(ctx)
	if err != nil {
		return nil, "", err
	}

	var body bytes.Buffer
	if err := jpeg.Encode(&body, picture.Shrink(screen, reportedScreenshotWidth),
		&jpeg.Options{Quality: 70}); err != nil {
		return nil, "", err
	}
	return body.Bytes(), "image/jpeg", nil
}
