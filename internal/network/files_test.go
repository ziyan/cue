package network

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

func testConfiguration(t *testing.T) *config.Configuration {
	t.Helper()
	configuration := config.Default()
	configuration.Paths.State = t.TempDir()
	configuration.Normalize()
	return configuration
}

// wpa_supplicant owns the file for an interface and saves into it the networks
// it has been told to join, names and passphrases both. Leaving that behind
// when the file moved would be a device that forgets every wireless network it
// knew and cannot get back onto one without somebody standing in front of it.
func TestTheWirelessConfigurationsFromAnOlderVersionAreMoved(t *testing.T) {
	configuration := testConfiguration(t)
	state := configuration.Paths.State

	loose := map[string]string{
		"wpa_supplicant-wlan0.conf":    "ctrl_interface=/run/wpa_supplicant\nnetwork={\n\tssid=\"a test network\"\n}\n",
		"wpa_supplicant-ap-wlan0.conf": "ctrl_interface=/run/wpa_supplicant\n",
	}
	for name, content := range loose {
		if err := os.WriteFile(filepath.Join(state, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Something that is not ours must be left where it is.
	if err := os.WriteFile(filepath.Join(state, "cue.yaml"), []byte("device:\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	AdoptOldFiles(configuration)

	directory := Directory(configuration)
	for name, content := range loose {
		moved, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Errorf("%s was not moved: %s", name, err)
			continue
		}
		if string(moved) != content {
			t.Errorf("%s changed on the way: %q", name, moved)
		}
		if _, err := os.Stat(filepath.Join(state, name)); !os.IsNotExist(err) {
			t.Errorf("%s is still in the old place as well", name)
		}
	}
	if _, err := os.Stat(filepath.Join(state, "cue.yaml")); err != nil {
		t.Error("something that is not this package's was moved")
	}
}

// A file this version has already written wins: the old one is stale, and
// overwriting the live one would lose whatever has been joined since.
func TestAStaleConfigurationDoesNotOverwriteTheLiveOne(t *testing.T) {
	configuration := testConfiguration(t)
	state := configuration.Paths.State
	directory := Directory(configuration)

	const name = "wpa_supplicant-wlan0.conf"
	if err := os.WriteFile(filepath.Join(state, name), []byte("the stale one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte("the live one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	AdoptOldFiles(configuration)

	content, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "the live one\n" {
		t.Errorf("the live configuration was overwritten with %q", content)
	}
}

// The directory holds passphrases, so nobody else on the machine may read it.
func TestTheDirectoryIsNotReadableByAnybodyElse(t *testing.T) {
	configuration := testConfiguration(t)

	directory := Directory(configuration)
	information, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if mode := information.Mode().Perm(); mode != 0o700 {
		t.Errorf("the directory holding wireless passphrases is mode %o, want 700", mode)
	}
}
