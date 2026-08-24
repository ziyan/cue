package web

import (
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"image/png"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/ziyan/cue/internal/audio"
	"github.com/ziyan/cue/internal/browser"
	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/display"
	"github.com/ziyan/cue/internal/fleet"
	"github.com/ziyan/cue/internal/hardware"
	"github.com/ziyan/cue/internal/input"
	"github.com/ziyan/cue/internal/network"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/timesync"
	"github.com/ziyan/cue/internal/util/drm"
	"github.com/ziyan/cue/internal/util/picture"
	"github.com/ziyan/cue/internal/util/security"
	"github.com/ziyan/cue/internal/version"
	"github.com/ziyan/cue/internal/watchdog"
)

// Status is everything the overview page shows, in one response, because a
// page that makes eight requests every three seconds is eight times as likely
// to show a mixture of two moments.
type Status struct {
	Device     DeviceStatus       `json:"device"`
	Programs   []supervise.Status `json:"programs"`
	Browser    browser.State      `json:"browser"`
	Watchdog   watchdog.State     `json:"watchdog"`
	Machine    hardware.Snapshot  `json:"machine"`
	Connectors []drm.Connector    `json:"connectors"`
	Outputs    []display.Output   `json:"outputs"`
	Screen     display.Screen     `json:"screen"`
	Clock      timesync.State     `json:"clock"`
	Sound      []audio.Device     `json:"sound"`
	Input      []input.Device     `json:"input"`
	Fleet      fleet.State        `json:"fleet"`
	Network    network.State      `json:"network"`

	// IgnoredSettings are names in the configuration file this version has no
	// setting for: a mistyped key, or a setting removed by an upgrade. They
	// are shown here because from in front of the screen a key that was
	// mistyped and a setting that does nothing look exactly the same.
	IgnoredSettings []string `json:"ignoredSettings,omitempty"`
}

// DeviceStatus is who this device is.
type DeviceStatus struct {
	Name       string    `json:"name"`
	Identifier string    `json:"identifier"`
	Location   string    `json:"location"`
	Version    string    `json:"version"`
	StartedAt  time.Time `json:"startedAt"`
	Uptime     string    `json:"uptime"`
	Timezone   string    `json:"timezone"`
	Now        time.Time `json:"now"`
}

// health is what a container orchestrator asks. It is deliberately generous
// about what counts as healthy: a display whose browser is restarting is
// having a bad minute, not a bad day, and a health check that fails during a
// planned restart would have the orchestrator kill the container in the
// middle of recovering.
func (self *Server) health(response http.ResponseWriter, request *http.Request) {
	healthy := false
	for _, status := range self.device.Statuses() {
		if status.Name != "chromium" {
			continue
		}
		healthy = status.State == supervise.StateRunning || status.State == supervise.StateStarting
	}

	if !healthy {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{
			"status": "the browser is not running",
		})
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (self *Server) status(response http.ResponseWriter, request *http.Request) {
	configuration := self.store.Current()

	status := Status{
		Device: DeviceStatus{
			Name:       configuration.Device.Name,
			Identifier: configuration.Device.Identifier,
			Location:   configuration.Device.Location,
			Version:    version.Version(),
			StartedAt:  self.device.StartedAt(),
			Uptime:     time.Since(self.device.StartedAt()).Round(time.Second).String(),
			Timezone:   configuration.Device.Timezone,
			Now:        time.Now(),
		},
		Programs: self.device.Statuses(),
		Watchdog: self.device.Watchdog().State(),
		Machine:  self.metrics.Collect(),
	}

	// Asking the browser what it is showing involves talking to it, so it is
	// given a short deadline of its own: a wedged browser must not make the
	// status page hang, since a wedged browser is exactly when somebody is
	// looking at it.
	browserContext, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	status.Browser = self.device.Browser().State(browserContext)

	if connectors, err := drm.Connectors(); err == nil {
		status.Connectors = connectors
	}
	if devices, err := audio.Devices(); err == nil {
		status.Sound = devices
	}
	if devices, err := input.Devices(); err == nil {
		status.Input = devices
	}

	clockContext, clockCancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer clockCancel()
	status.Clock = self.device.TimeSync().State(clockContext)

	if tunnel := self.device.Fleet(); tunnel != nil {
		status.Fleet = tunnel.State()
	}
	if manager := self.device.Network(); manager != nil {
		status.Network = manager.State()
	}
	status.IgnoredSettings = configuration.IgnoredSettings

	displayContext, displayCancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer displayCancel()
	if connection, err := display.Open(displayContext, configuration.Display.Number, self.device.XServer().Cookie()); err == nil {
		defer connection.Close()
		status.Screen = connection.Screen()
		if outputs, err := connection.Outputs(); err == nil {
			status.Outputs = outputs
		}
	}

	writeJSON(response, http.StatusOK, status)
}

// setupState tells the interface whether this device still needs setting up,
// so that a browser opening it for the first time knows to show the wizard.
func (self *Server) setupState(response http.ResponseWriter, request *http.Request) {
	configuration := self.store.Current()
	writeJSON(response, http.StatusOK, map[string]interface{}{
		"needsSetup": !self.isSetUp(),
		"signedIn":   self.hasSession(request),
		"device": map[string]string{
			"name":       configuration.Device.Name,
			"identifier": configuration.Device.Identifier,
		},
		"version": version.Version(),
	})
}

// setup finishes the first run: it names the device and sets the password.
// It works exactly once, because afterwards there is a password and this
// endpoint refuses.
func (self *Server) setup(response http.ResponseWriter, request *http.Request) {
	if self.isSetUp() {
		writeError(response, http.StatusConflict, "this device has already been set up")
		return
	}

	var body struct {
		Name     string `json:"name"`
		Location string `json:"location"`
		Timezone string `json:"timezone"`
		Password string `json:"password"`
	}
	if err := decode(request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if len(body.Password) < 8 {
		writeError(response, http.StatusBadRequest, "the password must be at least eight characters")
		return
	}

	hash, err := security.HashPassword(body.Password)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	err = self.store.Update(func(configuration *config.Configuration) error {
		if body.Name != "" {
			configuration.Device.Name = body.Name
		}
		configuration.Device.Location = body.Location
		if body.Timezone != "" {
			configuration.Device.Timezone = body.Timezone
		}
		configuration.Web.PasswordHash = hash
		return nil
	})
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	log.Noticef("this device has been set up as %q", self.store.Current().Device.Name)
	self.issueSession(response, request)
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (self *Server) signIn(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	if !self.isSetUp() {
		writeError(response, http.StatusForbidden, "this device has not been set up yet")
		return
	}

	// A wrong password costs the same as a right one, and both take about a
	// tenth of a second because of the hash, which is enough to make guessing
	// over the network impractical without a rate limiter to maintain.
	if !security.VerifyPassword(self.store.Current().Web.PasswordHash, body.Password) {
		log.Warningf("a sign-in from %s was refused", clientAddress(request))
		writeError(response, http.StatusUnauthorized, "that is not the password")
		return
	}

	self.issueSession(response, request)
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (self *Server) signOut(response http.ResponseWriter, request *http.Request) {
	self.clearSession(response)
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

// readConfiguration returns the whole configuration with every secret
// replaced by a placeholder. What comes back can be edited and sent to
// writeConfiguration; the placeholders are turned back into the real values
// there, so that saving a form does not erase the passwords it was never
// shown.
func (self *Server) readConfiguration(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, self.store.Current())
}

func (self *Server) writeConfiguration(response http.ResponseWriter, request *http.Request) {
	var updated config.Configuration
	if err := decode(request, &updated); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	err := self.store.Update(func(configuration *config.Configuration) error {
		// The password hash and the session secret are never sent out, so
		// they are never sent back; keeping the existing ones is what stops a
		// save from locking the operator out of their own device.
		hash := configuration.Web.PasswordHash
		secret := configuration.Web.SessionSecret
		*configuration = updated
		configuration.Web.PasswordHash = hash
		configuration.Web.SessionSecret = secret
		return nil
	})
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(response, http.StatusOK, self.store.Current())
}

// screenshot is a picture of what is on the screen this moment, read from the
// X server rather than from the browser.
//
// The browser can take a picture of itself, and for a long time that is what
// this did. It is the wrong picture twice over.
//
// It is not what is on the screen. It is what the browser believes it drew,
// and a window that was never sized to the screen, a page covered by something
// else, or a renderer that stopped painting all look perfect in it. Reading
// the root window shows the screen, which is the thing anybody asking this
// question wants to know about.
//
// And it is not available exactly when it is wanted. Asking the browser for a
// picture when the browser is the problem answers "nothing is on the screen
// yet", which is the moment somebody most needs to see the screen — a crashed
// Chromium still leaves its last frame on the glass, and this shows it.
//
// Taking it from X also costs the screen nothing. Asking Chromium for a
// *scaled* capture re-lays the page out while it takes the picture, and that
// is visible on the wall: the dashboard jumps to another size and back, every
// few seconds, for as long as anybody has the interface open.
func (self *Server) screenshot(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()

	configuration := self.store.Current()
	connection, err := display.Open(ctx, configuration.Display.Number, self.device.XServer().Cookie())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable,
			fmt.Sprintf("the X server cannot be reached, so there is nothing to photograph: %s", err))
		return
	}
	defer connection.Close()

	screen, err := connection.Capture(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, err.Error())
		return
	}

	// A lossless picture of a 4K screen is several megabytes, which is the
	// right thing to hand somebody who asked for a screenshot and the wrong
	// thing entirely on a page that asks for a new one every three seconds:
	// on the first real device this ran on it was 5.6 MB, or 110 MB a minute
	// to leave a browser tab open on. So the interface asks for a small one.
	if request.URL.Query().Get("small") != "" {
		response.Header().Set("Content-Type", "image/jpeg")
		response.Header().Set("Cache-Control", "no-store")
		response.WriteHeader(http.StatusOK)
		// JPEG because most of what is on these screens is video from a
		// camera, which PNG stores appallingly.
		_ = jpeg.Encode(response, picture.Shrink(screen, smallScreenshotWidth), &jpeg.Options{Quality: 70})
		return
	}

	response.Header().Set("Content-Type", "image/png")
	// A screenshot is out of date the moment it is taken.
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_ = png.Encode(response, screen)
}

// smallScreenshotWidth is wide enough for the card it goes in on a large
// monitor, and small enough that fetching one every few seconds is nothing.
const smallScreenshotWidth = 960

func (self *Server) show(response http.ResponseWriter, request *http.Request) {
	identifier := mux.Vars(request)["item"]

	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()

	if err := self.device.Browser().Show(ctx, identifier); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

// navigate points the tab on the screen somewhere else without changing the
// playlist. It is for looking at something once, and the next rotation puts
// the playlist back.
func (self *Server) navigate(response http.ResponseWriter, request *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := decode(request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if body.URL == "" {
		writeError(response, http.StatusBadRequest, "an address is needed")
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()

	if err := self.device.Browser().Navigate(ctx, body.URL); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (self *Server) restart(response http.ResponseWriter, request *http.Request) {
	program := mux.Vars(request)["program"]

	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Minute)
	defer cancel()

	if err := self.device.Restart(ctx, program); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

// networkState is the machine's own network: what interfaces it has, what
// addresses they hold, and which wireless network each is on.
func (self *Server) networkState(response http.ResponseWriter, request *http.Request) {
	manager := self.device.Network()
	if manager == nil {
		writeError(response, http.StatusServiceUnavailable, "this daemon has no network manager")
		return
	}
	writeJSON(response, http.StatusOK, manager.State())
}

// scanWireless looks for wireless networks within reach of one interface.
//
// It is a POST because it is not free: the radio stops carrying traffic while
// it sweeps the bands, so a screen already on a network flickers off it for a
// second. That is worth a deliberate act rather than a page refresh.
func (self *Server) scanWireless(response http.ResponseWriter, request *http.Request) {
	manager := self.device.Network()
	if manager == nil {
		writeError(response, http.StatusServiceUnavailable, "this daemon has no network manager")
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), 45*time.Second)
	defer cancel()

	networks, err := manager.Scan(ctx, mux.Vars(request)["interface"])
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"networks": networks})
}

// enrolInFleet turns on fleet management and stores the token. The daemon's
// tunnel notices on its next attempt; there is no need to restart anything.
func (self *Server) enrolInFleet(response http.ResponseWriter, request *http.Request) {
	var body struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := decode(request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if body.Token == "" {
		writeError(response, http.StatusBadRequest, "an enrolment token is needed")
		return
	}

	err := self.store.Update(func(configuration *config.Configuration) error {
		configuration.Fleet.Enabled = true
		if body.URL != "" {
			configuration.Fleet.URL = body.URL
		}
		configuration.Fleet.EnrollmentToken = config.Secret(body.Token)
		return nil
	})
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

// leaveFleet unenrols the device: the stored credential is deleted and fleet
// management is switched off. It is one file and one flag, which is what makes
// this reversible from the device rather than only from the service.
func (self *Server) leaveFleet(response http.ResponseWriter, request *http.Request) {
	if err := fleet.ForgetCredential(self.store.Current()); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	err := self.store.Update(func(configuration *config.Configuration) error {
		configuration.Fleet.Enabled = false
		configuration.Fleet.EnrollmentToken = ""
		return nil
	})
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	log.Noticef("this device has been unenrolled from fleet management")
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

// xorgLog is the end of the X server's own log. When a screen stays black,
// the reason is always in it, and getting at it otherwise would mean a shell
// on a machine that has none.
func (self *Server) xorgLog(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(response, self.device.XServer().LogTail(200))
}

// --- the small shared pieces ------------------------------------------------

func decode(request *http.Request, into interface{}) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1024*1024))
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("that is not the JSON this expects: %w", err)
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, body interface{}) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(body); err != nil {
		log.Debugf("cannot write a response: %s", err)
	}
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}

func clientAddress(request *http.Request) string {
	return request.RemoteAddr
}
