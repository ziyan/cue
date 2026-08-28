package web

import (
	"net/http"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/security"
)

// screenUnlock proves the password through a pass, so that the page holding
// it may change the device for as long as it stays open.
//
// This is not the ordinary sign-in. Signing in sets a cookie in the browser
// it was typed into, and the browser this is typed into is the one bolted to
// the wall: a session there outlives the menu, the person, and the reason.
// So nothing is set on the browser at all. The authority lives in the
// daemon's own memory, attached to this one page, and dies with it.
func (self *Server) screenUnlock(response http.ResponseWriter, request *http.Request) {
	value := passOf(request)
	if live, _ := self.passes.check(value); !live {
		writeError(response, http.StatusUnauthorized, "open the menu again")
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := decode(request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	if !self.isSetUp() {
		writeError(response, http.StatusConflict, "this device has no password yet; choose one")
		return
	}

	// The same cost either way, and the same tenth of a second, for the same
	// reason as the ordinary sign-in: it is the hash that makes guessing
	// impractical, not a rate limiter nobody would maintain.
	if !security.VerifyPassword(self.store.Current().Web.PasswordHash, body.Password) {
		log.Warningf("somebody at the screen gave the wrong password")
		writeError(response, http.StatusUnauthorized, "that is not the password")
		return
	}

	self.passes.elevate(value)
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

// screenChooseWord sets the first password, from the screen or the portal.
//
// A device with no password used to let anybody standing at it do anything,
// on the reasoning that there was nobody to ask. That is right for a device
// still in its box and wrong for one that has been hung on a wall and never
// finished setting up -- which is the state a device stays in for as long as
// nobody visits its web interface, and some never do.
//
// Refusing to serve them would leave somebody with a screen they cannot
// configure and no way to fix it but the interface they could not reach in
// the first place, which is the situation this menu exists to rescue. So it
// asks them to choose a password instead. One step, and the device ends up in
// the state it should have been in already.
func (self *Server) screenChooseWord(response http.ResponseWriter, request *http.Request) {
	value := passOf(request)
	if live, _ := self.passes.check(value); !live {
		writeError(response, http.StatusUnauthorized, "open the menu again")
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := decode(request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	// Whoever gets there first wins, and everybody after them needs the
	// password. Without this check somebody could set a second password over
	// the first through a page left open from before it was set.
	if self.isSetUp() {
		writeError(response, http.StatusConflict, "this device already has a password")
		return
	}

	// The same floor the setup wizard applies. Two rules would be one rule
	// too many, and the lower of the two would be the one that mattered.
	if len(body.Password) < 8 {
		writeError(response, http.StatusBadRequest, "the password must be at least eight characters")
		return
	}

	hash, err := security.HashPassword(body.Password)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	err = self.store.Update(func(configuration *config.Configuration) error {
		configuration.Web.PasswordHash = hash
		return nil
	})
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	log.Noticef("a password was set from the screen")
	self.passes.elevate(value)
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

// screenClose forgets the pass. This is the whole reason a pass is remembered
// rather than signed and left to expire: the authority ends when the person
// closes the menu, which is a moment no expiry written at minting time could
// have predicted.
func (self *Server) screenClose(response http.ResponseWriter, request *http.Request) {
	self.passes.revoke(passOf(request))
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}
