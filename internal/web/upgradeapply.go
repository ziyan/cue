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
// Behind the session, like the rest of the management interface, and
// deliberately not reachable from the on-screen menu. Standing in front of a
// screen authorises changing what it shows and how it reaches the network. It
// does not authorise replacing the software on the machine.
func (self *Server) applyUpgrade(response http.ResponseWriter, request *http.Request) {
	image, state, ok := self.readyToUpgrade(response)
	if !ok {
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
		if err := upgrade.Begin(ctx, upgrade.SocketPath, image); err != nil {
			log.Errorf("the upgrade to %s did not start: %s", state.Latest, err)
			// Put the screen back to what it was showing: nothing is going to
			// happen, and a display stuck on "upgrading" for ever is worse
			// than one that never said anything.
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
