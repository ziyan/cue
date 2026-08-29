package web

import (
	"net/http"

	"github.com/ziyan/cue/internal/util/qr"
)

// Linking this device to an account on the hosted service.
//
// Two ways in, and they are the same three calls. Somebody at the screen opens
// the menu and proves the device password, which elevates their pass; somebody
// at a desk signs in to the interface, which gives them a session. Both then
// start an attempt, watch it, and either see it complete or abandon it.
//
// Both are gated, and on the same thing in spirit: proof of already having the
// device. A code on a wall is enough to *see*, and on a screen in a public
// room that is a low bar, so it is not enough to give a device away.

// linkState is what both interfaces poll while a code is on the screen.
func (self *Server) linkState(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, self.device.Linker().State())
}

// linkStart mints a ticket and begins asking the service about it.
func (self *Server) linkStart(response http.ResponseWriter, request *http.Request) {
	state, err := self.device.Linker().Start(request.Context())
	if err != nil {
		// Almost always "no service is configured", which is a thing to fix in
		// the configuration rather than a fault, so it is a 409 and not a 500.
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, state)
}

// linkAbandon forgets an attempt in progress, which is what closing the page
// or pressing cancel does. It leaves an existing link alone.
func (self *Server) linkAbandon(response http.ResponseWriter, request *http.Request) {
	self.device.Linker().Abandon()
	writeJSON(response, http.StatusOK, self.device.Linker().State())
}

// linkForget drops the credential, so the device answers to nobody again.
//
// Deliberately not offered at the screen. Unlinking is not urgent, it is not
// something somebody standing in a room needs to do before they can leave, and
// the consequence of a stranger doing it is a device that quietly stops
// reporting. It is a decision for the interface, where there is a session.
func (self *Server) linkForget(response http.ResponseWriter, request *http.Request) {
	if err := self.device.Linker().Unlink(); err != nil {
		writeError(response, http.StatusInternalServerError, "cannot forget the link")
		return
	}
	writeJSON(response, http.StatusOK, self.device.Linker().State())
}

// screenLinkStart is the same as linkStart, for a page on this device's own
// screen, and it insists the pass has been elevated first.
func (self *Server) screenLinkStart(response http.ResponseWriter, request *http.Request) {
	if !self.elevatedPass(response, request) {
		return
	}
	self.linkStart(response, request)
}

// screenLinkState lets the menu poll while it waits. It needs only a live
// pass, not an elevated one: by the time there is anything to watch, the
// password has already been proved to start the attempt.
func (self *Server) screenLinkState(response http.ResponseWriter, request *http.Request) {
	if live, _ := self.passes.check(passOf(request)); !live {
		writeError(response, http.StatusForbidden, "this page is not open on the screen")
		return
	}
	self.linkState(response, request)
}

// screenLinkAbandon is what the menu calls when somebody backs out.
func (self *Server) screenLinkAbandon(response http.ResponseWriter, request *http.Request) {
	if !self.elevatedPass(response, request) {
		return
	}
	self.linkAbandon(response, request)
}

// elevatedPass answers the request itself when the pass is missing or has not
// had the device password proved through it.
func (self *Server) elevatedPass(response http.ResponseWriter, request *http.Request) bool {
	live, elevated := self.passes.check(passOf(request))
	if !live {
		writeError(response, http.StatusForbidden, "this page is not open on the screen")
		return false
	}
	if !elevated && self.isSetUp() {
		writeError(response, http.StatusForbidden, "the device password has not been given")
		return false
	}
	return true
}

// linkCode draws the attempt in progress as a QR code.
//
// Drawn here rather than in either page because the encoder is Go's and the
// pages are one Go template and one browser application. Serving the picture
// is the one thing both can use without either of them growing a second
// implementation.
func (self *Server) linkCode(response http.ResponseWriter, request *http.Request) {
	state := self.device.Linker().State()
	if !state.Pending || state.URL == "" {
		writeError(response, http.StatusNotFound, "no code is being shown")
		return
	}
	matrix, err := qr.Encode(state.URL)
	if err != nil {
		log.Debugf("cannot encode the linking code: %s", err)
		writeError(response, http.StatusInternalServerError, "cannot draw the code")
		return
	}

	response.Header().Set("Content-Type", "image/svg+xml")
	// The code changes whenever an attempt does, and a cached one is a code
	// that no longer works.
	response.Header().Set("Cache-Control", "no-store")
	if _, err := response.Write([]byte(renderQR(matrix, "Code to scan to link this device"))); err != nil {
		log.Debugf("cannot write the linking code: %s", err)
	}
}

// screenLinkCode is the same picture for a page on this device's own screen.
func (self *Server) screenLinkCode(response http.ResponseWriter, request *http.Request) {
	if live, _ := self.passes.check(passOf(request)); !live {
		writeError(response, http.StatusForbidden, "this page is not open on the screen")
		return
	}
	self.linkCode(response, request)
}
