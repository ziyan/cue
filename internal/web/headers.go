package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
)

// The response headers that constrain what a page this server sends is allowed
// to do.
//
// The device renders arbitrary pages full screen and holds the credentials for
// the systems it displays, so its own interface is worth keeping narrow: a
// script that reached it could read the configuration, drive the screen, and
// ask the browser to go somewhere else. None of these headers stop a bug in
// this program. What they stop is a bug becoming somebody else's code running
// in the interface's origin.

// nonceKey carries the per-response nonce to the templates that need one.
type nonceKey struct{}

// contentSecurityPolicy is the policy every page gets, with the nonce filled in.
//
// script-src is 'self' plus the nonce and nothing else: the interface's own
// bundle is a file, and the five pages this server renders itself carry one
// inline script each, which the nonce covers. No 'unsafe-inline', so a script
// injected into any of them does not run.
//
// style-src has to allow inline, and it is the one concession here. The
// interface is built with MUI, which injects its styles into the document at
// runtime through emotion rather than shipping a stylesheet -- there is no
// nonce to give them, because the browser makes them rather than us. A style
// injection is a much smaller thing than a script injection, and this is the
// choice that keeps the rest strict rather than the whole policy theoretical.
//
// connect-src is 'self' for the API and the websocket that carries the screen.
// img-src allows data: because the linking code is drawn as an SVG and handed
// to an img as a data URL, and blob: because the screen viewer paints frames it
// has assembled itself.
//
// frame-ancestors 'none' rather than X-Frame-Options: this interface is never
// meant to be inside somebody else's page, and a screen that could be framed is
// one that could be clicked through.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'nonce-%s'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"media-src 'self' blob:; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// withSecurityHeaders wraps a handler so every response carries the policy and
// a fresh nonce.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		nonce := newNonce()

		response.Header().Set("Content-Security-Policy",
			strings.Replace(contentSecurityPolicy, "%s", nonce, 1))

		// A browser guessing at a content type is a browser that can be
		// persuaded to run an upload as a script.
		response.Header().Set("X-Content-Type-Options", "nosniff")

		// Where a request came from is not somebody else's business, and the
		// address of a screen on a private network is exactly the sort of
		// thing that should not travel.
		response.Header().Set("Referrer-Policy", "no-referrer")

		next.ServeHTTP(response, request.WithContext(
			context.WithValue(request.Context(), nonceKey{}, nonce)))
	})
}

// nonceOf returns the nonce for this response, for a template that has an
// inline script to mark.
func nonceOf(request *http.Request) string {
	nonce, _ := request.Context().Value(nonceKey{}).(string)
	return nonce
}

// newNonce returns 128 bits, base64, which is what the specification asks for:
// unguessable, and different on every response so that a nonce read from one
// page is no use on the next.
//
// URL-safe base64 rather than the standard alphabet, and that is not cosmetic.
// The standard alphabet contains + and /, and html/template escapes + as &#43;
// inside an attribute -- so the header said nonce-I0WiV0FN6jLrX+w+mJaVWg while
// the page said nonce="I0WiV0FN6jLrX&#43;w&#43;mJaVWg". A browser decodes the
// entity and the two match again, so it works; it works by way of a round trip
// through HTML entity decoding that nothing states and nothing tests. The
// URL-safe alphabet is - and _ instead, which no escaper touches, so the two
// are the same bytes and stay that way.
func newNonce() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		// Without randomness there is no safe nonce to hand out, and an empty
		// one would match nothing -- so the inline scripts on this device's
		// own pages stop running, rather than anything else starting to.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
