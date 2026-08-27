// Package daemon is the composition root: it builds the X server, the
// browser, the VNC server and the watchdog from one configuration, starts them
// in the order they depend on each other, keeps the display arranged as
// monitors come and go, and stops everything in reverse when asked.
//
// Nothing else in this project knows about more than one of those. Everything
// that has to know about all of them is here.
package daemon

import (
	"context"
	"fmt"
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
	"github.com/ziyan/cue/internal/media"
	"github.com/ziyan/cue/internal/network"
	setupnetwork "github.com/ziyan/cue/internal/network/onboarding"
	"github.com/ziyan/cue/internal/onboarding"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/timesync"
	"github.com/ziyan/cue/internal/util/deferutil"
	"github.com/ziyan/cue/internal/util/reaper"
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
	videos     *media.Store
	watchdog   *watchdog.Watchdog

	web *web.Server

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

	// The web interface comes up before anything else, so that a device whose
	// X server will not start is still reachable to say so. That is the case
	// where somebody most needs to see the logs, and the case where a daemon
	// that started the interface last would be silent.
	self.web = web.New(self.store, self)
	if videos, err := media.Open(filepath.Join(configuration.Paths.State, "videos")); err != nil {
		log.Warningf("videos cannot be stored on this device: %s", err)
	} else {
		self.videos = videos
		self.web = self.web.WithVideos(videos)
		self.sweepVideos()
	}
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

	self.startProcesses(ctx, configuration)

	// SIGHUP re-reads the configuration file. This is how somebody who has
	// edited it over SSH applies the change without restarting the container
	// and blanking the screen.
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
		}
	}
}

// startProcesses brings the three supervised programs up in the order they
// need each other: the X server first, because the other two connect to it.
func (self *Daemon) startProcesses(ctx context.Context, configuration *config.Configuration) {
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

		if configuration.VNC.Enabled {
			self.vncProcess = supervise.New(self.vncserver.Settings())
			self.vncProcess.Start(ctx)
		}
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
		}
		return
	}

	if wanted == config.OnboardingAuto && self.hasSomewhereToBe(configuration) {
		return
	}

	interfaceName := network.AccessPointCapableInterface()
	if interfaceName != "" && managedWireless(configuration, interfaceName) {
		// Whatever the mode says. Two programs cannot drive one radio, and
		// this daemon's own network manager is already driving this one: it
		// has been told which network to join and is keeping it there. Running
		// an access point on the same interface makes the two fight, and what
		// that looks like in the log is this daemon reporting that "another
		// program" has the radio -- which is true, and the other program is
		// itself.
		log.Debugf("not offering to set up over the air: %s is already configured "+
			"to join a network", interfaceName)
		return
	}
	if interfaceName == "" {
		// Said once, at debug, because on a device with no wireless hardware
		// this is true for ever and is not news.
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

// hasSomewhereToBe reports whether this device is already on a network, or has
// been told which one to join.
func (self *Daemon) hasSomewhereToBe(configuration *config.Configuration) bool {
	for _, one := range configuration.Network.Interfaces {
		if one.Wireless != nil && one.Wireless.SSID != "" {
			return true
		}
	}

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

// managedWireless reports whether the configuration tells this daemon to keep
// a particular interface on a particular wireless network.
func managedWireless(configuration *config.Configuration, interfaceName string) bool {
	if !configuration.Network.Manage {
		return false
	}
	for _, one := range configuration.Network.Interfaces {
		if one.Name == interfaceName && one.Wireless != nil && one.Wireless.SSID != "" {
			return true
		}
	}
	return false
}

// sweepVideos deletes uploaded videos that no playlist item refers to.
//
// It runs at startup and after every accepted change to the configuration,
// because deleting an item is exactly when its video stops being wanted. A
// device nobody logs into would otherwise accumulate every video ever put on
// it until its disk filled, and the first anybody would know of it is a screen
// that stopped working.
func (self *Daemon) sweepVideos() {
	if self.videos == nil {
		return
	}

	var wanted []string
	for _, item := range self.store.Current().Playlist.Items {
		if item.Media != nil && item.Media.File != "" {
			wanted = append(wanted, item.Media.File)
		}
	}

	removed, err := self.videos.Sweep(wanted)
	if err != nil {
		log.Warningf("cannot tidy up unused videos: %s", err)
		return
	}
	if len(removed) > 0 {
		log.Noticef("removed %d video(s) nothing refers to any more", len(removed))
	}
}
