// Package security holds the small number of things this project does that
// have to be got right: generating identifiers that do not collide, hashing
// the administrator's password, and comparing secrets without leaking how
// much of them matched.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// identifierEncoding is Crockford-style: lower case, no padding, and without
// the letters that are read wrongly off a screen. Identifiers end up in URLs
// and in support conversations, so they are made to be read aloud.
var identifierEncoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// NewIdentifier returns a random identifier of 16 characters, which is 80
// bits. Used for the device identifier and for playlist items, both of which
// are generated once and never change, because something else refers to them
// afterwards.
func NewIdentifier() string {
	buffer := make([]byte, 10)
	if _, err := rand.Read(buffer); err != nil {
		// crypto/rand does not fail on any system this runs on, and a device
		// that cannot produce randomness must not carry on and pretend it can.
		panic(fmt.Sprintf("security: cannot read randomness: %s", err))
	}
	return identifierEncoding.EncodeToString(buffer)
}

// NewToken returns a random secret of 32 bytes, base64 encoded, for session
// signing keys and API tokens.
func NewToken() string {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		panic(fmt.Sprintf("security: cannot read randomness: %s", err))
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}

// The argon2id parameters. These are the values the RFC 9106 second
// recommended option gives, sized for a device with very little memory: 64 MB
// is affordable on a compute stick with 2 GB and is enough that guessing is
// expensive. Changing them does not invalidate existing hashes, because the
// parameters are stored in the hash string.
const (
	hashTime    = 2
	hashMemory  = 64 * 1024
	hashThreads = 1
	hashLength  = 32
	saltLength  = 16
)

// HashPassword returns an encoded argon2id hash, in the standard format that
// records the parameters alongside the digest so that they can be raised later
// without locking anybody out.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("security: refusing to hash an empty password")
	}
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("security: cannot read randomness: %w", err)
	}
	digest := argon2.IDKey([]byte(password), salt, hashTime, hashMemory, hashThreads, hashLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, hashMemory, hashTime, hashThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest)), nil
}

// VerifyPassword reports whether the password matches the encoded hash. A
// malformed hash is a mismatch rather than an error, so that a corrupted
// configuration file locks the operator out rather than letting them in.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	digest := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(expected)))
	return Equal(digest, expected)
}

// Equal compares two secrets in constant time.
func Equal(first, second []byte) bool {
	return subtle.ConstantTimeCompare(first, second) == 1
}

// EqualString compares two secrets in constant time.
func EqualString(first, second string) bool {
	return Equal([]byte(first), []byte(second))
}
