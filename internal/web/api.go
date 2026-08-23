package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/ziyan/cue/internal/browser"
	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/display"
	"github.com/ziyan/cue/internal/hardware"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/util/drm"
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

	if connection, err := display.Open(configuration.Display.Number, self.device.XServer().Cookie()); err == nil {
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

// screenshot is a picture of what is on the screen this moment. It is the
// fastest way to answer "what is it showing" from somewhere else, and unlike
// the VNC view it needs nothing but an image tag.
func (self *Server) screenshot(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()

	image, err := self.device.Browser().Screenshot(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, err.Error())
		return
	}

	response.Header().Set("Content-Type", "image/png")
	// A screenshot is out of date the moment it is taken.
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(image)
}

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
