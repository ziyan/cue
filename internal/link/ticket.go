// Package link attaches this device to an account on the hosted service.
//
// The problem it solves is that a screen on a wall has no keyboard and the
// person in front of it has a phone. So the device shows a QR code, the phone
// opens it, somebody presses authorise on a page they are already signed in
// to, and the device ends up holding a credential. Nobody types anything into
// the screen.
//
// A code on a wall can be photographed by anybody who walks past, so what it
// carries must not be enough to take the device over. It carries a ticket, and
// the ticket is the hash of a verifier the device never sends anywhere except
// straight to the service:
//
//	verifier = 32 random bytes
//	ticket   = base64url(sha256(verifier))
//
// Somebody who photographs the screen gets the ticket. To finish a link they
// would have to produce the verifier it was hashed from, which is the part
// that never appeared on the screen.
//
// This is the shape of PKCE and it is here for the same reason: one party
// starts a flow that another finishes, over a channel that can be watched.
// Deriving the ticket rather than pairing two random values is what lets the
// device show a code before it has spoken to the service at all -- which
// matters, because pressing a button on a screen should not wait on a network
// that may be the thing being fixed.
package link

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ticketLifetime is how long one attempt at linking is good for.
//
// Long enough to find a phone, unlock it, open the camera, sign in to the
// service and read what is being asked. Short enough that a code left on a
// screen by somebody who wandered off stops being useful before the room
// empties.
const ticketLifetime = 10 * time.Minute

// Ticket is one attempt at linking, and the two halves it is made of.
type Ticket struct {
	// Ticket is the public half. It travels in the QR code and in the URL.
	Ticket string

	// Verifier is the secret half. It stays on this device.
	Verifier string

	// ExpiresAt is when this attempt stops being answerable.
	ExpiresAt time.Time
}

// IsExpired reports whether the attempt is past its life.
func (self *Ticket) IsExpired(now time.Time) bool {
	return self == nil || !now.Before(self.ExpiresAt)
}

// newTicket mints a verifier and derives its ticket.
func newTicket(now time.Time) (*Ticket, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return nil, fmt.Errorf("link: cannot generate a verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(buffer)
	return &Ticket{
		Ticket:    deriveTicket(verifier),
		Verifier:  verifier,
		ExpiresAt: now.Add(ticketLifetime),
	}, nil
}

// deriveTicket is the one place the relationship between the two halves is
// written down. The service computes the same thing to check an exchange, so
// changing it here changes the protocol.
func deriveTicket(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// URL is what the QR code carries and what a phone opens.
func (self *Ticket) URL(service string) (string, error) {
	if self == nil {
		return "", fmt.Errorf("link: no attempt is in progress")
	}
	return serviceURL(service, "link", self.Ticket)
}

// serviceURL builds an address on the configured service.
//
// One place understands what a service address looks like, so a device can be
// pointed at a service somewhere else -- a staging one, or a deployment that
// is not the public one -- without a different build, and without two
// functions disagreeing about trailing slashes.
func serviceURL(service string, segments ...string) (string, error) {
	base, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(service), "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("link: %q is not an address a phone could open", service)
	}
	parts := make([]string, 0, len(segments)+1)
	if trimmed := strings.Trim(base.Path, "/"); trimmed != "" {
		parts = append(parts, trimmed)
	}
	parts = append(parts, segments...)
	base.Path = "/" + strings.Join(parts, "/")
	return base.String(), nil
}
