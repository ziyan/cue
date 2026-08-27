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
	"sync"
	"syscall"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/audio"
	"github.com/ziyan/cue/internal/browser"
	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/display"
	"github.com/ziyan/cue/internal/network"
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

	xserver   *xserver.Server
	browser   *browser.Browser
	vncserver *vncserver.Server
	timesync  *timesync.Client
	network   *network.Manager
	watchdog  *watchdog.Watchdog

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
// from a phone.
//
// Nothing runs one yet: bringing the radio up as an access point is the next
// milestone of docs/planning/active/20260826-wireless-onboarding.md, and the
// state machine that decides when to do it is the one after. Until then this
// answers "no network", which is the truth -- the welcome page falls back to
// showing the device's web address, exactly as it did before. It is wired up
// now so that the page has one place to ask and does not have to change again.
func (self *Daemon) SetupNetwork() (network.Credentials, bool) {
	return network.Credentials{}, false
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
