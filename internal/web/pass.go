package web

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

// passHeader is where a page carries the pass it was given.
const passHeader = "X-Cue-Pass"

// passLifetime is the longest a pass lives if nobody closes the page it
// belongs to. A menu left open on a wall is the case this is for: the person
// who unlocked it has walked away, and the screen should not stay unlocked
// for the rest of the day because a browser never told anybody.
const passLifetime = 15 * time.Minute

// A pass is the authority a page shown on this device's own screen carries,
// and it lasts as long as that page does.
//
// It replaces asking the browser where a request came from. That question was
// answered with the Origin header, which is sound as far as it goes -- a page
// cannot forge it -- but it can only say "a page this daemon served", never
// "the page somebody is standing in front of right now". A pass can, because
// the daemon mints it when it serves that page and forgets it when the page
// says it is closing.
//
// Live and elevated are deliberately separate. The menu holds the playlist as
// soon as it opens, so that the pages stop rotating underneath somebody who is
// reading it, and that must work before anybody has typed a password. Nothing
// that changes the device works until the password has been proved through
// this particular pass.
type pass struct {
	elevated bool
	expires  time.Time
}

// passes is every pass currently outstanding. There are never many: one per
// menu or portal page open on this device, which in practice means zero or
// one.
type passes struct {
	mutex sync.Mutex
	live  map[string]*pass
}

func newPasses() *passes {
	return &passes{live: make(map[string]*pass)}
}

// mint makes a pass and remembers it. The value is random rather than signed:
// the whole point is that it can be forgotten on demand, and a value that has
// been forgotten is refused whatever it says about itself.
func (self *passes) mint() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	value := base64.RawURLEncoding.EncodeToString(raw)

	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.sweep()
	self.live[value] = &pass{expires: time.Now().Add(passLifetime)}
	return value, nil
}

// elevate records that the password has been proved through this pass. It
// reports whether there was a pass to elevate: a page whose pass has already
// expired is told to start again rather than quietly granted anything.
func (self *passes) elevate(value string) bool {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.sweep()

	held, found := self.live[value]
	if !found {
		return false
	}
	held.elevated = true
	return true
}

// check reports what a value is worth: whether it is a pass at all, and
// whether the password has been proved through it.
func (self *passes) check(value string) (live bool, elevated bool) {
	if value == "" {
		return false, false
	}

	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.sweep()

	held, found := self.live[value]
	if !found {
		return false, false
	}
	return true, held.elevated
}

// revoke forgets a pass. This is what closing the menu does, and it is the
// reason a pass is remembered here rather than signed and left to expire on
// its own.
func (self *passes) revoke(value string) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	delete(self.live, value)
	self.sweep()
}

// sweep drops what has expired. It runs on every operation because the map is
// tiny and nothing else would ever run.
//
// The caller holds the mutex.
func (self *passes) sweep() {
	now := time.Now()
	for value, held := range self.live {
		if now.After(held.expires) {
			delete(self.live, value)
		}
	}
}

// passOf reads the pass a request carries.
func passOf(request *http.Request) string {
	return request.Header.Get(passHeader)
}

// hasElevatedPass reports whether this request carries a pass that the
// password has been proved through.
func (self *Server) hasElevatedPass(request *http.Request) bool {
	_, elevated := self.passes.check(passOf(request))
	return elevated
}

// hasLivePass reports whether this request carries a pass at all, elevated or
// not.
func (self *Server) hasLivePass(request *http.Request) bool {
	live, _ := self.passes.check(passOf(request))
	return live
}
