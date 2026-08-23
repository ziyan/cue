// Package xauth writes the file an X server and its clients use to agree that
// they are allowed to talk to each other.
//
// The X server accepts a connection only from a client that presents a shared
// secret, and the secret is kept in a file — usually ~/.Xauthority — in a
// format that predates every convention a modern file would follow. There is
// no library for it, and the tool that manipulates it is a shell command this
// project does not ship, so it is written here. It is thirty lines of
// big-endian length-prefixed strings.
package xauth

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"

	"github.com/ziyan/cue/internal/util/atomicfile"
)

const (
	// familyLocal means the entry matches a connection from one named host.
	familyLocal = 256

	// familyWild means the entry matches a connection from anywhere. The
	// daemon writes one of these as well as a local entry, because inside a
	// container the host name is a random hexadecimal string that changes on
	// every start, and a client that resolved it a moment earlier would
	// otherwise fail to match its own entry.
	familyWild = 65535

	// cookieName is the only authentication scheme anybody still uses: a
	// sixteen byte secret, presented verbatim.
	cookieName = "MIT-MAGIC-COOKIE-1"

	cookieLength = 16
)

// Cookie is a shared secret for one X display.
type Cookie []byte

// NewCookie returns a fresh random cookie.
func NewCookie() (Cookie, error) {
	cookie := make([]byte, cookieLength)
	if _, err := rand.Read(cookie); err != nil {
		return nil, fmt.Errorf("xauth: cannot read randomness: %w", err)
	}
	return cookie, nil
}

// Write creates an authority file granting access to one display number.
//
// The file is written 0640 rather than 0600 because two accounts need it: the
// daemon and the X server run as root, and the browser runs as somebody else.
// The caller sets the group.
func Write(filename string, displayNumber int, cookie Cookie) error {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}
	number := strconv.Itoa(displayNumber)

	var buffer bytes.Buffer
	if err := writeEntry(&buffer, familyLocal, hostname, number, cookie); err != nil {
		return err
	}
	if err := writeEntry(&buffer, familyWild, "", number, cookie); err != nil {
		return err
	}

	return atomicfile.Write(filename, buffer.Bytes(), 0o640)
}

// writeEntry appends one record: a family, then four length-prefixed strings.
func writeEntry(buffer *bytes.Buffer, family uint16, address, number string, cookie Cookie) error {
	if err := binary.Write(buffer, binary.BigEndian, family); err != nil {
		return fmt.Errorf("xauth: %w", err)
	}
	for _, field := range [][]byte{[]byte(address), []byte(number), []byte(cookieName), cookie} {
		if len(field) > 0xffff {
			return fmt.Errorf("xauth: a field of %d bytes does not fit in the format", len(field))
		}
		if err := binary.Write(buffer, binary.BigEndian, uint16(len(field))); err != nil {
			return fmt.Errorf("xauth: %w", err)
		}
		if _, err := buffer.Write(field); err != nil {
			return fmt.Errorf("xauth: %w", err)
		}
	}
	return nil
}
