package timesync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

func newTestClient(t *testing.T, change func(*config.Configuration)) *Client {
	t.Helper()
	configuration := config.Default()
	configuration.Paths.Runtime = t.TempDir()
	configuration.Paths.State = t.TempDir()
	if change != nil {
		change(configuration)
	}
	configuration.Normalize()

	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)
	return New(store)
}

func generated(t *testing.T, client *Client) string {
	t.Helper()
	if err := client.prepare(context.Background()); err != nil {
		t.Fatalf("prepare: %s", err)
	}
	content, err := os.ReadFile(client.configFilename)
	if err != nil {
		t.Fatalf("read: %s", err)
	}
	return string(content)
}

func TestTheClockIsAllowedToBeSteppedAtAnyTime(t *testing.T) {
	// This is the setting that matters, and the default is wrong for a
	// display. chronyd normally stops stepping the clock after the first
	// three updates, so a device whose battery has died comes up years out
	// on every boot and stays there — showing certificate errors instead of
	// the dashboard, and blaming the certificate.
	content := generated(t, newTestClient(t, nil))
	if !strings.Contains(content, "makestep 1.0 -1") {
		t.Errorf("the clock is not allowed to be stepped at any time:\n%s", content)
	}
}

func TestTheConfiguredServersAreAskedFourTimesAtOnce(t *testing.T) {
	client := newTestClient(t, func(configuration *config.Configuration) {
		configuration.Time.Servers = []string{"time.example.com", "time.example.net"}
	})
	content := generated(t, client)

	for _, server := range []string{"time.example.com", "time.example.net"} {
		if !strings.Contains(content, server) {
			t.Errorf("%s is not in the generated configuration:\n%s", server, content)
		}
	}
	// iburst asks four times in the first few seconds rather than once a
	// minute, so a device that has just booted is right almost at once
	// rather than after several minutes of certificate errors.
	if !strings.Contains(content, "iburst") {
		t.Errorf("the servers are not asked with iburst:\n%s", content)
	}
}

func TestNothingCanAskThisDeviceForTheTime(t *testing.T) {
	// A display is a client, not a time server.
	content := generated(t, newTestClient(t, nil))
	if !strings.Contains(content, "port 0") {
		t.Errorf("the daemon would serve time to the network:\n%s", content)
	}
}

func TestTheDirectoriesChronydNeedsArePreparedWithTheRightPermissions(t *testing.T) {
	// chronyd refuses to open its command socket in a directory anybody can
	// write to, and says so as "Wrong permissions on <directory>". It also
	// writes a pid file into a directory it does not create.
	client := newTestClient(t, nil)
	if err := client.prepare(context.Background()); err != nil {
		t.Fatalf("prepare: %s", err)
	}

	information, err := os.Stat(client.runtimeDirectory)
	if err != nil {
		t.Fatalf("the runtime directory was not created: %s", err)
	}
	if mode := information.Mode().Perm(); mode&0o007 != 0 {
		t.Errorf("the runtime directory is mode %o; chronyd refuses one others can reach", mode)
	}

	if _, err := os.Stat(client.driftDirectory); err != nil {
		t.Errorf("the drift directory was not created: %s", err)
	}

	content := generated(t, client)
	if !strings.Contains(content, "pidfile") {
		t.Errorf("no pid file was named, and chronyd will not create the directory for the default one:\n%s", content)
	}
	if !strings.Contains(content, client.socketPath()) {
		t.Errorf("the command socket is not where the daemon looks for it:\n%s", content)
	}
}

func TestRewritingTheConfigurationIsSafeToRepeat(t *testing.T) {
	// It is rewritten before every start, so that changing the servers takes
	// effect on the next restart without anybody editing a second file.
	client := newTestClient(t, nil)
	first := generated(t, client)
	second := generated(t, client)
	if first != second {
		t.Error("preparing twice produced two different configurations")
	}
}

func TestAClockThatIsWildlyOutIsWorthSayingSoAbout(t *testing.T) {
	// The point of the warning is that the screen will show certificate
	// errors until it is fixed, and nothing else will say why.
	if message := ClockWarning(State{Enabled: true, OffsetSeconds: 3600}); message == "" {
		t.Error("an hour of drift produced no warning")
	}
	if message := ClockWarning(State{Enabled: true, OffsetSeconds: -3600}); message == "" {
		t.Error("an hour of drift the other way produced no warning")
	}
	if message := ClockWarning(State{Enabled: true, OffsetSeconds: 0.01}); message != "" {
		t.Errorf("a correct clock produced a warning: %s", message)
	}
	if message := ClockWarning(State{Enabled: false, OffsetSeconds: 3600}); message != "" {
		t.Errorf("a clock nobody is managing produced a warning: %s", message)
	}
}
