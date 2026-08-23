// Package web is the interface an operator uses: an HTTP server that reports
// what the device is doing, lets its content be configured, and carries a
// live view of the screen to a browser tab.
//
// It is also, deliberately, the only way in. The VNC server listens on the
// loopback address and this is what bridges it to the outside, so that
// watching the screen needs the same password as changing what is on it.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/browser"
	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/hardware"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/watchdog"
	"github.com/ziyan/cue/internal/xserver"
)

var log = logging.MustGetLogger("web")

//go:embed all:static
var staticFiles embed.FS

// Device is what the web interface needs from the rest of the daemon. It is
// an interface so that this package can be tested without starting an X
// server, and so that the dependency points one way: the daemon knows about
// the interface, the interface knows nothing about the daemon.
type Device interface {
	// Statuses is what every supervised program is doing.
	Statuses() []supervise.Status

	// Browser is the running browser.
	Browser() *browser.Browser

	// Watchdog is the running watchdog.
	Watchdog() *watchdog.Watchdog

	// VNCAddress is where the VNC server listens, for the bridge.
	VNCAddress() string

	// StartedAt is when the daemon came up.
	StartedAt() time.Time

	// XServer is the X server, for the display report and its log.
	XServer() *xserver.Server

	// Restart restarts one supervised program by name.
	Restart(ctx context.Context, name string) error
}

// Server is the HTTP server.
type Server struct {
	store   *config.Store
	device  Device
	metrics *hardware.Collector

	router   *mux.Router
	listener net.Listener
	server   *http.Server
}

// New builds the server. Nothing listens until Start.
func New(store *config.Store, device Device) *Server {
	configuration := store.Current()

	self := &Server{
		store:   store,
		device:  device,
		metrics: hardware.NewCollector("/", configuration.Paths.State),
		router:  mux.NewRouter(),
	}
	self.addRoutes()
	return self
}

// Start begins listening. It returns once the listener is open, so that a
// caller can report the address before anything has connected.
func (self *Server) Start(ctx context.Context) error {
	address := self.store.Current().Web.Listen

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("web: cannot listen on %s: %w", address, err)
	}
	self.listener = listener

	self.server = &http.Server{
		Handler: self.router,
		// A screenshot of a 4K screen takes a moment to encode, and a VNC
		// connection lasts as long as somebody is watching, so the write
		// timeout is left off and the read one is generous.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		if err := self.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Errorf("the web interface stopped: %s", err)
		}
	}()

	log.Noticef("the web interface is on http://%s/", describeAddress(listener.Addr()))
	if self.store.Current().Web.PasswordHash == "" {
		log.Noticef("this device has not been set up yet; open that address to finish")
	}
	return nil
}

// Stop shuts the server down.
func (self *Server) Stop(ctx context.Context) {
	if self.server == nil {
		return
	}
	if err := self.server.Shutdown(ctx); err != nil {
		log.Warningf("the web interface did not shut down cleanly: %s", err)
	}
}

// Address is where the server is listening, once it has started.
func (self *Server) Address() string {
	if self.listener == nil {
		return self.store.Current().Web.Listen
	}
	return self.listener.Addr().String()
}

func (self *Server) addRoutes() {
	// Health first and without authentication: it is what a container
	// orchestrator and a monitoring system ask, and neither of them has a
	// password.
	self.router.Path("/healthz").Methods(http.MethodGet).HandlerFunc(self.health)

	// The page shown on the screen itself when there is no playlist. It is
	// served without authentication because the only thing that can reach it
	// is the browser on this machine, and its whole job is to tell somebody
	// standing in front of the screen where to go.
	self.router.Path("/welcome").Methods(http.MethodGet).HandlerFunc(self.welcome)

	api := self.router.PathPrefix("/api/v1").Subrouter()

	// Setting up and signing in are the two things that must work before
	// there is a session.
	api.Path("/setup").Methods(http.MethodGet).HandlerFunc(self.setupState)
	api.Path("/setup").Methods(http.MethodPost).HandlerFunc(self.setup)
	api.Path("/session").Methods(http.MethodPost).HandlerFunc(self.signIn)
	api.Path("/session").Methods(http.MethodDelete).HandlerFunc(self.signOut)

	guarded := api.NewRoute().Subrouter()
	guarded.Use(self.requireSession)

	guarded.Path("/status").Methods(http.MethodGet).HandlerFunc(self.status)
	guarded.Path("/configuration").Methods(http.MethodGet).HandlerFunc(self.readConfiguration)
	guarded.Path("/configuration").Methods(http.MethodPut).HandlerFunc(self.writeConfiguration)
	guarded.Path("/screenshot.png").Methods(http.MethodGet).HandlerFunc(self.screenshot)
	guarded.Path("/show/{item}").Methods(http.MethodPost).HandlerFunc(self.show)
	guarded.Path("/navigate").Methods(http.MethodPost).HandlerFunc(self.navigate)
	guarded.Path("/restart/{program}").Methods(http.MethodPost).HandlerFunc(self.restart)
	guarded.Path("/logs/xorg").Methods(http.MethodGet).HandlerFunc(self.xorgLog)
	guarded.Path("/vnc").Methods(http.MethodGet).HandlerFunc(self.vnc)

	// Everything else is the interface itself.
	self.router.PathPrefix("/").Methods(http.MethodGet).HandlerFunc(self.static)
}

// static serves the embedded interface, falling back to the single page for
// any path the interface routes itself.
func (self *Server) static(response http.ResponseWriter, request *http.Request) {
	content, err := fs.Sub(staticFiles, "static")
	if err != nil {
		http.Error(response, "the interface is missing from this build", http.StatusInternalServerError)
		return
	}

	path := request.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	file, err := content.Open(path[1:])
	if err != nil {
		// A path the interface handles itself: serve the shell and let it
		// route. A reload of /content must not be a 404.
		request.URL.Path = "/"
		http.FileServerFS(content).ServeHTTP(response, request)
		return
	}
	_ = file.Close()

	http.FileServerFS(content).ServeHTTP(response, request)
}

func describeAddress(address net.Addr) string {
	text := address.String()
	host, port, err := net.SplitHostPort(text)
	if err != nil {
		return text
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		// Reporting "0.0.0.0:8080" as the address to open is unhelpful; every
		// address of the machine reaches it, and the one somebody will
		// actually type is the machine's own.
		if best := primaryAddress(); best != "" {
			return net.JoinHostPort(best, port)
		}
		return net.JoinHostPort("127.0.0.1", port)
	}
	return text
}

// primaryAddress is the machine's own address on the network, worked out by
// asking the routing table which one would be used to reach the outside. No
// packet is sent: a UDP socket is connected, which only chooses a route.
func primaryAddress() string {
	connection, err := net.Dial("udp", "192.0.2.1:9")
	if err != nil {
		return ""
	}
	defer func() { _ = connection.Close() }()
	host, _, err := net.SplitHostPort(connection.LocalAddr().String())
	if err != nil {
		return ""
	}
	return host
}
