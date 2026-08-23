package security

import (
	"strings"
	"testing"
)

func TestNewIdentifierIsStableInShapeAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for index := 0; index < 1000; index++ {
		identifier := NewIdentifier()
		if len(identifier) != 16 {
			t.Fatalf("identifier %q is %d characters, want 16", identifier, len(identifier))
		}
		if strings.ContainsAny(identifier, "ilou") {
			t.Fatalf("identifier %q contains a character that is misread aloud", identifier)
		}
		if seen[identifier] {
			t.Fatalf("identifier %q was generated twice in 1000 tries", identifier)
		}
		seen[identifier] = true
	}
}

func TestPasswordRoundTrip(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %s", err)
	}
	if strings.Contains(encoded, "correct") {
		t.Fatalf("the hash contains the password: %s", encoded)
	}
	if !VerifyPassword(encoded, "correct horse battery staple") {
		t.Error("the right password was rejected")
	}
	if VerifyPassword(encoded, "correct horse battery stapl") {
		t.Error("a wrong password was accepted")
	}
	if VerifyPassword(encoded, "") {
		t.Error("an empty password was accepted")
	}
}

func TestHashingTheSamePasswordTwiceGivesDifferentHashes(t *testing.T) {
	first, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("hash: %s", err)
	}
	second, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("hash: %s", err)
	}
	if first == second {
		t.Error("two hashes of the same password are identical, so the salt is not random")
	}
}

func TestMalformedHashIsRejectedRatherThanAccepted(t *testing.T) {
	// A corrupted configuration file must lock the operator out, not let
	// everybody in.
	for _, encoded := range []string{"", "not a hash", "$argon2id$", "$argon2id$v=19$m=1,t=1,p=1$!!!$!!!"} {
		if VerifyPassword(encoded, "anything") {
			t.Errorf("the malformed hash %q accepted a password", encoded)
		}
	}
}

func TestEmptyPasswordIsRefused(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Error("hashing an empty password should be refused")
	}
}
