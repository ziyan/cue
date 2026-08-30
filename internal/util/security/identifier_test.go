package security

import (
	"strings"
	"testing"
)

// The service takes a device's identifier as its own name for the device and
// refuses anything that is not a ULID, so a device that emitted something else
// could not link at all. This is the shape, written down on this side rather
// than assumed from the other.
func TestADeviceIdentifierIsAULID(t *testing.T) {
	seen := map[string]bool{}
	for attempt := 0; attempt < 200; attempt++ {
		one := NewDeviceIdentifier()

		if len(one) != 26 {
			t.Fatalf("%q is %d characters, want 26", one, len(one))
		}
		if !IsDeviceIdentifier(one) {
			t.Fatalf("%q is not one this device would accept back", one)
		}
		// Crockford's alphabet: no I, L, O or U, because they are read
		// wrongly off a screen and said wrongly down a telephone.
		for _, letter := range one {
			if strings.ContainsRune("ILOU", letter) {
				t.Fatalf("%q contains %q, which is not in the alphabet", one, letter)
			}
			if !strings.ContainsRune(ulidEncoding, letter) {
				t.Fatalf("%q contains %q, which is not base32", one, letter)
			}
		}
		// A 48-bit timestamp in a 50-bit space leaves the first character at
		// '7' or below, for ten thousand years or so.
		if one[0] > '7' {
			t.Fatalf("%q starts with %q, which no ULID does", one, one[0])
		}
		if seen[one] {
			t.Fatalf("%q was generated twice in %d tries", one, attempt)
		}
		seen[one] = true
	}
}

// Sorting by identifier sorts by when the device first ran, which is what the
// timestamp at the front is for.
func TestDeviceIdentifiersSortByWhenTheyWereMade(t *testing.T) {
	first := NewDeviceIdentifier()
	// The timestamp is milliseconds, so two in the same one share a prefix.
	// What must never happen is a later one sorting before an earlier one.
	for attempt := 0; attempt < 50; attempt++ {
		later := NewDeviceIdentifier()
		if later[:10] < first[:10] {
			t.Fatalf("%q was made after %q and sorts before it", later, first)
		}
	}
}

// What the service will and will not take.
func TestWhatCountsAsADeviceIdentifier(t *testing.T) {
	for _, good := range []string{
		"01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"01arz3ndektsv4rrffq69g5fav", // read case-insensitively at the far end
		"00000000000000000000000000",
		"7ZZZZZZZZZZZZZZZZZZZZZZZZZ",
	} {
		if !IsDeviceIdentifier(good) {
			t.Errorf("%q was refused", good)
		}
	}
	for _, bad := range []string{
		"",
		"t6ny2v00xad86aj0",            // the old sixteen-character form
		"01ARZ3NDEKTSV4RRFFQ69G5FA",   // one short
		"01ARZ3NDEKTSV4RRFFQ69G5FAVX", // one long
		"8ZZZZZZZZZZZZZZZZZZZZZZZZZ",  // the timestamp has overflowed
		"01ARZ3NDEKTSV4RRFFQ69G5FAI",  // I is not in the alphabet
		"01ARZ3NDEKTSV4RRFFQ69G5FAL",  // nor L
		"01ARZ3NDEKTSV4RRFFQ69G5FAO",  // nor O
		"01ARZ3NDEKTSV4RRFFQ69G5FAU",  // nor U
		"01ARZ3NDEKTSV4RRFFQ69G5F-V",
	} {
		if IsDeviceIdentifier(bad) {
			t.Errorf("%q was accepted", bad)
		}
	}
}
