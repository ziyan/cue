package daemon

import (
	"context"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/display"
	"github.com/ziyan/cue/internal/util/drm"
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
	case running.Cursor != updated.Display.Cursor:
		// Both servers are told about the cursor on the command line.
	default:
		return false
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
