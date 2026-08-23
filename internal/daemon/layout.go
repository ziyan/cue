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
		self.applyLayout()
	}
}

// applyLayout connects to the X server, arranges the outputs, and tells the
// browser how large its window should be.
func (self *Daemon) applyLayout() {
	configuration := self.store.Current()

	connection, err := display.Open(configuration.Display.Number, self.xserver.Cookie())
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

	self.applyLayout()

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
