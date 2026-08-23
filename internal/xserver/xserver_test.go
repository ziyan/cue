package xserver

import (
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
	server, err := New(store)
	if err != nil {
		t.Fatalf("new: %s", err)
	}
	return server
}

func arguments(t *testing.T, server *Server) string {
	t.Helper()
	return strings.Join(server.Settings().CommandLine(), " ")
}

func TestTheXServerIsToldTheConsoleToDrawOn(t *testing.T) {
	// Getting this wrong is a black screen and a message naming neither the
	// setting nor the container. It has to match the device the container is
	// given; see deploy/docker-compose.yml.
	server := newTestServer(t, func(configuration *config.Configuration) {
		configuration.Display.VirtualTerminal = 4
	})
	if line := arguments(t, server); !strings.Contains(line, "vt4") {
		t.Errorf("the console is not on the command line:\n%s", line)
	}
}

func TestConsoleZeroLetsTheServerChoose(t *testing.T) {
	server := newTestServer(t, func(configuration *config.Configuration) {
		configuration.Display.VirtualTerminal = 0
	})
	line := arguments(t, server)
	if strings.Contains(line, " vt") {
		t.Errorf("a console was named although the configuration said to let the server choose:\n%s", line)
	}
}

func TestTheFlagsThatKeepAKioskAliveAreThere(t *testing.T) {
	server := newTestServer(t, nil)
	line := arguments(t, server)

	for flag, why := range map[string]string{
		"-noreset":  "without it the server throws away every client when the last one disconnects, so a browser crash takes the screen with it",
		"-keeptty":  "the daemon is process 1 of a container and has no controlling terminal; without this the server fails in a way that names neither",
		"-nolisten": "the X protocol over TCP has no place on a device like this",
		"-nocursor": "a kiosk with a pointer parked in the middle of it looks broken",
	} {
		if !strings.Contains(line, flag) {
			t.Errorf("%s is missing: %s\n%s", flag, why, line)
		}
	}
}

func TestTheCursorCanBeAskedFor(t *testing.T) {
	server := newTestServer(t, func(configuration *config.Configuration) {
		configuration.Display.Cursor = true
	})
	if line := arguments(t, server); strings.Contains(line, "-nocursor") {
		t.Errorf("the cursor was hidden although it was asked for:\n%s", line)
	}
}

func TestTheConfigurationDirectoryIsOnlyNamedWhenThereIsOne(t *testing.T) {
	// Xorg logs "(EE) Unable to locate/open config directory" for a directory
	// with no .conf files in it. A spurious error in the log of a machine
	// whose screen is black is worse than useless.
	empty := newTestServer(t, nil)
	if line := arguments(t, empty); strings.Contains(line, "-configdir") {
		t.Errorf("the configuration directory was named although there is no configuration:\n%s", line)
	}

	supplied := newTestServer(t, func(configuration *config.Configuration) {
		configuration.Display.XorgConfiguration = `Section "Device"
EndSection`
	})
	if line := arguments(t, supplied); !strings.Contains(line, "-configdir") {
		t.Errorf("the configuration directory was not named although there is a configuration:\n%s", line)
	}
}

func TestTheAuthorityFileIsSharedByEveryoneWhoNeedsIt(t *testing.T) {
	// The browser and the VNC server both connect to this server, and both
	// are told where the cookie is. If the server were given a different file
	// than they are, everything would fail to authenticate.
	server := newTestServer(t, nil)
	line := arguments(t, server)
	if !strings.Contains(line, server.AuthorityFilename()) {
		t.Errorf("the server is not given the authority file the clients are told about:\n%s", line)
	}
}

func TestTheVirtualServerGetsASizeAndTheExtensionsTheDaemonNeeds(t *testing.T) {
	server := newTestServer(t, func(configuration *config.Configuration) {
		configuration.Display.Server = config.ServerXvfb
		configuration.Display.Framebuffer = "1600x900"
	})
	line := arguments(t, server)

	if !strings.Contains(line, "1600x900x24") {
		t.Errorf("the configured size did not reach the virtual server:\n%s", line)
	}
	// Without RANDR the daemon cannot ask what the screen looks like, and half
	// of what it does would have to be written twice.
	if !strings.Contains(line, "RANDR") {
		t.Errorf("RANDR was not asked for:\n%s", line)
	}
}

func TestTheVirtualServerHasASizeEvenWhenNoneIsConfigured(t *testing.T) {
	server := newTestServer(t, func(configuration *config.Configuration) {
		configuration.Display.Server = config.ServerXvfb
		configuration.Display.Framebuffer = ""
	})
	if line := arguments(t, server); !strings.Contains(line, "-screen") {
		t.Errorf("the virtual server was given no screen at all:\n%s", line)
	}
}

func TestExtraArgumentsComeLastSoTheyCanOverride(t *testing.T) {
	server := newTestServer(t, func(configuration *config.Configuration) {
		configuration.Display.ExtraArguments = []string{"-novtswitch"}
	})
	list := server.Settings().CommandLine()
	if list[len(list)-1] != "-novtswitch" {
		t.Errorf("the extra argument is not last: %v", list)
	}
}

func TestTheDisplayNumberReachesTheNameAndTheCommandLine(t *testing.T) {
	server := newTestServer(t, func(configuration *config.Configuration) {
		configuration.Display.Number = 5
	})
	if server.DisplayName() != ":5" {
		t.Errorf("the display is called %q, want :5", server.DisplayName())
	}
	if line := arguments(t, server); !strings.Contains(line, ":5") {
		t.Errorf("the display number is not on the command line:\n%s", line)
	}
}
