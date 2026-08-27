package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/browser"
	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/media"
	"github.com/ziyan/cue/internal/network"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/timesync"
	"github.com/ziyan/cue/internal/watchdog"
	"github.com/ziyan/cue/internal/xserver"
)

// fakeDevice stands in for the daemon. The Device interface exists so that
// this package can be tested without starting an X server; what it hands back
// are real components that were never started, which is the state the
// interface has to cope with anyway on a device whose display will not start.
type fakeDevice struct {
	statuses  []supervise.Status
	browser   *browser.Browser
	watchdog  *watchdog.Watchdog
	xserver   *xserver.Server
	timesync  *timesync.Client
	startedAt time.Time

	setupNetwork network.Credentials
	onboarding   bool
	setupSeen    []network.WirelessNetwork
	setupTrouble string

	joinedSSID       string
	joinedPassphrase string
	rescans          int
	forgotten        int
}

func newFakeDevice(t *testing.T, store *config.Store) *fakeDevice {
	t.Helper()
	configuration := store.Current()
	server, err := xserver.New(store)
	if err != nil {
		t.Fatalf("xserver: %s", err)
	}
	return &fakeDevice{
		browser:   browser.New(configuration, ":9", "/nonexistent/Xauthority"),
		watchdog:  watchdog.New(&configuration.Watchdog, watchdog.Remedies{}),
		xserver:   server,
		timesync:  timesync.New(store),
		startedAt: time.Now(),
	}
}

func (self *fakeDevice) Statuses() []supervise.Status { return self.statuses }
func (self *fakeDevice) Browser() *browser.Browser    { return self.browser }
func (self *fakeDevice) Watchdog() *watchdog.Watchdog { return self.watchdog }
func (self *fakeDevice) VNCAddress() string           { return "127.0.0.1:5900" }
func (self *fakeDevice) StartedAt() time.Time         { return self.startedAt }
func (self *fakeDevice) XServer() *xserver.Server     { return self.xserver }
func (self *fakeDevice) TimeSync() *timesync.Client   { return self.timesync }
func (self *fakeDevice) SetupNetwork() (network.Credentials, bool) {
	return self.setupNetwork, self.onboarding
}
func (self *fakeDevice) SetupNetworks() []network.WirelessNetwork { return self.setupSeen }
func (self *fakeDevice) SetupTrouble() string                     { return self.setupTrouble }
func (self *fakeDevice) RescanFromSetup() error                   { self.rescans++; return nil }
func (self *fakeDevice) ForgetWireless() error                    { self.forgotten++; return nil }
func (self *fakeDevice) JoinFromSetup(ssid, passphrase string) error {
	self.joinedSSID, self.joinedPassphrase = ssid, passphrase
	return nil
}
func (self *fakeDevice) Network() *network.Manager             { return nil }
func (self *fakeDevice) Restart(context.Context, string) error { return nil }

func newTestServer(t *testing.T, configuration *config.Configuration) *Server {
	t.Helper()
	// Nothing in these tests reaches the network, the clock or a display. In
	// particular the display number is one nothing uses: 0 is the developer's
	// own desktop, and connecting to that from a test is both surprising and
	// slow.
	configuration.Time.Enabled = false
	configuration.Display.Number = 63
	configuration.Paths.State = t.TempDir()
	configuration.Paths.Runtime = t.TempDir()
	configuration.Normalize()

	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)
	videos, err := media.Open(filepath.Join(t.TempDir(), "media"))
	if err != nil {
		t.Fatal(err)
	}
	return New(store, newFakeDevice(t, store)).WithUploads(videos)
}

func TestHealthIsAnsweredWithoutASession(t *testing.T) {
	// A container orchestrator and a monitoring system both ask this, and
	// neither of them has a password.
	server := newTestServer(t, config.Default())
	server.device.(*fakeDevice).statuses = []supervise.Status{
		{Name: "chromium", State: supervise.StateRunning},
	}

	response := do(server, http.MethodGet, "/healthz", nil, nil)
	if response.Code != http.StatusOK {
		t.Errorf("/healthz answered %d, want 200", response.Code)
	}
}

func TestHealthIsUnhealthyWhenTheBrowserIsNotRunning(t *testing.T) {
	server := newTestServer(t, config.Default())
	server.device.(*fakeDevice).statuses = []supervise.Status{
		{Name: "chromium", State: supervise.StateBackoff},
	}

	response := do(server, http.MethodGet, "/healthz", nil, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("/healthz answered %d, want 503", response.Code)
	}
}

func TestHealthStaysHealthyWhileTheBrowserIsStarting(t *testing.T) {
	// A health check that failed during a planned restart would have an
	// orchestrator kill the container in the middle of recovering.
	server := newTestServer(t, config.Default())
	server.device.(*fakeDevice).statuses = []supervise.Status{
		{Name: "chromium", State: supervise.StateStarting},
	}

	response := do(server, http.MethodGet, "/healthz", nil, nil)
	if response.Code != http.StatusOK {
		t.Errorf("/healthz answered %d while the browser was starting, want 200", response.Code)
	}
}

func TestNothingIsReachableBeforeTheDeviceIsSetUp(t *testing.T) {
	server := newTestServer(t, config.Default())

	response := do(server, http.MethodGet, "/api/v1/status", nil, nil)
	if response.Code != http.StatusForbidden {
		t.Errorf("/api/v1/status answered %d before setup, want 403", response.Code)
	}
}

func TestSetupWorksOnceAndSignsTheBrowserIn(t *testing.T) {
	server := newTestServer(t, config.Default())

	response := do(server, http.MethodPost, "/api/v1/setup", map[string]string{
		"name": "Reception", "password": "a long test password",
	}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("setup answered %d: %s", response.Code, response.Body)
	}

	cookies := response.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != sessionCookie {
		t.Fatal("setup did not issue a session")
	}
	if !cookies[0].HttpOnly {
		t.Error("the session cookie is readable by JavaScript")
	}

	// Once is once. A second call would let anybody who reaches the device
	// before the operator does take it over.
	again := do(server, http.MethodPost, "/api/v1/setup", map[string]string{
		"name": "Theirs", "password": "another test password",
	}, nil)
	if again.Code != http.StatusConflict {
		t.Errorf("a second setup answered %d, want 409", again.Code)
	}
}

func TestSetupRefusesAShortPassword(t *testing.T) {
	server := newTestServer(t, config.Default())
	response := do(server, http.MethodPost, "/api/v1/setup", map[string]string{"password": "test"}, nil)
	if response.Code != http.StatusBadRequest {
		t.Errorf("a four character password answered %d, want 400", response.Code)
	}
}

func TestSigningInAndOut(t *testing.T) {
	server := newTestServer(t, config.Default())
	session := setUp(t, server)

	if response := do(server, http.MethodGet, "/api/v1/status", nil, session); response.Code == http.StatusUnauthorized {
		t.Fatal("a signed-in request was refused")
	}

	wrong := do(server, http.MethodPost, "/api/v1/session", map[string]string{"password": "the wrong test password"}, nil)
	if wrong.Code != http.StatusUnauthorized {
		t.Errorf("a wrong password answered %d, want 401", wrong.Code)
	}

	right := do(server, http.MethodPost, "/api/v1/session", map[string]string{"password": testPassword}, nil)
	if right.Code != http.StatusOK {
		t.Errorf("the right password answered %d, want 200", right.Code)
	}
}

func TestATamperedSessionIsRefused(t *testing.T) {
	server := newTestServer(t, config.Default())
	session := setUp(t, server)

	forged := &http.Cookie{Name: sessionCookie, Value: "9999999999.notasignature"}
	if response := do(server, http.MethodGet, "/api/v1/status", nil, forged); response.Code != http.StatusUnauthorized {
		t.Errorf("a forged session answered %d, want 401", response.Code)
	}

	// And the real one still works, so the check is not simply refusing
	// everything.
	if response := do(server, http.MethodGet, "/api/v1/configuration", nil, session); response.Code != http.StatusOK {
		t.Errorf("the real session answered %d", response.Code)
	}
}

func TestAnExpiredSessionIsRefused(t *testing.T) {
	configuration := config.Default()
	configuration.Web.SessionLifetime = config.Duration(time.Second)
	server := newTestServer(t, configuration)
	setUp(t, server)

	// A correctly signed cookie issued two hours ago.
	old := time.Now().Add(-2 * time.Hour).Unix()
	expired := &http.Cookie{
		Name:  sessionCookie,
		Value: joinSession(old, server.signSession(old)),
	}
	if response := do(server, http.MethodGet, "/api/v1/status", nil, expired); response.Code != http.StatusUnauthorized {
		t.Errorf("an expired session answered %d, want 401", response.Code)
	}
}

func TestTheConfigurationComesBackWithoutItsSecrets(t *testing.T) {
	configuration := config.Default()
	configuration.VNC.Password = "test vnc password"
	configuration.Playlist.Items = []config.Item{{
		URL: "https://example.com/",
		Login: &config.Login{
			WhenURLMatches:   "/login",
			PasswordSelector: "#password",
			Username:         "display",
			Password:         "test page password",
		},
	}}

	server := newTestServer(t, configuration)
	session := setUp(t, server)

	response := do(server, http.MethodGet, "/api/v1/configuration", nil, session)
	if response.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", response.Code, response.Body)
	}

	body := response.Body.String()
	for _, secret := range []string{"test vnc password", "test page password"} {
		if strings.Contains(body, secret) {
			t.Errorf("the API returned the secret %q", secret)
		}
	}
	if strings.Contains(body, "passwordHash") {
		t.Error("the API returned the administrator's password hash")
	}
}

func TestSavingTheConfigurationBackDoesNotEraseTheSecrets(t *testing.T) {
	// The failure this guards against: opening the settings page and pressing
	// Save wipes every credential on the device, because the interface was
	// never shown them and sends the placeholder back.
	configuration := config.Default()
	configuration.VNC.Password = "test vnc password"
	server := newTestServer(t, configuration)
	session := setUp(t, server)

	read := do(server, http.MethodGet, "/api/v1/configuration", nil, session)
	var returned map[string]interface{}
	if err := json.Unmarshal(read.Body.Bytes(), &returned); err != nil {
		t.Fatalf("decode: %s", err)
	}

	written := do(server, http.MethodPut, "/api/v1/configuration", returned, session)
	if written.Code != http.StatusOK {
		t.Fatalf("saving answered %d: %s", written.Code, written.Body)
	}

	if password := server.store.Current().VNC.Password.Reveal(); password != "test vnc password" {
		t.Errorf("the VNC password became %q after a save", password)
	}
	if server.store.Current().Web.PasswordHash == "" {
		t.Error("saving erased the administrator's password")
	}
}

func TestSavingAnInvalidConfigurationChangesNothing(t *testing.T) {
	server := newTestServer(t, config.Default())
	session := setUp(t, server)
	before := server.store.Current().Web.Listen

	broken := map[string]interface{}{"web": map[string]interface{}{"listen": "not an address"}}
	response := do(server, http.MethodPut, "/api/v1/configuration", broken, session)
	if response.Code != http.StatusBadRequest {
		t.Errorf("an invalid configuration answered %d, want 400", response.Code)
	}
	if server.store.Current().Web.Listen != before {
		t.Error("an invalid configuration was applied anyway")
	}
}

func TestTheVNCOriginCheck(t *testing.T) {
	configuration := config.Default()
	configuration.Web.TrustedOrigins = []string{"https://proxy.example.com"}
	server := newTestServer(t, configuration)

	cases := map[string]bool{
		"":                             true,  // not a browser
		"http://device.local:8080":     true,  // its own host
		"https://proxy.example.com":    true,  // a listed proxy
		"https://attacker.example.net": false, // a page elsewhere
	}
	for origin, allowed := range cases {
		request := httptest.NewRequest(http.MethodGet, "http://device.local:8080/api/v1/vnc", nil)
		request.Host = "device.local:8080"
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if server.isOriginAllowed(request) != allowed {
			t.Errorf("origin %q: allowed=%v, want %v", origin, !allowed, allowed)
		}
	}
}

func TestTheInterfaceIsServedAndUnknownPathsFallBackToIt(t *testing.T) {
	server := newTestServer(t, config.Default())

	index := do(server, http.MethodGet, "/", nil, nil)
	if index.Code != http.StatusOK {
		t.Errorf("/ answered %d", index.Code)
	}

	// The interface routes /content itself; a reload of that address must not
	// be a 404.
	page := do(server, http.MethodGet, "/content", nil, nil)
	if page.Code != http.StatusOK {
		t.Errorf("/content answered %d, want the interface", page.Code)
	}
}

// --- helpers ----------------------------------------------------------------

const testPassword = "a long test password"

// defaultConfigurationForTest is the default with the parts that would reach
// the network or a display switched off; every test here uses it.
func defaultConfigurationForTest() *config.Configuration {
	return config.Default()
}

func setUp(t *testing.T, server *Server) *http.Cookie {
	t.Helper()
	response := do(server, http.MethodPost, "/api/v1/setup", map[string]string{
		"name": "Test", "password": testPassword,
	}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("setup answered %d: %s", response.Code, response.Body)
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("setup issued no session")
	}
	return cookies[0]
}

func do(server *Server, method, path string, body interface{}, session *http.Cookie) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = strings.NewReader(string(encoded))
	} else {
		reader = strings.NewReader("")
	}

	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	if session != nil {
		request.AddCookie(session)
	}

	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	return response
}

func joinSession(issuedAt int64, signature string) string {
	return strconv.FormatInt(issuedAt, 10) + "." + signature
}
