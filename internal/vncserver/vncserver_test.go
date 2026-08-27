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
	return strings.Join(server.Settings().CommandLine(), " ")
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

// A server told to listen on an IPv4 address must not turn up on the IPv6
// wildcard, which is what x11vnc does left to itself.
//
// This is not hypothetical. A device configured for the loopback was found
// listening on [::]:5900 with no password while holding a globally routable
// address, so the only thing between its screen and the internet was a
// firewall somewhere upstream that nobody had checked.
func TestListeningOnIPv4DoesNotAlsoListenOnEveryIPv6Address(t *testing.T) {
	// 0.0.0.0 counts: it asks for every IPv4 interface, not for every
	// interface, and carbon is configured exactly that way.
	for _, listen := range []string{"127.0.0.1:5900", "192.0.2.10:5900", "0.0.0.0:5900"} {
		server := newTestServer(t, func(configuration *config.Configuration) {
			configuration.VNC.Listen = listen
		})
		got := arguments(t, server)
		// Both, because on port 5900 neither one alone closes the socket.
		for _, flag := range []string{"-no6", "-rfbportv6 -1"} {
			if !strings.Contains(got, flag) {
				t.Errorf("listening on %s does not pass %s, so x11vnc will "+
					"also listen on [::]: %s", listen, flag, got)
			}
		}
	}
}

// Asking for an IPv6 address, or for every interface, is a request this must
// not quietly override.
func TestIPv6IsLeftAloneWhenItIsWhatWasAskedFor(t *testing.T) {
	for _, listen := range []string{"[::1]:5900", "[::]:5900", ":5900"} {
		server := newTestServer(t, func(configuration *config.Configuration) {
			configuration.VNC.Listen = listen
		})
		if got := arguments(t, server); strings.Contains(got, "-no6") ||
			strings.Contains(got, "-rfbportv6") {
			t.Errorf("listening on %s passes -no6, which refuses the "+
				"address that was asked for: %s", listen, got)
		}
	}
}
