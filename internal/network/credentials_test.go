package network

import (
	"strings"
	"testing"
)

func TestTheNetworkIsNamedForCueAndIsDifferentEveryTime(t *testing.T) {
	seen := map[string]bool{}
	for attempt := 0; attempt < 200; attempt++ {
		credentials, err := NewCredentials()
		if err != nil {
			t.Fatalf("inventing credentials: %s", err)
		}
		if !strings.HasPrefix(credentials.SSID, "cue-") {
			t.Fatalf("the network is called %q, which nobody will recognise as this device", credentials.SSID)
		}
		if len(credentials.SSID) != len("cue-")+6 {
			t.Fatalf("the network is called %q; the name should be cue- and six characters", credentials.SSID)
		}
		if seen[credentials.SSID] {
			t.Fatalf("%q came up twice in 200 tries, so the name is not random enough "+
				"for two devices in one room", credentials.SSID)
		}
		seen[credentials.SSID] = true
	}
}

// WPA2 refuses a passphrase shorter than 8 characters or longer than 63, and a
// device that generated one outside that range would fail to start its own
// network with an error nobody could act on.
func TestThePassphraseIsOneWPA2WillAccept(t *testing.T) {
	credentials, err := NewCredentials()
	if err != nil {
		t.Fatalf("inventing credentials: %s", err)
	}
	if length := len(credentials.Passphrase); length < 8 || length > 63 {
		t.Fatalf("the passphrase is %d characters, and WPA2 takes 8 to 63", length)
	}
}

// Nobody should have to read these off a screen, but somebody will when a
// camera will not focus, and telling a one from an ell at four metres is not
// something to ask of them.
func TestNeitherNameNorPassphraseUsesCharactersPeopleConfuse(t *testing.T) {
	credentials, err := NewCredentials()
	if err != nil {
		t.Fatalf("inventing credentials: %s", err)
	}
	for _, confusing := range []string{"l", "1", "O", "0", "I"} {
		if strings.Contains(credentials.SSID, confusing) && confusing != "0" {
			t.Errorf("the name %q contains %q", credentials.SSID, confusing)
		}
		if strings.Contains(credentials.Passphrase, confusing) {
			t.Errorf("the passphrase contains %q", confusing)
		}
	}
}

func TestTheJoinCodeIsWhatAPhoneExpects(t *testing.T) {
	credentials := Credentials{SSID: "cue-4k2p9x", Passphrase: "hd7Rk2m9Qw4x"}
	if got, want := credentials.JoinCode(), "WIFI:S:cue-4k2p9x;T:WPA;P:hd7Rk2m9Qw4x;;"; got != want {
		t.Errorf("the join code is\n  %s\nand a phone expects\n  %s", got, want)
	}
}

// A semicolon inside a passphrase would end the field early and the phone
// would join with the wrong password -- silently, because the network would
// simply refuse it and the person would blame the device.
func TestASemicolonInAPassphraseIsEscapedRatherThanEndingTheField(t *testing.T) {
	credentials := Credentials{SSID: `cue-a;b`, Passphrase: `pass;word\x`}
	code := credentials.JoinCode()
	if !strings.Contains(code, `cue-a\;b`) {
		t.Errorf("the semicolon in the name is not escaped: %s", code)
	}
	if !strings.Contains(code, `pass\;word\\x`) {
		t.Errorf("the semicolon or the backslash in the passphrase is not escaped: %s", code)
	}
}

// The passphrase must not be predictable from the name, which is the one thing
// about this network that is broadcast to everybody in range.
func TestTheSameCredentialsAreNotProducedTwice(t *testing.T) {
	first, err := NewCredentials()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if first.Passphrase == second.Passphrase {
		t.Error("two sessions produced the same passphrase")
	}
}
