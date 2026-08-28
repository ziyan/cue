package web

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/ziyan/cue/internal/upgrade"
)

// applyUpgrade replaces this device's container with one built from the newest
// release.
//
// It answers before the upgrade finishes, and it has to: what it starts will
// stop this daemon, so a reply written afterwards would never be sent. The
// page says the screen is about to go dark and comes back when the new one
// answers.
//
// Reachable two ways, and both prove the same thing. From the web interface it
// is behind the session; from the screen's own menu it is behind a pass that
// has been through the password gate. Proximity alone does not reach it.
//
// One at a time. Two of these running together is not a slow upgrade, it is a
// dead device: starting a second helper force-removes the first, and the first
// may be between stopping the old container and starting the new one, so what
// is left is a machine with no cue on it and a dark screen on a wall.
func (self *Server) applyUpgrade(response http.ResponseWriter, request *http.Request) {
	image, state, ok := self.readyToUpgrade(response)
	if !ok {
		return
	}

	if !self.upgradeRunning.CompareAndSwap(false, true) {
		writeError(response, http.StatusConflict, "an upgrade is already under way on this device")
		return
	}

	// Say so on the screen before anything happens to it. An upgrade blanks a
	// wall display for the better part of a minute, and whoever is standing in
	// front of it should be told why rather than watching it die.
	self.sayOnScreen(request.Context(), state.Latest)

	// The pull is the slow part and the part most likely to fail, and it
	// happens with everything still running. Doing it here, before answering,
	// means a device that cannot fetch the image says so on the page instead
	// of going dark and coming back unchanged with no explanation.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	go func() {
		defer cancel()

		// The hold on the playlist expires if nobody renews it, which is what
		// stops a lost request freezing a screen for ever -- and an upgrade
		// takes minutes, which is longer than that. So it is renewed here for
		// as long as this goes on. The page on the screen cannot do it: it is
		// deliberately a page that fetches nothing, because the daemon serving
		// it is about to stop.
		// Renewed until this container stops, which is how the upgrade ends.
		// Not stopped when Begin returns: Begin returns as soon as the helper
		// is running, and the helper still has to stop this container. If the
		// renewal stopped there, a slow handover would let the hold lapse and
		// the playlist would rotate the "updating" page off the screen while
		// the upgrade was still going on.
		stopHolding := self.keepHolding(ctx)

		// If the helper starts and then fails, it puts the old container back
		// and this process carries on running -- with the flag above still
		// set, refusing every further attempt until somebody restarts the
		// daemon. So the claim is given up after long enough that a real
		// upgrade would have stopped this process instead.
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(15 * time.Minute):
			}
			if self.upgradeRunning.Swap(false) {
				log.Warningf("the upgrade to %s did not replace this container; "+
					"letting somebody try again", state.Latest)
			}
		}()

		if err := upgrade.Begin(ctx, upgrade.SocketPath, image); err != nil {
			log.Errorf("the upgrade to %s did not start: %s", state.Latest, err)
			// Nothing is going to happen now, so let the screen go back to
			// what it was showing: a display stuck on "updating" for ever is
			// worse than one that never said anything. And let somebody try
			// again.
			self.upgradeRunning.Store(false)
			stopHolding()
			if browser := self.device.Browser(); browser != nil {
				browser.Release()
			}
		}
	}()

	writeJSON(response, http.StatusAccepted, map[string]string{
		"status": "started",
		"image":  image,
	})
}

// menuUpgrade is the same thing from the screen's own menu.
//
// It sits behind screenAction, which means an elevated pass: somebody standing
// at the screen who has typed this device's password. That is a different
// thing from proximity, and it is the reason this is offered here at all --
// the menu asks for the password before it offers anything, so the person
// pressing this has proved the same thing they would have proved by signing
// in to the web interface.
func (self *Server) menuUpgrade(response http.ResponseWriter, request *http.Request) {
	self.applyUpgrade(response, request)
}

// keepHolding renews the hold on the playlist until the returned function is
// called or the context ends.
func (self *Server) keepHolding(ctx context.Context) func() {
	browser := self.device.Browser()
	if browser == nil {
		return func() {}
	}

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				browser.KeepHolding()
			}
		}
	}()
	return func() { close(done) }
}

// readyToUpgrade answers the four questions both callers have to ask, and
// writes the refusal itself when the answer is no.
func (self *Server) readyToUpgrade(response http.ResponseWriter) (string, upgrade.State, bool) {
	if self.upgrades == nil {
		writeError(response, http.StatusServiceUnavailable, "this daemon is not checking for releases")
		return "", upgrade.State{}, false
	}

	canApply, whyNot := upgrade.CanApply(self.store.Current().Upgrade.AllowApply)
	if !canApply {
		writeError(response, http.StatusForbidden, whyNot)
		return "", upgrade.State{}, false
	}

	state := self.upgrades.State()
	if state.Latest == "" {
		writeError(response, http.StatusConflict, "nothing is known about newer releases yet")
		return "", upgrade.State{}, false
	}
	if !state.Newer {
		writeError(response, http.StatusConflict, "this device is already running the newest release")
		return "", upgrade.State{}, false
	}
	return upgrade.ImageFor(state.Latest), state, true
}

// sayOnScreen puts a page on the display explaining that it is about to go
// away. Best effort: a device whose browser is not running is a device with
// nothing to tell anybody, and that must not stop the upgrade.
func (self *Server) sayOnScreen(ctx context.Context, version string) {
	browser := self.device.Browser()
	if browser == nil {
		return
	}

	// Held, so the playlist does not rotate the message away after a few
	// seconds. Released by the failure path above; on success this container
	// stops and the question does not arise.
	browser.Hold()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, port, err := net.SplitHostPort(self.Address())
	if err != nil {
		log.Debugf("cannot work out where to send the screen: %s", err)
		return
	}
	address := "http://127.0.0.1:" + port + "/upgrading?version=" + url.QueryEscape(version)
	if err := browser.Navigate(ctx, address); err != nil {
		log.Debugf("cannot tell the screen it is being upgraded: %s", err)
	}
	// Long enough for the page to be on the glass before the container stops.
	time.Sleep(time.Second)
}
