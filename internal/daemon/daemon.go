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

	"github.com/ziyan/cue/internal/browser"
	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/display"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/util/deferutil"
	"github.com/ziyan/cue/internal/util/reaper"
	"github.com/ziyan/cue/internal/vncserver"
	"github.com/ziyan/cue/internal/watchdog"
	"github.com/ziyan/cue/internal/xserver"
)

var log = logging.MustGetLogger("daemon")

// Daemon is everything, running.
type Daemon struct {
	store *config.Store

	xserver   *xserver.Server
	browser   *browser.Browser
	vncserver *vncserver.Server
	watchdog  *watchdog.Watchdog

	xProcess       *supervise.Process
	browserProcess *supervise.Process
	vncProcess     *supervise.Process

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

	server, err := xserver.New(configuration)
	if err != nil {
		return nil, err
	}

	self := &Daemon{
		store:     store,
		xserver:   server,
		startedAt: time.Now(),
	}

	self.browser = browser.New(configuration, server.DisplayName(), server.AuthorityFilename())
	self.vncserver = vncserver.New(configuration, server.DisplayName(), server.AuthorityFilename())
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

// VNCAddress is where the VNC server listens, for the web interface's bridge.
func (self *Daemon) VNCAddress() string {
	return self.vncserver.Address()
}

// StartedAt is when the daemon came up, which is the uptime the interface
// shows.
func (self *Daemon) StartedAt() time.Time {
	return self.startedAt
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

	self.watchdog.Start(ctx)

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
		self.applyLayout()

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

	for _, process := range []*supervise.Process{self.vncProcess, self.browserProcess, self.xProcess} {
		if process != nil {
			process.Stop(ctx)
		}
	}
}

// Statuses reports what every supervised program is doing, for the interface.
func (self *Daemon) Statuses() []supervise.Status {
	statuses := make([]supervise.Status, 0, 3)
	for _, process := range []*supervise.Process{self.xProcess, self.browserProcess, self.vncProcess} {
		if process != nil {
			statuses = append(statuses, process.Status())
		}
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

// probeDisplay is the watchdog's first question: does the X server answer at
// all? A failure here means restarting the browser would be pointless.
func (self *Daemon) probeDisplay(ctx context.Context) error {
	connection, err := display.Open(self.store.Current().Display.Number, self.xserver.Cookie())
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

	self.applyLayout()

	if self.browserProcess != nil {
		self.browserProcess = supervise.New(self.browser.Settings())
		self.browserProcess.Start(ctx)
	}
	return nil
}
