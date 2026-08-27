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
	return New(config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration))
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
