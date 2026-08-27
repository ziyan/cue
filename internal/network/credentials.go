package network

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// Credentials are the name and passphrase of the temporary wireless network a
// device runs while somebody sets it up from a phone.
//
// They live in memory and nowhere else. They are worthless the moment
// onboarding ends, and writing them into the configuration file would only
// leave a secret lying on the disk of every device that was ever set up this
// way.
type Credentials struct {
	// SSID is the network name, "cue-" and six random characters.
	SSID string

	// Passphrase is the WPA2 password for it.
	Passphrase string
}

// nameAlphabet and passphraseAlphabet leave out the characters people confuse
// for one another. Nobody should have to read either of these off a screen --
// that is what the QR code is for -- but somebody will, when a camera will not
// focus or a phone is too old, and "was that a one or an ell" is a bad way to
// end up unable to set up a screen.
const nameAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
const passphraseAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"

// NewCredentials invents a name and a passphrase for one setup session.
//
// The name is random rather than derived from the device identifier. A device
// waiting to be set up broadcasts this name continuously, and a name derived
// from the identifier would tell anybody in range which device this is, for as
// long as it sits there unconfigured. Random also means two devices unboxed in
// the same room cannot collide.
//
// The passphrase is twelve characters from an alphabet of 54, which is about
// 69 bits. That is far more than a network living for half an hour needs, and
// it costs nothing because nobody types it.
func NewCredentials() (Credentials, error) {
	name, err := randomText(nameAlphabet, 6)
	if err != nil {
		return Credentials{}, err
	}
	passphrase, err := randomText(passphraseAlphabet, 12)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{SSID: "cue-" + name, Passphrase: passphrase}, nil
}

// JoinCode is what goes into the QR code on the screen. A phone camera reading
// this offers to join the network, with nothing typed by hand.
//
// The format is the de-facto one every phone understands: fields separated by
// semicolons, S for the name, T for the security type, P for the passphrase,
// and a closing empty field. WPA covers WPA2 here; there is no separate code
// for it and phones treat the two the same.
//
// Semicolons, commas, colons, backslashes and double quotes inside a value
// have to be escaped with a backslash, or a passphrase containing one would
// end the field early and the phone would join with the wrong password. The
// alphabets above contain none of them, so this cannot happen today -- it is
// done anyway, because the day somebody widens the alphabet is not the day
// they will remember this function exists.
func (self Credentials) JoinCode() string {
	return fmt.Sprintf("WIFI:S:%s;T:WPA;P:%s;;",
		escapeForJoinCode(self.SSID), escapeForJoinCode(self.Passphrase))
}

func escapeForJoinCode(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if strings.ContainsRune(`\;,:"`, character) {
			builder.WriteRune('\\')
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

// randomText draws length characters from alphabet, using the cryptographic
// source because this is a password.
//
// The modulo of a random byte would favour the first characters of an alphabet
// that does not divide 256. Bytes that would land in the short tail are thrown
// away and drawn again instead, which keeps every character equally likely.
func randomText(alphabet string, length int) (string, error) {
	limit := byte(256 - (256 % len(alphabet)))
	result := make([]byte, 0, length)
	buffer := make([]byte, length)
	for len(result) < length {
		if _, err := rand.Read(buffer); err != nil {
			return "", fmt.Errorf("network: no randomness for a passphrase: %w", err)
		}
		for _, drawn := range buffer {
			if drawn >= limit {
				continue
			}
			result = append(result, alphabet[int(drawn)%len(alphabet)])
			if len(result) == length {
				break
			}
		}
	}
	return string(result), nil
}
