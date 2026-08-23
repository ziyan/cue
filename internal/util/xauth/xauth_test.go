package xauth

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteProducesAFileTheXProtocolCanRead(t *testing.T) {
	cookie, err := NewCookie()
	if err != nil {
		t.Fatalf("new cookie: %s", err)
	}
	filename := filepath.Join(t.TempDir(), "Xauthority")
	if err := Write(filename, 7, cookie); err != nil {
		t.Fatalf("write: %s", err)
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read: %s", err)
	}

	// Parse it back the way a client does, and check that both entries are
	// there: one for this host and one that matches anywhere.
	entries := 0
	sawWild := false
	offset := 0
	for offset < len(content) {
		family := binary.BigEndian.Uint16(content[offset:])
		offset += 2
		if family == familyWild {
			sawWild = true
		}
		var fields [4]string
		for index := range fields {
			if offset+2 > len(content) {
				t.Fatalf("the file ends in the middle of an entry")
			}
			length := int(binary.BigEndian.Uint16(content[offset:]))
			offset += 2
			if offset+length > len(content) {
				t.Fatalf("a field claims %d bytes but the file has %d left", length, len(content)-offset)
			}
			fields[index] = string(content[offset : offset+length])
			offset += length
		}
		if fields[1] != "7" {
			t.Errorf("the display number is %q, want 7", fields[1])
		}
		if fields[2] != cookieName {
			t.Errorf("the scheme is %q, want %s", fields[2], cookieName)
		}
		if fields[3] != string(cookie) {
			t.Error("the cookie in the file is not the one that was written")
		}
		entries++
	}

	if entries != 2 {
		t.Errorf("the file has %d entries, want 2", entries)
	}
	if !sawWild {
		// Inside a container the host name changes on every start, so a
		// local-only entry would stop matching.
		t.Error("there is no wildcard entry, so a client on a renamed host cannot authenticate")
	}
}

func TestTwoCookiesDiffer(t *testing.T) {
	first, err := NewCookie()
	if err != nil {
		t.Fatalf("new cookie: %s", err)
	}
	second, err := NewCookie()
	if err != nil {
		t.Fatalf("new cookie: %s", err)
	}
	if string(first) == string(second) {
		t.Fatal("two cookies are the same, so they are not random")
	}
	if len(first) != cookieLength {
		t.Errorf("a cookie is %d bytes, want %d", len(first), cookieLength)
	}
}
