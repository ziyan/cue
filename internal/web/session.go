package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ziyan/cue/internal/util/security"
)

// sessionCookie is the name of the cookie a signed-in browser carries.
const sessionCookie = "cue_session"

// A session is a signed statement that somebody knew the password at a
// certain time. There is no session store: a device that reboots would lose
// one, and there is exactly one account, so there is nothing a store would
// record that the cookie cannot carry.
//
// The value is "<issued at>.<signature>", where the signature is an HMAC over
// the issue time with the secret generated on the first run. Changing the
// password does not invalidate existing sessions; changing the secret does,
// which is what "sign out everywhere" would do if it existed.
func (self *Server) issueSession(response http.ResponseWriter, request *http.Request) {
	configuration := self.store.Current()
	issuedAt := time.Now().Unix()
	value := fmt.Sprintf("%d.%s", issuedAt, self.signSession(issuedAt))

	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure would stop the cookie working entirely: these devices are
		// reached over plain HTTP on a local network, and there is no
		// certificate anybody could issue for an address on it. Anyone in a
		// position to read the traffic is already on the network the screen
		// is on.
		Secure:  request.TLS != nil,
		Expires: time.Now().Add(configuration.Web.SessionLifetime.Duration()),
		MaxAge:  int(configuration.Web.SessionLifetime.Duration().Seconds()),
	})
}

func (self *Server) clearSession(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (self *Server) signSession(issuedAt int64) string {
	secret := self.store.Current().Web.SessionSecret.Reveal()
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "session:%d", issuedAt)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// hasSession reports whether the request carries a valid, unexpired session.
func (self *Server) hasSession(request *http.Request) bool {
	cookie, err := request.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return false
	}

	issued, signature, found := strings.Cut(cookie.Value, ".")
	if !found {
		return false
	}
	issuedAt, err := strconv.ParseInt(issued, 10, 64)
	if err != nil {
		return false
	}
	if !security.EqualString(signature, self.signSession(issuedAt)) {
		return false
	}

	lifetime := self.store.Current().Web.SessionLifetime.Duration()
	return time.Since(time.Unix(issuedAt, 0)) < lifetime
}

// isSetUp reports whether the device has an administrator password yet.
// Before it has, the interface shows the onboarding wizard and the only thing
// the API will do is finish it.
func (self *Server) isSetUp() bool {
	return self.store.Current().Web.PasswordHash != ""
}

// requireSession is the middleware in front of everything that is not
// health, setting up, or signing in.
func (self *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if arrivedThroughTunnel(request) {
			// The fleet tunnel is authenticated by the device's own
			// credential before a single byte of HTTP crosses it, so a
			// request that arrived on it has already proved more than a
			// password would. It is treated as signed in.
			//
			// This is also why the tunnel serves this handler rather than one
			// of its own: the service gets exactly the interface an operator
			// standing in front of the screen would get, and there is no
			// second, more privileged way in to audit separately.
			next.ServeHTTP(response, request)
			return
		}
		if !self.isSetUp() {
			writeError(response, http.StatusForbidden, "this device has not been set up yet")
			return
		}
		if !self.hasSession(request) {
			writeError(response, http.StatusUnauthorized, "sign in first")
			return
		}
		next.ServeHTTP(response, request)
	})
}

// tunnelMarker is the context key marking a request that arrived through the
// fleet tunnel. It is a private type so that nothing outside this package can
// set it, which matters: setting it is the same as being signed in.
type tunnelMarker struct{}

// TunnelHandler is the handler the fleet tunnel serves. It is this server's
// own handler with every request marked as having arrived through the tunnel.
func (self *Server) TunnelHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		marked := request.WithContext(context.WithValue(request.Context(), tunnelMarker{}, true))
		self.router.ServeHTTP(response, marked)
	})
}

func arrivedThroughTunnel(request *http.Request) bool {
	marked, _ := request.Context().Value(tunnelMarker{}).(bool)
	return marked
}
