package vncserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

func newTestServer(t *testing.T, change func(*config.Configuration)) *Server {
	t.Helper()
	configuration := config.Default()
	configuration.Paths.Runtime = t.TempDir()
	configuration.Paths.State = t.TempDir()
	if change != nil {
		change(configuration)
	}
	configuration.Normalize()

	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)
	return New(store, ":0", "/run/cue/Xauthority")
}

func arguments(t *testing.T, server *Server) string {
	t.Helper()
	return strings.Join(server.Settings().Arguments, " ")
}

func TestTheServerStaysUpWhenTheLastViewerLeaves(t *testing.T) {
	// Without -forever it exits when the last viewer disconnects, and the
	// supervisor would spend the rest of the day restarting it.
	server := newTestServer(t, nil)
	line := arguments(t, server)

	for _, flag := range []string{"-forever", "-shared", "-noremote"} {
		if !strings.Contains(line, flag) {
			t.Errorf("%s is missing:\n%s", flag, line)
		}
	}
}

func TestTheAddressIsSplitIntoWhereAndWhichPort(t *testing.T) {
	server := newTestServer(t, func(configuration *config.Configuration) {
		configuration.VNC.Listen = "127.0.0.1:5911"
	})
	line := arguments(t, server)

	if !strings.Contains(line, "-rfbport 5911") {
		t.Errorf("the port did not reach the command line:\n%s", line)
	}
	if !strings.Contains(line, "-listen 127.0.0.1") {
		t.Errorf("the address did not reach the command line:\n%s", line)
	}
}

func TestAPasswordReplacesTheNoPasswordFlag(t *testing.T) {
	// -nopw and a password file together are contradictory, and x11vnc
	// refuses to start with neither.
	without := newTestServer(t, nil)
	if line := arguments(t, without); !strings.Contains(line, "-nopw") {
		t.Errorf("with no password, -nopw is needed or the server refuses to start:\n%s", line)
	}

	with := newTestServer(t, func(configuration *config.Configuration) {
		configuration.VNC.Password = "a test vnc password"
	})
	line := arguments(t, with)
	if strings.Contains(line, "-nopw") {
		t.Errorf("-nopw was passed alongside a password:\n%s", line)
	}
	if !strings.Contains(line, "-passwdfile") {
		t.Errorf("the password file was not named:\n%s", line)
	}
}

func TestThePasswordFileIsWrittenPrivatelyAndRemovedWhenUnset(t *testing.T) {
	server := newTestServer(t, func(configuration *config.Configuration) {
		configuration.VNC.Password = "a test vnc password"
	})
	if err := server.prepare(context.Background()); err != nil {
		t.Fatalf("prepare: %s", err)
	}

	information, err := os.Stat(server.passwordFilename)
	if err != nil {
		t.Fatalf("the password file was not written: %s", err)
	}
	if information.Mode().Perm() != 0o600 {
		t.Errorf("the password file is mode %o; it must not be readable by anybody else", information.Mode().Perm())
	}

	content, err := os.ReadFile(server.passwordFilename)
	if err != nil {
		t.Fatalf("read: %s", err)
	}
	if strings.TrimSpace(string(content)) != "a test vnc password" {
		t.Errorf("the file holds %q", strings.TrimSpace(string(content)))
	}

	// Clearing the password must remove the file, or the server would keep
	// asking for one nobody has.
	cleared := newTestServer(t, nil)
	cleared.passwordFilename = server.passwordFilename
	if err := cleared.prepare(context.Background()); err != nil {
		t.Fatalf("prepare: %s", err)
	}
	if _, err := os.Stat(server.passwordFilename); !os.IsNotExist(err) {
		t.Error("the password file survived the password being cleared")
	}
}

func TestViewersCanBeStoppedFromTyping(t *testing.T) {
	server := newTestServer(t, func(configuration *config.Configuration) {
		configuration.VNC.ViewOnly = true
	})
	if line := arguments(t, server); !strings.Contains(line, "-viewonly") {
		t.Errorf("view-only was asked for and not passed on:\n%s", line)
	}
}

func TestAnExposedListenerIsRecognisedAsExposed(t *testing.T) {
	// The daemon warns at every start when the screen can be watched from the
	// network with no password, so this has to be right about which is which.
	exposed := map[string]bool{
		"0.0.0.0:5900":    true,
		":5900":           true,
		"192.0.2.10:5900": true,
		"127.0.0.1:5900":  false,
		"[::1]:5900":      false,
	}
	for address, want := range exposed {
		if isExposed(address) != want {
			t.Errorf("%s: exposed=%v, want %v", address, !want, want)
		}
	}
}
