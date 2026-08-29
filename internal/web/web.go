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
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/browser"
	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/hardware"
	"github.com/ziyan/cue/internal/media"
	"github.com/ziyan/cue/internal/network"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/timesync"
	"github.com/ziyan/cue/internal/upgrade"
	"github.com/ziyan/cue/internal/watchdog"
	"github.com/ziyan/cue/internal/xserver"
)

var log = logging.MustGetLogger("web")

// The interface: React and MUI from web/, built by Vite. It is embedded, so
// the daemon ships as one executable with nothing to fetch at runtime.
//
// Built by `make web`, which writes it here because Go's embed cannot reach
// outside the directory of the package that declares it. A checkout that has
// not run it has an empty directory and says so at runtime rather than
// failing to compile, which is what the .gitkeep is for.
//
//go:embed all:dist
var builtFiles embed.FS

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

	// TimeSync is the time client, for the clock report.
	TimeSync() *timesync.Client

	// SetupNetwork is the temporary wireless network this device is running
	// so that somebody can set it up from a phone, and whether it is running
	// at all. When it is, the welcome page puts its credentials in the QR
	// code on the screen instead of a web address: that passphrase exists
	// nowhere else, which is what makes being able to set this device up the
	// same as being able to see its screen.
	SetupNetwork() (network.Credentials, bool)

	// SetupNetworks is what the radio saw before it became an access point,
	// which is the list the setup portal offers. It is a remembered scan and
	// not a fresh one: the radio cannot search every channel while it is busy
	// advertising on one.
	SetupNetworks() []network.WirelessNetwork

	// The network, set up from the screen itself. This is the one kind of
	// configuration the menu offers, because it is the one that cannot be
	// done from the web interface: reaching the web interface is what it is
	// for.
	InterfacesForSetup() []network.Interface
	ScanForNetworks(interfaceName string) ([]network.WirelessNetwork, error)
	JoinWireless(interfaceName, ssid, passphrase string) error
	ConfigureWired(settings config.Interface) error

	// ForgetWireless takes this device off the network it was told to join and
	// starts the setup network, so that its screen shows the code again. It is
	// what the control on the screen does.
	ForgetWireless() error

	// SetupTrouble is what to tell somebody about the last attempt to join,
	// or empty. A mistyped passphrase has to be explained on the page they
	// come back to, or they will try the same thing again.
	SetupTrouble() string

	// JoinFromSetup leaves the setup network and joins the one chosen on the
	// portal. It returns as soon as the attempt has started, because the
	// phone that asked is about to lose the network it asked over.
	JoinFromSetup(ssid, passphrase string) error

	// RescanFromSetup looks again for networks in range.
	RescanFromSetup() error

	// Network is the machine's own network, for the Network page.
	Network() *network.Manager

	// Restart restarts one supervised program by name.
	Restart(ctx context.Context, name string) error
}

// Server is the HTTP server.
type Server struct {
	store   *config.Store
	device  Device
	metrics *hardware.Collector

	uploads *media.Store

	// The authority pages shown on this device's own screen carry, which
	// lasts as long as the page does. See pass.go.
	passes *passes

	// What is known about newer releases. Nil on a daemon built without one,
	// and the Upgrade page says so rather than pretending to be up to date.
	upgrades *upgrade.Checker

	// What an upgrade is doing, if one is. Two at once is not a slow upgrade
	// but a dead device: see applyUpgrade. Kept rather than merely counted so
	// that the page can say what is happening -- an upgrade takes minutes, and
	// a page that shows the button again while one is running invites somebody
	// to press it a second time.
	upgradeMutex    sync.Mutex
	upgradeProgress upgradeProgress

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
		passes:  newPasses(),
		router:  mux.NewRouter(),
	}
	self.addRoutes()
	return self
}

// WithUpgrades gives the server what knows whether a newer release exists.
// Without one the Upgrade page says this daemon is not checking, which is
// honest; the alternative is a page that looks like "you are up to date".
func (self *Server) WithUpgrades(checker *upgrade.Checker) *Server {
	self.upgrades = checker
	return self
}

// WithUploads gives the server the store uploaded pictures and videos live
// in. Without one it refuses uploads and serves no media, which is what a
// daemon that could not create the directory should do.
func (self *Server) WithUploads(store *media.Store) *Server {
	self.uploads = store
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

	// The setup portal, and the probes that make a phone open it. All of them
	// answer 404 unless this device is actually being set up, so that a device
	// in normal use serves nothing here. They are registered once rather than
	// added and removed as setup starts and stops: a router cannot be changed
	// safely while it is serving, and a guard cannot get out of step with the
	// state it guards.
	// The player page and the videos themselves. The browser on this device
	// has no session and never will, so these let it through and ask everybody
	// else for one.
	self.router.Path("/play/{item}").Methods(http.MethodGet).HandlerFunc(self.localOrSession(self.play))
	self.router.Path("/media/{file}").Methods(http.MethodGet, http.MethodHead).HandlerFunc(self.localOrSession(self.serveMedia))

	self.router.Path("/portal").Methods(http.MethodGet).HandlerFunc(self.onboardingOrNotFound(self.portal))
	for _, probe := range captiveProbePaths {
		self.router.Path(probe).Methods(http.MethodGet).HandlerFunc(self.onboardingOrNotFound(self.captiveProbe))
	}

	api := self.router.PathPrefix("/api/v1").Subrouter()

	// Setting up and signing in are the two things that must work before
	// there is a session.
	api.Path("/setup").Methods(http.MethodGet).HandlerFunc(self.setupState)
	api.Path("/setup").Methods(http.MethodPost).HandlerFunc(self.setup)
	api.Path("/session").Methods(http.MethodPost).HandlerFunc(self.signIn)
	api.Path("/session").Methods(http.MethodDelete).HandlerFunc(self.signOut)

	// The three things a page on this device's own screen does with its pass.
	// They cannot sit behind a session, because they are how a screen with
	// nobody signed in at it proves who is standing there. Each one checks
	// the pass itself.
	api.Path("/screen/unlock").Methods(http.MethodPost).HandlerFunc(self.screenUnlock)
	api.Path("/screen/password").Methods(http.MethodPost).HandlerFunc(self.screenChooseWord)
	api.Path("/screen/close").Methods(http.MethodPost).HandlerFunc(self.screenClose)

	// Setting the device up over the air happens before there is a session,
	// for the same reason setup and sign-in do.
	// Setting up from a phone is gated the same way. A device out of its box
	// has no password and anybody holding the code may set it up; a device
	// that has one asks for it before it will join anything, because losing a
	// network is not the same as losing ownership.
	api.Path("/portal/join").Methods(http.MethodPost).HandlerFunc(self.onboardingOrNotFound(self.portalAction(self.portalJoin)))
	api.Path("/portal/scan").Methods(http.MethodPost).HandlerFunc(self.onboardingOrNotFound(self.portalAction(self.portalScan)))

	guarded := api.NewRoute().Subrouter()
	guarded.Use(self.requireSession)
	guarded.Path("/upgrade").Methods(http.MethodGet).HandlerFunc(self.upgradeState)
	guarded.Path("/upgrade").Methods(http.MethodPost).HandlerFunc(self.applyUpgrade)

	guarded.Path("/status").Methods(http.MethodGet).HandlerFunc(self.status)
	guarded.Path("/timezones").Methods(http.MethodGet).HandlerFunc(self.timezones)
	guarded.Path("/configuration").Methods(http.MethodGet).HandlerFunc(self.readConfiguration)
	guarded.Path("/configuration").Methods(http.MethodPut).HandlerFunc(self.writeConfiguration)
	guarded.Path("/screenshot.png").Methods(http.MethodGet).HandlerFunc(self.screenshot)
	guarded.Path("/show/{item}").Methods(http.MethodPost).HandlerFunc(self.show)
	guarded.Path("/navigate").Methods(http.MethodPost).HandlerFunc(self.navigate)
	guarded.Path("/restart/{program}").Methods(http.MethodPost).HandlerFunc(self.restart)
	guarded.Path("/logs/xorg").Methods(http.MethodGet).HandlerFunc(self.xorgLog)
	guarded.Path("/network").Methods(http.MethodGet).HandlerFunc(self.networkState)
	guarded.Path("/network/scan/{interface}").Methods(http.MethodPost).HandlerFunc(self.scanWireless)
	guarded.Path("/vnc").Methods(http.MethodGet).HandlerFunc(self.vnc)
	guarded.Path("/media").Methods(http.MethodGet).HandlerFunc(self.listMedia)
	guarded.Path("/media").Methods(http.MethodPost).HandlerFunc(self.uploadMedia)

	// Moving to the next item is how the player says its video has ended, so
	// it has to be reachable by the browser on this device, which has no
	// session.
	api.Path("/playlist/next").Methods(http.MethodPost).HandlerFunc(self.localOrSession(self.showNext))

	// The way back, for somebody standing in front of the screen. Served to
	// this machine and refused to the network, like everything else the
	// screen's own browser asks for.
	api.Path("/wireless/reset").Methods(http.MethodPost).HandlerFunc(self.screenAction(self.resetWireless))

	// The menu somebody at the screen can open, and the few things it does.
	// All of them are actions; none of them changes a setting.
	self.router.Path("/menu").Methods(http.MethodGet).HandlerFunc(self.localOrSession(self.menu))
	self.router.Path("/upgrading").Methods(http.MethodGet).HandlerFunc(self.localOrSession(self.upgrading))
	api.Path("/playlist/hold").Methods(http.MethodPost).HandlerFunc(self.localOrSession(self.holdPlaylist))
	api.Path("/playlist/release").Methods(http.MethodPost).HandlerFunc(self.localOrSession(self.holdPlaylist))
	api.Path("/playlist/keep").Methods(http.MethodPost).HandlerFunc(self.localOrSession(self.holdPlaylist))
	api.Path("/playlist/refresh").Methods(http.MethodPost).HandlerFunc(self.localOrSession(self.holdPlaylist))
	api.Path("/playlist/back").Methods(http.MethodPost).HandlerFunc(self.localOrSession(self.holdPlaylist))
	api.Path("/menu/reload").Methods(http.MethodPost).HandlerFunc(self.screenAction(self.menuReload))
	api.Path("/menu/restart/{program}").Methods(http.MethodPost).HandlerFunc(self.screenAction(self.menuRestart))
	api.Path("/menu/network").Methods(http.MethodGet).HandlerFunc(self.localOrSession(self.menuNetwork))
	api.Path("/menu/network/scan").Methods(http.MethodPost).HandlerFunc(self.screenAction(self.menuScan))
	api.Path("/menu/network/wireless").Methods(http.MethodPost).HandlerFunc(self.screenAction(self.menuJoinWireless))
	api.Path("/menu/network/wired").Methods(http.MethodPost).HandlerFunc(self.screenAction(self.menuConfigureWired))
	api.Path("/menu/language").Methods(http.MethodPost).HandlerFunc(self.screenAction(self.menuLanguage))
	api.Path("/menu/upgrade").Methods(http.MethodPost).HandlerFunc(self.screenAction(self.menuUpgrade))
	api.Path("/menu/display").Methods(http.MethodGet).HandlerFunc(self.screenAction(self.menuDisplay))
	api.Path("/menu/display").Methods(http.MethodPost).HandlerFunc(self.screenAction(self.menuSetDisplay))

	// Everything else is the interface itself.
	self.router.PathPrefix("/").Methods(http.MethodGet).HandlerFunc(self.built)
}

// static serves the embedded interface, falling back to the single page for
// any path the interface routes itself.
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

// built serves the bundle from web/.
//
// Anything it does not have is answered with index.html, because the routing
// is in the browser: /device is a page React knows about and not a file.
func (self *Server) built(response http.ResponseWriter, request *http.Request) {
	content, err := fs.Sub(builtFiles, "dist")
	if err != nil {
		http.Error(response, "the interface is missing from this build", http.StatusInternalServerError)
		return
	}

	path := strings.TrimPrefix(request.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	if file, err := content.Open(path); err == nil {
		_ = file.Close()
		http.FileServerFS(content).ServeHTTP(response, request)
		return
	}

	// Only a path that could be a page falls through to the shell. A request
	// for something with an extension is asking for a file, and answering
	// that with HTML means a missing script gets a page instead of a 404 --
	// which the browser then fails to parse, somewhere far away from the
	// missing file.
	if strings.Contains(strings.TrimPrefix(path, "assets/"), ".") {
		http.NotFound(response, request)
		return
	}

	shell, err := content.Open("index.html")
	if err != nil {
		http.Error(response, "this build has no interface in it; run make web", http.StatusNotFound)
		return
	}
	defer func() { _ = shell.Close() }()

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(response, shell)
}
