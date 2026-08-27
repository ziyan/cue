package onboarding

import (
	"path/filepath"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

func newTestOnboarding(t *testing.T) *Onboarding {
	t.Helper()
	configuration := config.Default()
	configuration.Paths.State = t.TempDir()
	configuration.Paths.Runtime = t.TempDir()
	configuration.Normalize()
	// No manager: these tests never get as far as joining anything.
	return New(config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration), nil)
}

// The setup network goes down and comes back twice in normal use: to free the
// radio for a scan, and to free it to try joining. It has to come back as the
// same network, or the phone that already joined it cannot find it again --
// and the new password would be on a screen the person has walked away from.
func TestTheSetupNetworkKeepsItsNameAndPasswordAcrossRestarts(t *testing.T) {
	onboarding := newTestOnboarding(t)

	first, err := onboarding.sessionCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if first.SSID == "" || first.Passphrase == "" {
		t.Fatal("no credentials were invented")
	}

	again, err := onboarding.sessionCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("asking twice gave %+v and then %+v; a phone could not rejoin", first, again)
	}
}

// Ending a session must forget the password, so that the next one is a new
// network rather than one somebody wrote down last month.
func TestFinishingASessionForgetsThePassword(t *testing.T) {
	onboarding := newTestOnboarding(t)

	first, err := onboarding.sessionCredentials()
	if err != nil {
		t.Fatal(err)
	}
	onboarding.Finish(t.Context())

	if got := onboarding.Credentials(); got.Passphrase != "" {
		t.Errorf("the passphrase %q survived the session ending", got.Passphrase)
	}

	second, err := onboarding.sessionCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if second.Passphrase == first.Passphrase {
		t.Error("a new session came up with the same passphrase as the last one")
	}
}

// Nothing may be joined when no setup network is running: the request would
// otherwise take down whatever the device is doing.
func TestJoiningIsRefusedWhenTheDeviceIsNotBeingSetUp(t *testing.T) {
	onboarding := newTestOnboarding(t)

	if err := onboarding.Join(t.Context(), "somewhere", "a test passphrase"); err == nil {
		t.Error("a join was accepted on a device that is not being set up")
	}
	if err := onboarding.Rescan(t.Context()); err == nil {
		t.Error("a scan was accepted on a device that is not being set up")
	}
}

// A network chosen on the phone has to end up in the configuration, with
// management switched on, because that is what actually joins it and what
// brings it back after a power cut.
func TestTheChosenNetworkIsWrittenIntoTheConfiguration(t *testing.T) {
	onboarding := newTestOnboarding(t)

	onboarding.remember("wlan0", "the office", "a test passphrase")

	configuration := onboarding.store.Current()
	if !configuration.Network.Manage {
		t.Error("the network was chosen but managing the network is still off, so " +
			"nothing will ever join it")
	}
	if len(configuration.Network.Interfaces) != 1 {
		t.Fatalf("the configuration has %d interface(s), want 1", len(configuration.Network.Interfaces))
	}
	written := configuration.Network.Interfaces[0]
	if written.Name != "wlan0" || written.Method != "dhcp" {
		t.Errorf("the interface was written as %+v", written)
	}
	if written.Wireless == nil || written.Wireless.SSID != "the office" {
		t.Errorf("the network was written as %+v", written.Wireless)
	}
	if written.Wireless != nil && string(written.Wireless.Passphrase) != "a test passphrase" {
		t.Error("the passphrase was not written, so the device could join once and " +
			"never again")
	}
}

// A network that did not work has to come back out again. Left in the
// configuration it is retried for ever, which keeps the radio busy and stops
// the setup network coming back -- so a mistyped password would strand the
// device rather than costing forty-five seconds.
func TestANetworkThatDidNotWorkIsTakenBackOut(t *testing.T) {
	onboarding := newTestOnboarding(t)

	onboarding.remember("wlan0", "the office", "the wrong test passphrase")
	onboarding.forget("wlan0")

	configuration := onboarding.store.Current()
	if len(configuration.Network.Interfaces) != 0 {
		t.Errorf("the network that did not work is still configured: %+v",
			configuration.Network.Interfaces)
	}
	if configuration.Network.Manage {
		t.Error("managing the network was left on after the only reason for it failed")
	}
}

// Choosing a second network must replace the first rather than adding to it:
// two entries for one interface is a device that cannot decide.
func TestChoosingAgainReplacesTheFirstChoice(t *testing.T) {
	onboarding := newTestOnboarding(t)

	onboarding.remember("wlan0", "the office", "a test passphrase")
	onboarding.remember("wlan0", "upstairs", "another test passphrase")

	configuration := onboarding.store.Current()
	if len(configuration.Network.Interfaces) != 1 {
		t.Fatalf("the configuration has %d entries for one interface", len(configuration.Network.Interfaces))
	}
	if got := configuration.Network.Interfaces[0].Wireless.SSID; got != "upstairs" {
		t.Errorf("the interface is configured for %q, want the second choice", got)
	}
}
