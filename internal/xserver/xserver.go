// Package xserver starts and stops the X server, and knows the handful of
// small, unobvious things that have to be true before one will start: the
// socket directory has to exist, a lock file left by a server that was killed
// has to be cleared, and an authority file has to be written that both the
// server and everything the daemon runs can read.
//
// Two servers are supported. Xorg drives real graphics hardware and is what a
// device runs. Xvfb draws into memory and is what a developer's machine and
// the continuous integration smoke test run, which is what makes it possible
// to exercise the whole daemon — browser, VNC, web interface and all — on a
// machine with no screen.
package xserver

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/display"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/util/xauth"
)

var log = logging.MustGetLogger("xserver")

// Server owns the X server process and the files it needs.
type Server struct {
	settings *config.Configuration

	// cookie authenticates every connection to this server, including the
	// daemon's own. It is generated once per daemon run rather than per
	// server start, so that a restart of the X server does not invalidate the
	// authority file the browser was started with.
	cookie xauth.Cookie

	authorityFilename string
	logFilename       string
	configDirectory   string
}

// New prepares a server for the given configuration. Nothing is started and
// nothing is written until Prepare.
func New(settings *config.Configuration) (*Server, error) {
	cookie, err := xauth.NewCookie()
	if err != nil {
		return nil, err
	}
	runtime := settings.Paths.Runtime
	return &Server{
		settings:          settings,
		cookie:            cookie,
		authorityFilename: filepath.Join(runtime, "Xauthority"),
		logFilename:       filepath.Join(runtime, "xorg.log"),
		configDirectory:   filepath.Join(runtime, "xorg.conf.d"),
	}, nil
}

// AuthorityFilename is the file the browser and the VNC server point
// XAUTHORITY at.
func (self *Server) AuthorityFilename() string {
	return self.authorityFilename
}

// Cookie is the shared secret, for the daemon's own X connections.
func (self *Server) Cookie() xauth.Cookie {
	return self.cookie
}

// DisplayName is the value of DISPLAY that reaches this server.
func (self *Server) DisplayName() string {
	return display.Name(self.settings.Display.Number)
}

// LogFilename is where the X server writes its own log. The daemon reads the
// tail of it when the server fails to start, because that file is where the
// reason always is.
func (self *Server) LogFilename() string {
	return self.logFilename
}

// Prepare writes everything the server needs before it is started. It runs
// again before every restart, so a file removed by hand comes back.
func (self *Server) Prepare(ctx context.Context) error {
	if err := os.MkdirAll(self.settings.Paths.Runtime, 0o755); err != nil {
		return fmt.Errorf("xserver: create %s: %w", self.settings.Paths.Runtime, err)
	}

	// Every X server since the 1980s puts its socket here, and the browser
	// and the VNC server find it by that path alone. 1777 because more than
	// one account connects to it.
	if err := os.MkdirAll(socketDirectory, 0o1777); err != nil {
		return fmt.Errorf("xserver: create %s: %w", socketDirectory, err)
	}
	_ = os.Chmod(socketDirectory, 0o1777)

	if err := xauth.Write(self.authorityFilename, self.settings.Display.Number, self.cookie); err != nil {
		return err
	}
	// The browser runs as another account and has to read this file. Making
	// it readable by that account's group is the smallest thing that works;
	// the alternative — one authority file per account — means the daemon
	// has to rewrite them all whenever the cookie changes.
	if err := self.shareAuthorityWithBrowser(); err != nil {
		log.Warningf("%s", err)
	}

	if err := self.clearStaleLock(); err != nil {
		return err
	}

	return self.writeConfiguration()
}

// Settings builds the supervisor settings for the server process.
func (self *Server) Settings() *supervise.Settings {
	settings := &supervise.Settings{
		Name:        self.settings.Display.Server,
		Restart:     true,
		BeforeStart: self.Prepare,
		Ready:       self.probe,
		// A server that has not answered in fifteen seconds is not going to.
		ReadyTimeout:  15 * time.Second,
		ReadyInterval: 200 * time.Millisecond,
		CaptureOutput: true,
		// The X server writes almost nothing to standard error; the useful
		// output is in its own log, which the daemon reads separately.
		OutputLevel: logging.INFO,
		// SIGTERM lets the server hand the graphics hardware back. Killing it
		// outright leaves the console blank and the next server unable to
		// take the device, which on a machine nobody can reach means a
		// power cycle.
		StopTimeout: 10 * time.Second,
		Environment: supervise.Environ(supervise.Inherit(), map[string]string{
			"XAUTHORITY": self.authorityFilename,
			"DISPLAY":    self.DisplayName(),
		}),
	}

	switch self.settings.Display.Server {
	case config.ServerXvfb:
		settings.Path = "Xvfb"
		settings.Arguments = self.virtualArguments()
	default:
		settings.Path = "Xorg"
		settings.Arguments = self.hardwareArguments()
	}
	return settings
}

// hardwareArguments builds the Xorg command line.
func (self *Server) hardwareArguments() []string {
	arguments := []string{
		self.DisplayName(),
		"-auth", self.authorityFilename,
		"-configdir", self.configDirectory,
		"-logfile", self.logFilename,
		// The X protocol over TCP has no place on a device like this; every
		// client is on the same machine and reaches it over the Unix socket.
		"-nolisten", "tcp",
		// Without this the server resets — throwing away every client — the
		// moment the last one disconnects, so a browser crash would take the
		// server with it and the screen would flash black.
		"-noreset",
		// The daemon has no controlling terminal, being process 1 of a
		// container. Without -keeptty the server tries to take one over and
		// fails in a way whose message ("xf86OpenConsole: Cannot open
		// virtual console") sends everybody looking in the wrong place.
		"-keeptty",
		"-verbose", "3",
	}
	if !self.settings.Display.Cursor {
		arguments = append(arguments, "-nocursor")
	}
	arguments = append(arguments, self.settings.Display.ExtraArguments...)
	return arguments
}

// virtualArguments builds the Xvfb command line. Xvfb has a fixed screen size
// decided when it starts, so the configured framebuffer — or a sensible
// default — is baked in here rather than set over RandR afterwards.
func (self *Server) virtualArguments() []string {
	size := self.settings.Display.Framebuffer
	if size == "" {
		size = "1280x720"
	}
	arguments := []string{
		self.DisplayName(),
		"-auth", self.authorityFilename,
		"-screen", "0", size + "x24",
		"-nolisten", "tcp",
		"-noreset",
		// Without RANDR the daemon cannot ask what the screen looks like, and
		// half of what it does would have to be written twice.
		"+extension", "RANDR",
		"+extension", "GLX",
	}
	arguments = append(arguments, self.settings.Display.ExtraArguments...)
	return arguments
}

// probe is the readiness check: connect as an ordinary client and ask a
// question. Anything less — the socket existing, the process running — is
// true a few hundred milliseconds before the server will actually answer, and
// starting the browser in that window is how a kiosk ends up showing nothing.
func (self *Server) probe(ctx context.Context) error {
	connection, err := display.Open(self.settings.Display.Number, self.cookie)
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Ping()
}

// clearStaleLock removes the lock file and socket of a server that is no
// longer running. Xorg refuses to start when the lock file exists, and a
// device that lost power mid-frame has one every time it boots — which
// presents as a screen that is black until somebody logs in and deletes a
// file they have never heard of.
func (self *Server) clearStaleLock() error {
	lock := fmt.Sprintf("/tmp/.X%d-lock", self.settings.Display.Number)
	socket := display.SocketPath(self.settings.Display.Number)

	if _, err := os.Stat(lock); err != nil {
		return nil
	}

	// If something answers, the lock is not stale and starting a second
	// server would be a mistake.
	if err := self.probe(context.Background()); err == nil {
		return fmt.Errorf("xserver: an X server is already running on %s", self.DisplayName())
	}

	log.Noticef("removing the lock left by a previous X server: %s", lock)
	if err := os.Remove(lock); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("xserver: remove %s: %w", lock, err)
	}
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		log.Warningf("cannot remove the stale socket %s: %s", socket, err)
	}
	return nil
}

// writeConfiguration writes the Xorg configuration fragments. There is only
// ever one, and it is whatever the operator supplied: this daemon deliberately
// generates no device or monitor sections, because the modesetting driver
// works out what is attached better than a generated file can, and every
// generated xorg.conf in this author's experience has eventually been the
// reason a screen stayed black.
func (self *Server) writeConfiguration() error {
	if err := os.MkdirAll(self.configDirectory, 0o755); err != nil {
		return fmt.Errorf("xserver: create %s: %w", self.configDirectory, err)
	}

	filename := filepath.Join(self.configDirectory, "10-cue.conf")
	if strings.TrimSpace(self.settings.Display.XorgConfiguration) == "" {
		if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("xserver: remove %s: %w", filename, err)
		}
		return nil
	}

	content := "# Written by cue from display.xorgConfiguration. Edit the\n" +
		"# configuration file, not this one: it is rewritten on every start.\n\n" +
		self.settings.Display.XorgConfiguration + "\n"
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		return fmt.Errorf("xserver: write %s: %w", filename, err)
	}
	return nil
}

// LogTail returns the last few lines of the X server's own log, which is
// where the reason it would not start always is.
func (self *Server) LogTail(lines int) string {
	content, err := os.ReadFile(self.logFilename)
	if err != nil {
		return ""
	}
	all := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

// shareAuthorityWithBrowser gives the account the browser runs as read access
// to the authority file, by setting the file's group to that account's group.
// The alternative — one authority file per account — means every one of them
// has to be rewritten whenever the cookie changes.
func (self *Server) shareAuthorityWithBrowser() error {
	name := self.settings.Browser.User
	if name == "" {
		return nil
	}
	account, err := user.Lookup(name)
	if err != nil {
		return fmt.Errorf("xserver: no account named %q, so the browser may not be able to read %s: %w",
			name, self.authorityFilename, err)
	}
	groupId, err := strconv.Atoi(account.Gid)
	if err != nil {
		return fmt.Errorf("xserver: account %q has a group id that is not a number: %w", name, err)
	}
	if err := os.Chown(self.authorityFilename, -1, groupId); err != nil {
		return fmt.Errorf("xserver: cannot give %s to the group of %q: %w", self.authorityFilename, name, err)
	}
	return nil
}

const socketDirectory = "/tmp/.X11-unix"
