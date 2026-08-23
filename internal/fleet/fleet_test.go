package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"github.com/ziyan/cue/internal/config"
)

// stubService stands in for the management service. It does the two things
// the device needs one to do: hand out a credential in exchange for a token,
// and accept a tunnel it can then make requests over.
type stubService struct {
	server *httptest.Server

	mutex     sync.Mutex
	enrolled  []enrolmentRequest
	sessions  []*yamux.Session
	connected chan struct{}

	// name, when set, is sent back at enrolment to rename the device.
	name string

	// secret is what the service hands out.
	secret string
}

func newStubService(t *testing.T) *stubService {
	t.Helper()
	service := &stubService{
		connected: make(chan struct{}, 4),
		secret:    "a test fleet secret",
	}

	router := http.NewServeMux()
	router.HandleFunc(enrolmentPath, service.enrol)
	router.HandleFunc(tunnelPath, service.tunnel)

	service.server = httptest.NewServer(router)
	t.Cleanup(service.server.Close)
	return service
}

func (self *stubService) enrol(response http.ResponseWriter, request *http.Request) {
	var body enrolmentRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}

	self.mutex.Lock()
	self.enrolled = append(self.enrolled, body)
	self.mutex.Unlock()

	if body.Token == "" {
		http.Error(response, "no token", http.StatusUnauthorized)
		return
	}

	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(enrolmentResponse{Secret: self.secret, Name: self.name})
}

func (self *stubService) tunnel(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer "+self.secret {
		http.Error(response, "wrong credential", http.StatusUnauthorized)
		return
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	connection, err := upgrader.Upgrade(response, request, nil)
	if err != nil {
		return
	}

	session, err := yamux.Server(newStreamOverWebSocket(connection), yamuxSettings())
	if err != nil {
		_ = connection.Close()
		return
	}

	self.mutex.Lock()
	self.sessions = append(self.sessions, session)
	self.mutex.Unlock()

	select {
	case self.connected <- struct{}{}:
	default:
	}
}

// ask makes an HTTP request to the device through the tunnel, which is the
// whole point of the tunnel existing.
func (self *stubService) ask(path string) (int, string, error) {
	self.mutex.Lock()
	if len(self.sessions) == 0 {
		self.mutex.Unlock()
		return 0, "", fmt.Errorf("no device is connected")
	}
	session := self.sessions[len(self.sessions)-1]
	self.mutex.Unlock()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (netConn, error) {
				return session.Open()
			},
		},
		Timeout: 10 * time.Second,
	}

	response, err := client.Get("http://device" + path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = response.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, string(body), nil
}

func testConfiguration(t *testing.T, service *stubService) *config.Configuration {
	t.Helper()
	configuration := config.Default()
	configuration.Paths.State = t.TempDir()
	configuration.Paths.Runtime = t.TempDir()
	configuration.Fleet.Enabled = true
	configuration.Fleet.URL = service.server.URL
	configuration.Fleet.EnrollmentToken = "a test enrolment token"
	configuration.Normalize()
	return configuration
}

func TestEnrollingStoresACredentialAndClearsTheToken(t *testing.T) {
	service := newStubService(t)
	configuration := testConfiguration(t, service)
	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)

	tunnel := NewTunnel(store, http.NotFoundHandler())
	credential, err := tunnel.ensureEnrolled(context.Background())
	if err != nil {
		t.Fatalf("enrol: %s", err)
	}

	if credential.Secret != service.secret {
		t.Errorf("the credential is %q, want the one the service handed out", credential.Secret)
	}
	if credential.DeviceIdentifier != configuration.Device.Identifier {
		t.Error("the credential is not for this device")
	}

	// The token is used once. One that leaks must not be usable to enrol
	// something else claiming to be this device.
	if store.Current().Fleet.EnrollmentToken.IsSet() {
		t.Error("the enrolment token was not cleared after being used")
	}

	// And it survives a restart.
	reloaded, err := LoadCredential(store.Current())
	if err != nil {
		t.Fatalf("load: %s", err)
	}
	if reloaded == nil || reloaded.Secret != service.secret {
		t.Error("the credential was not stored")
	}
}

func TestTheServiceCanRenameTheDeviceAtEnrolment(t *testing.T) {
	// A fleet is named centrally; a screen called "cue" in a list of two
	// hundred is no use to anybody.
	service := newStubService(t)
	service.name = "Reception, second floor"

	configuration := testConfiguration(t, service)
	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)

	tunnel := NewTunnel(store, http.NotFoundHandler())
	if _, err := tunnel.ensureEnrolled(context.Background()); err != nil {
		t.Fatalf("enrol: %s", err)
	}
	if name := store.Current().Device.Name; name != "Reception, second floor" {
		t.Errorf("the device is called %q, want the name the service gave it", name)
	}
}

func TestEnrollingAgainReusesTheStoredCredential(t *testing.T) {
	service := newStubService(t)
	configuration := testConfiguration(t, service)
	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)

	tunnel := NewTunnel(store, http.NotFoundHandler())
	if _, err := tunnel.ensureEnrolled(context.Background()); err != nil {
		t.Fatalf("first enrol: %s", err)
	}
	if _, err := tunnel.ensureEnrolled(context.Background()); err != nil {
		t.Fatalf("second enrol: %s", err)
	}

	service.mutex.Lock()
	defer service.mutex.Unlock()
	if len(service.enrolled) != 1 {
		t.Errorf("the device enrolled %d times, want once", len(service.enrolled))
	}
}

func TestACredentialForADifferentServiceIsRefused(t *testing.T) {
	service := newStubService(t)
	configuration := testConfiguration(t, service)
	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)

	tunnel := NewTunnel(store, http.NotFoundHandler())
	if _, err := tunnel.ensureEnrolled(context.Background()); err != nil {
		t.Fatalf("enrol: %s", err)
	}

	// The operator points the device somewhere else without unenrolling it.
	err := store.Update(func(updated *config.Configuration) error {
		updated.Fleet.URL = "https://elsewhere.example.com"
		return nil
	})
	if err != nil {
		t.Fatalf("update: %s", err)
	}

	if _, err := tunnel.ensureEnrolled(context.Background()); err == nil {
		t.Error("a credential for another service should be refused, not used")
	} else if !strings.Contains(err.Error(), "unenrol") {
		t.Errorf("the error should say what to do about it: %s", err)
	}
}

func TestTheServiceReachesTheDeviceThroughTheTunnel(t *testing.T) {
	// This is the whole point: the device dials out, and the service then
	// makes ordinary HTTP requests back down that connection to the very same
	// handler the local interface uses.
	service := newStubService(t)
	configuration := testConfiguration(t, service)
	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)

	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/healthz" {
			_, _ = response.Write([]byte("ok from the device"))
			return
		}
		http.NotFound(response, request)
	})

	tunnel := NewTunnel(store, handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tunnel.Run(ctx)

	select {
	case <-service.connected:
	case <-time.After(15 * time.Second):
		t.Fatalf("the device never connected: %s", tunnel.State().LastError)
	}

	// Give the device's HTTP server a moment to start serving the session.
	var status int
	var body string
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		status, body, err = service.ask("/healthz")
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("asking through the tunnel: %s", err)
	}
	if status != http.StatusOK || body != "ok from the device" {
		t.Errorf("the device answered %d %q", status, body)
	}

	if state := tunnel.State(); !state.Connected || state.StreamsServed == 0 {
		t.Errorf("the state does not reflect a working tunnel: %+v", state)
	}
}

func TestTheTunnelReconnectsAfterTheServiceDropsIt(t *testing.T) {
	service := newStubService(t)
	configuration := testConfiguration(t, service)
	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)

	tunnel := NewTunnel(store, http.NotFoundHandler())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tunnel.Run(ctx)

	select {
	case <-service.connected:
	case <-time.After(15 * time.Second):
		t.Fatal("the device never connected")
	}

	// The service drops it, as a restart or a load balancer would.
	service.mutex.Lock()
	session := service.sessions[0]
	service.mutex.Unlock()
	_ = session.Close()

	// The device waits a minute before reconnecting, which is far longer than
	// a test should take. What is checked here is that it notices the
	// connection has gone at all — a tunnel that thinks it is up when it is
	// not is worse than one that is plainly down.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if !tunnel.State().Connected {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Error("the device still believes it is connected after the service dropped it")
}

func TestNothingHappensWhenFleetManagementIsOff(t *testing.T) {
	// The daemon must make no outbound connection of its own accord.
	service := newStubService(t)
	configuration := testConfiguration(t, service)
	configuration.Fleet.Enabled = false
	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)

	tunnel := NewTunnel(store, http.NotFoundHandler())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tunnel.Run(ctx)

	service.mutex.Lock()
	defer service.mutex.Unlock()
	if len(service.enrolled) != 0 || len(service.sessions) != 0 {
		t.Error("a device with fleet management switched off contacted the service")
	}
}

func TestForgettingTheCredentialUnenrolsTheDevice(t *testing.T) {
	service := newStubService(t)
	configuration := testConfiguration(t, service)
	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)

	tunnel := NewTunnel(store, http.NotFoundHandler())
	if _, err := tunnel.ensureEnrolled(context.Background()); err != nil {
		t.Fatalf("enrol: %s", err)
	}

	if err := ForgetCredential(store.Current()); err != nil {
		t.Fatalf("forget: %s", err)
	}
	credential, err := LoadCredential(store.Current())
	if err != nil {
		t.Fatalf("load: %s", err)
	}
	if credential != nil {
		t.Error("the credential is still there")
	}

	// Forgetting twice is not an error: a script that unenrols should be
	// runnable more than once.
	if err := ForgetCredential(store.Current()); err != nil {
		t.Errorf("forgetting an already-forgotten credential: %s", err)
	}
}
