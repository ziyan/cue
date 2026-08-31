package daemon

import (
	"context"
	"reflect"
	"strings"
	"time"

	"github.com/ziyan/cue/internal/browser"
	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/display"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/util/drm"
	"github.com/ziyan/cue/internal/util/loglevel"
	"github.com/ziyan/cue/internal/util/timezone"
	"github.com/ziyan/cue/internal/wallpaper"
)

// arrangeDisplay keeps the screen arranged as monitors come and go.
//
// The kernel's view of the connectors is the trigger rather than the X
// server's, for two reasons: it can be read without connecting to anything, so
// it costs nothing to check often; and it is right even when the X server has
// not noticed yet, which it frequently has not until something asks it.
//
// Polling rather than listening for kernel events is deliberate. Kernel uevent
// sockets are scoped to a network namespace, so a container on a bridge
// network receives none at all — which would mean this worked on the
// developer's machine and silently did nothing on a device.
func (self *Daemon) arrangeDisplay(ctx context.Context) {
	interval := self.store.Current().Display.ReconcileInterval.Duration()
	if interval <= 0 {
		interval = 5 * time.Second
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		connectors, err := drm.Connectors()
		if err != nil {
			// A machine with no /sys/class/drm at all is one running against
			// a virtual screen, where there is nothing to arrange.
			continue
		}

		fingerprint := drm.Fingerprint(connectors)

		self.mutex.Lock()
		changed := fingerprint != self.connectorFingerprint
		previous := self.connectorFingerprint
		self.connectorFingerprint = fingerprint
		self.mutex.Unlock()

		if !changed {
			continue
		}
		if previous != "" {
			log.Noticef("the screens attached to this machine have changed: %s", fingerprint)
		}
		self.applyLayout(ctx)
	}
}

// applyLayout connects to the X server, arranges the outputs, and tells the
// browser how large its window should be.
func (self *Daemon) applyLayout(ctx context.Context) {
	configuration := self.store.Current()

	connection, err := display.Open(ctx, configuration.Display.Number, self.xserver.Cookie())
	if err != nil {
		log.Warningf("cannot arrange the display: %s", err)
		return
	}
	defer connection.Close()

	changed, err := connection.Apply(&configuration.Display)
	if err != nil {
		log.Errorf("cannot arrange the display: %s", err)
		return
	}

	screen := connection.Screen()
	self.browser.SetScreenSize(screen.Width, screen.Height)

	// Before the browser has anything on the screen, and after it if it goes
	// away. Cheap, and it is the difference between a screen that is starting
	// and a screen that looks broken.
	if configuration.Display.Wallpaper {
		if err := connection.SetRootBackground(wallpaper.Draw(screen.Width, screen.Height)); err != nil {
			log.Debugf("%s", err)
		}
	}

	// Nothing else will give a window the keyboard: there is no window
	// manager here. A browser that has never been focused paints one frame
	// and then stops, so the screen holds that frame for ever while the
	// browser goes on working perfectly. See display.FocusTopWindow.
	if err := connection.FocusTopWindow(); err != nil {
		log.Debugf("%s", err)
	}

	if changed {
		outputs, err := connection.Outputs()
		if err == nil {
			log.Noticef("the screen is %dx%d: %s", screen.Width, screen.Height, describe(outputs))
		}
	}
}

// apply takes a changed configuration and works out how little has to happen
// for it to take effect. Restarting everything would be simpler and would
// blank the screen for several seconds every time somebody edited a playlist.
func (self *Daemon) apply(ctx context.Context, updated *config.Configuration) {
	log.Noticef("applying the changed configuration")

	// Cheap, and nothing has to be restarted for either of them: the log
	// level is global state, and the watchdog reads its settings as it needs
	// them. Both are done before anything that might restart a program, so
	// that a change that turns the logging up is in force for the restart it
	// was probably turned up to investigate.
	loglevel.Set(updated.Log.Level)
	timezone.Apply(updated.Device.Timezone)
	if self.browserProcess != nil {
		self.browserProcess.SetOutputLevel(browser.OutputLevel(updated))
	}
	self.watchdog.Reconfigure(&updated.Watchdog)
	self.warnAboutWhatNeedsARestart(updated)

	// A few settings are decided when the X server is executed and cannot be
	// changed under it: which server, which display number, the size of a
	// virtual screen. Without this, switching display.server from xorg to
	// xvfb appears to be accepted and then does nothing, which is a
	// particularly unhelpful way to fail.
	if self.displayRestartNeeded(updated) {
		log.Noticef("the change needs the X server restarted")
		if err := self.restartDisplay(ctx); err != nil {
			log.Errorf("cannot restart the X server: %s", err)
		}
		// Still the VNC server's turn: restarting the display does not come
		// back through here, and a change that turned screen sharing on in
		// the same edit would otherwise be dropped on the floor.
		self.applyVNC(ctx, updated)
		self.applyTimesync(ctx, updated)
		return
	}

	self.applyLayout(ctx)

	restartNeeded, err := self.browser.Reconfigure(ctx, updated)
	if err != nil {
		log.Errorf("cannot apply the new playlist: %s", err)
	}
	if restartNeeded && self.browserProcess != nil {
		log.Noticef("the change needs the browser restarted")
		if err := self.restartBrowser(ctx); err != nil {
			log.Errorf("cannot restart the browser: %s", err)
		}
	}

	self.applyVNC(ctx, updated)
	self.applyTimesync(ctx, updated)
}

// displayRestartNeeded reports whether a change is one the running X server
// cannot be told about. It compares against what the server was actually
// started with rather than against the previous configuration, so that a
// restart which failed is retried on the next change rather than skipped
// because "nothing changed since last time".
func (self *Daemon) displayRestartNeeded(updated *config.Configuration) bool {
	return self.displayRestartWouldBeNeeded(self.xserver.StartedWith(), updated)
}

// displayRestartWouldBeNeeded is the decision itself, separated from reading
// the running server's state so that it can be tested: getting it wrong one
// way blanks the screen for no reason, and the other way accepts a setting
// through the interface that then silently does nothing.
func (self *Daemon) displayRestartWouldBeNeeded(running config.Display, updated *config.Configuration) bool {
	switch {
	case running.Server != updated.Display.Server:
	case running.Number != updated.Display.Number:
	case running.Server == config.ServerXvfb && running.Framebuffer != updated.Display.Framebuffer:
		// Xvfb's screen size is fixed when it starts; RandR cannot change it.
	case running.VirtualTerminal != updated.Display.VirtualTerminal:
		// Which console the server draws on is a vtN argument on its command
		// line. Changing it is rare and deliberate -- it is how a device with
		// the wrong console passed through is fixed -- and doing nothing
		// until the next boot is how it looks like it did not work.
	case !sameStrings(running.ExtraArguments, updated.Display.ExtraArguments):
	case strings.TrimSpace(running.XorgConfiguration) != strings.TrimSpace(updated.Display.XorgConfiguration):
		// Written into the configuration directory before the server starts,
		// and read by the server only at start-up. Whether there is any of it
		// also decides whether -configdir is on the command line at all.
	case running.Cursor.ServerDrawsOne() != updated.Display.Cursor.ServerDrawsOne():
		// Whether the server has a cursor at all is on its command line and
		// cannot be changed afterwards. Which is not the same question as
		// whether one is *shown*: moving between "auto" and "always" is the
		// daemon's own business and must not blank the screen to do it.
	default:
		return false
	}
	return true
}

func sameStrings(before, after []string) bool {
	if len(before) != len(after) {
		return false
	}
	for index := range before {
		if before[index] != after[index] {
			return false
		}
	}
	return true
}

func describe(outputs []display.Output) string {
	parts := make([]string, 0, len(outputs))
	for _, output := range outputs {
		if !output.Connected {
			continue
		}
		if !output.Enabled {
			parts = append(parts, output.Name+" (off)")
			continue
		}
		parts = append(parts, output.Name+" "+output.CurrentMode)
	}
	if len(parts) == 0 {
		return "nothing is plugged in"
	}
	return join(parts, ", ")
}

func join(parts []string, separator string) string {
	result := ""
	for index, part := range parts {
		if index > 0 {
			result += separator
		}
		result += part
	}
	return result
}

// applyVNC brings x11vnc into line with the configuration: started if it is
// wanted and not running, stopped if it is running and no longer wanted, and
// restarted if it is running with settings that have since changed.
//
// The daemon does this rather than the VNC server itself because the daemon
// owns every supervised process. Without it, turning the screen sharing on,
// moving it to another address or changing its password were all accepted by
// the interface, written to the file, logged as applied — and then silently
// did nothing until something else happened to restart the daemon.
func (self *Daemon) applyVNC(ctx context.Context, updated *config.Configuration) {
	switch childAction(self.vncProcess != nil, updated.VNC.Enabled, self.vncserver.StartedWith() != updated.VNC) {
	case childStop:
		log.Noticef("screen sharing has been turned off; stopping the VNC server")
		stopContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		self.vncProcess.Stop(stopContext)
		self.vncProcess = nil

	case childStart:
		// Only once the X server is up: x11vnc exports a display, and with
		// nothing to export it exits and is restarted until there is.
		if self.xProcess == nil {
			return
		}
		log.Noticef("screen sharing has been turned on; starting the VNC server")
		self.vncProcess = supervise.New(self.vncserver.Settings())
		self.vncProcess.Start(ctx)

	case childRestart:
		// Compared as a whole struct rather than field by field on purpose. A
		// list of the settings that matter is a list that falls behind the
		// struct the first time somebody adds a field to it, and the way that
		// failure shows up is a setting that the interface accepts and the
		// server never sees. Everything x11vnc is given comes from this
		// struct, either on the command line or in the password file, and
		// both are built afresh before every start.
		log.Noticef("the change needs the VNC server restarted")
		self.vncProcess.Restart()
	}
}

// applyTimesync brings chronyd into line with the configuration, for the same
// reason as applyVNC: the clock is the daemon's process too, and adding a time
// server to the file was accepted and then ignored until the next boot. That
// is worst on exactly the device it matters on — one whose clock battery has
// died, where the wrong servers mean nothing with a certificate will load.
func (self *Daemon) applyTimesync(ctx context.Context, updated *config.Configuration) {
	switch childAction(self.timesyncProcess != nil,
		updated.Time.Enabled,
		!reflect.DeepEqual(self.timesync.StartedWith(), updated.Time)) {
	case childStop:
		log.Noticef("time synchronisation has been turned off; stopping the clock")
		stopContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		self.timesyncProcess.Stop(stopContext)
		self.timesyncProcess = nil

	case childStart:
		log.Noticef("time synchronisation has been turned on; starting the clock")
		self.timesyncProcess = supervise.New(self.timesync.Settings())
		self.timesyncProcess.Start(ctx)

	case childRestart:
		// DeepEqual rather than a comparison field by field, so that a
		// setting added to config.Time later is covered without anybody
		// having to remember this line. chronyd's whole configuration file is
		// written from this struct before every start.
		log.Noticef("the change needs the clock restarted")
		self.timesyncProcess.Restart()
	}
}

// action is what a configuration change asks of a supervised child that is
// started, stopped and restarted by the daemon rather than living for as long
// as the daemon does.
type action int

const (
	childNothing action = iota
	childStart
	childStop
	childRestart
)

// childAction is the decision itself, kept apart from the starting and
// stopping so that it can be tested. Both ways of getting it wrong are quiet:
// too eager and the screen sharing drops every time an unrelated setting is
// saved, too shy and a setting is accepted, written to the file, reported as
// applied, and never reaches the program it was meant for.
func childAction(running, wanted, changed bool) action {
	switch {
	case !wanted && running:
		return childStop
	case !wanted:
		return childNothing
	case !running:
		return childStart
	case changed:
		return childRestart
	default:
		return childNothing
	}
}

// warnAboutWhatNeedsARestart says so when a change has been saved that this
// daemon cannot put into force while it runs.
//
// Both of these could be applied here and deliberately are not. Rebinding the
// web interface means closing the socket the operator is talking to and
// hoping the new address binds; when it does not -- a port already in use, an
// address this host does not have -- the device is left with no interface at
// all and no way to take the change back except physical access. Moving the
// paths out from under a running X server, browser and VNC server has the
// same shape and a worse blast radius.
//
// So they wait for a restart. What matters is that this is said out loud: a
// setting that is saved, shown as saved, and quietly not in force is the
// failure this whole file exists to stop.
func (self *Daemon) warnAboutWhatNeedsARestart(updated *config.Configuration) {
	if address := self.web.StartedWith(); address != updated.Web.Listen {
		log.Warningf("web.listen is now %s, but the interface is still on %s; "+
			"restart cue for that to take effect", updated.Web.Listen, address)
	}
	if self.startedWith.State != updated.Paths.State || self.startedWith.Runtime != updated.Paths.Runtime {
		log.Warningf("the paths section has changed, but every program is still using "+
			"state %s and runtime %s; restart cue for that to take effect",
			self.startedWith.State, self.startedWith.Runtime)
	}
}
