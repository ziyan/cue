// Package vncserver supervises x11vnc, which exports the running X display so
// that it can be watched and driven from somewhere else.
//
// The server itself is not written here on purpose. x11vnc's damage tracking
// and its encodings are tuned and correct, and a first attempt at the same
// thing in Go would be slower and buggier in ways nobody looking at the screen
// could excuse. What is written here is the part that has to be ours: the
// process's lifecycle, and — in internal/web — an authenticated WebSocket
// bridge so that a browser can be the viewer.
package vncserver

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/supervise"
)

var log = logging.MustGetLogger("vncserver")

// Server owns the x11vnc process.
//
// It reads the configuration through the store rather than holding a snapshot:
// the password file is written before every start, and a snapshot taken when
// the daemon started would be stale by then.
type Server struct {
	store *config.Store

	displayName       string
	authorityFilename string
	passwordFilename  string
}

// New returns a VNC server for the given configuration.
func New(store *config.Store, displayName, authorityFilename string) *Server {
	return &Server{
		store:             store,
		displayName:       displayName,
		authorityFilename: authorityFilename,
		passwordFilename:  filepath.Join(store.Current().Paths.Runtime, "vncpasswd"),
	}
}

// configuration is the settings in force right now.
func (self *Server) configuration() *config.Configuration {
	return self.store.Current()
}

// Address is where the server listens, which the web interface's bridge dials.
func (self *Server) Address() string {
	return self.configuration().VNC.Listen
}

// Settings builds the supervisor settings for x11vnc.
func (self *Server) Settings() *supervise.Settings {
	return &supervise.Settings{
		Name: "x11vnc",
		Path: "x11vnc",
		// Built before every start rather than once: the listen address, the
		// password and whether viewers may type are all on this command line
		// and all editable from the web interface.
		BuildArguments: self.arguments,
		Restart:        true,
		BeforeStart:    self.prepare,
		Ready:          self.probe,
		ReadyTimeout:   30 * time.Second,
		CaptureOutput:  true,
		OutputLevel:    logging.DEBUG,
		Environment: supervise.Environ(supervise.Inherit(), map[string]string{
			"DISPLAY":    self.displayName,
			"XAUTHORITY": self.authorityFilename,
		}),
	}
}

func (self *Server) arguments() []string {
	settings := self.configuration().VNC

	host, port, err := net.SplitHostPort(settings.Listen)
	if err != nil {
		host, port = "127.0.0.1", "5900"
	}

	arguments := []string{
		"-display", self.displayName,
		"-auth", self.authorityFilename,
		// Without -forever the server exits when the last viewer disconnects,
		// and the supervisor would spend the rest of the day restarting it.
		"-forever",
		// Several people watching the same screen at once is the normal case
		// when something has gone wrong with it.
		"-shared",
		"-rfbport", port,
		"-listen", host,
		// x11vnc logs every client connection, every screen change and a
		// paragraph of advice at its default verbosity.
		"-q",
		// Nothing should be able to reconfigure or shut down the server from
		// the viewer side.
		"-noremote",
		// Work out modifiers through the XKEYBOARD extension rather than by
		// guessing at the keymap. Somebody connecting to a screen is usually
		// doing it to type a password into a dashboard that logged itself
		// out, from a keyboard laid out differently to the one this X server
		// thinks it has; without this the shifted characters in that password
		// arrive as something else, and all they see is that it was refused.
		"-xkb",
	}

	if !self.configuration().Display.Cursor {
		// The X server is started with no cursor, so there is nothing to
		// send; drawing one for the viewer alone is confusing, because it
		// does not appear on the screen in the room.
		arguments = append(arguments, "-nocursor")
	}
	if settings.ViewOnly {
		arguments = append(arguments, "-viewonly")
	}
	if settings.Password.IsSet() {
		// -passwdfile takes the password as the first line of a plain file.
		// The alternative, -rfbauth, wants VNC's own obfuscated format, which
		// is a fixed-key DES encryption that protects nothing and would have
		// to be reimplemented here to no benefit.
		arguments = append(arguments, "-passwdfile", self.passwordFilename)
	} else {
		// Without this x11vnc refuses to start, which is the right default
		// for a general-purpose tool; here the listener is on the loopback
		// address and the web interface authenticates in front of it.
		arguments = append(arguments, "-nopw")
	}
	return arguments
}

// prepare writes the password file, when there is a password.
func (self *Server) prepare(ctx context.Context) error {
	settings := self.configuration().VNC

	if err := os.MkdirAll(filepath.Dir(self.passwordFilename), 0o755); err != nil {
		return fmt.Errorf("vncserver: create %s: %w", filepath.Dir(self.passwordFilename), err)
	}

	if !settings.Password.IsSet() {
		if err := os.Remove(self.passwordFilename); err != nil && !os.IsNotExist(err) {
			log.Warningf("cannot remove the old VNC password file: %s", err)
		}
		if isExposed(settings.Listen) {
			// Said loudly, and every time, because the consequence is that
			// anybody who can reach this address can watch and drive the
			// screen.
			log.Warningf("the VNC server is listening on %s with no password; anybody who can reach it can control this screen", settings.Listen)
		}
		return nil
	}

	if err := os.WriteFile(self.passwordFilename, []byte(settings.Password.Reveal()+"\n"), 0o600); err != nil {
		return fmt.Errorf("vncserver: write %s: %w", self.passwordFilename, err)
	}
	return nil
}

// probe is the readiness check: the port accepts a connection.
func (self *Server) probe(ctx context.Context) error {
	dialer := net.Dialer{}
	address := self.configuration().VNC.Listen
	if host, port, err := net.SplitHostPort(address); err == nil && (host == "" || host == "0.0.0.0") {
		// Dialling 0.0.0.0 does not mean anything; the loopback address
		// reaches a server bound to everything.
		address = net.JoinHostPort("127.0.0.1", port)
	}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("vncserver: not listening on %s yet: %w", address, err)
	}
	return connection.Close()
}

// isExposed reports whether a listen address is reachable from outside this
// machine.
func isExposed(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return true
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && !parsed.IsLoopback()
}
