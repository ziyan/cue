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
	"time"

	"golang.org/x/crypto/argon2"
)

// identifierEncoding is Crockford-style: lower case, no padding, and without
// the letters that are read wrongly off a screen. Identifiers end up in URLs
// and in support conversations, so they are made to be read aloud.
var identifierEncoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// ulidEncoding is Crockford's, upper case. The same alphabet as
// identifierEncoding and the same reasons for it, in the case a ULID is
// canonically written in.
var ulidEncoding = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewDeviceIdentifier returns a ULID: 26 characters of Crockford base32, a
// 48-bit millisecond timestamp followed by 80 bits of randomness.
//
// A device's identifier is a ULID rather than the shorter random one because
// the hosted service uses it as its own name for the device. One name for one
// thing was the point: cue used to mint an identifier of its own beside this
// one, and two names for a device is two things to match up whenever anybody
// looks at both systems at once.
//
// The timestamp at the front is not needed by anything here. It comes with the
// format, and it means identifiers sort by when the device first ran, which is
// a small kindness to whoever reads a list of them.
func NewDeviceIdentifier() string {
	milliseconds := time.Now().UnixMilli()
	random := make([]byte, 10)
	if _, err := rand.Read(random); err != nil {
		panic(fmt.Sprintf("security: cannot read randomness: %s", err))
	}

	// 128 bits, most significant first: six bytes of timestamp then ten of
	// randomness.
	value := make([]byte, 16)
	for index := 0; index < 6; index++ {
		value[5-index] = byte(milliseconds >> (8 * index))
	}
	copy(value[6:], random)

	// Base32 by hand, five bits at a time from the top. encoding/base32 pads
	// to a multiple of eight characters and a ULID is twenty-six, so doing it
	// directly is shorter than trimming what that would produce.
	written := make([]byte, 26)
	for index := 0; index < 26; index++ {
		bit := index * 5
		// The two extra bits at the front of a 130-bit window over 128 bits
		// are zero, which is what keeps the first character at '7' or below.
		var chunk int
		for offset := 0; offset < 5; offset++ {
			chunk <<= 1
			at := bit + offset - 2
			if at >= 0 && at < 128 {
				chunk |= int(value[at/8]>>(7-at%8)) & 1
			}
		}
		written[index] = ulidEncoding[chunk]
	}
	return string(written)
}

// IsDeviceIdentifier reports whether a string is one of ours: twenty-six
// characters of Crockford base32 whose timestamp has not overflowed.
//
// Written here rather than assumed at the far end, because the service refuses
// anything else and a device that emitted a name it would not take could not
// link at all.
func IsDeviceIdentifier(value string) bool {
	if len(value) != 26 {
		return false
	}
	// A ULID's 48-bit timestamp leaves two spare bits at the front, so the
	// first character can never be above '7'.
	if strings.IndexByte(ulidEncoding, upperOf(value[0])) > 7 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if strings.IndexByte(ulidEncoding, upperOf(value[index])) < 0 {
			return false
		}
	}
	return true
}

func upperOf(letter byte) byte {
	if letter >= 'a' && letter <= 'z' {
		return letter - 'a' + 'A'
	}
	return letter
}

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
