package fleet

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/deferutil"
	"github.com/ziyan/cue/internal/version"
)

// Tunnel keeps one outbound connection to the management service open, and
// serves whatever the service asks for over it.
//
// The connection carries a yamux session: many independent streams over one
// socket, the way HTTP/2 does. The service opens a stream, speaks HTTP on it,
// and the device answers with the very same handler that serves the local web
// interface. That is the whole design: there is no second interface with more
// privileges, so nothing the service can do is anything an operator could not
// do standing in front of the screen.
type Tunnel struct {
	store   *config.Store
	handler http.Handler

	mutex sync.Mutex
	state State
}

// NewTunnel returns a tunnel that serves the given handler. Nothing connects
// until Run.
func NewTunnel(store *config.Store, handler http.Handler) *Tunnel {
	return &Tunnel{store: store, handler: handler}
}

// State is a snapshot for the interface.
func (self *Tunnel) State() State {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.state
}

// Run enrols the device if it has a token and has not enrolled yet, then keeps
// the connection open until the context is cancelled.
func (self *Tunnel) Run(ctx context.Context) {
	configuration := self.store.Current()

	self.mutex.Lock()
	self.state.Enabled = configuration.Fleet.Enabled
	self.state.URL = configuration.Fleet.URL
	self.mutex.Unlock()

	if !configuration.Fleet.Enabled {
		return
	}

	log.Noticef("fleet management is enabled; the service is %s", configuration.Fleet.URL)

	for {
		if ctx.Err() != nil {
			return
		}

		credential, err := self.ensureEnrolled(ctx)
		if err != nil {
			self.recordFailure(err)
			log.Errorf("%s", err)
			if !sleep(ctx, 60*time.Second) {
				return
			}
			continue
		}

		if err := self.connect(ctx, credential); err != nil {
			self.recordFailure(err)
			log.Warningf("the connection to %s ended: %s", credential.URL, err)
		}

		// Reconnect after a pause. A fixed minute rather than a growing
		// backoff: the service being down for an hour should not mean an hour
		// of extra silence after it comes back, and one connection attempt a
		// minute from each device is not a load worth avoiding.
		if !sleep(ctx, 60*time.Second) {
			return
		}
	}
}

// ensureEnrolled returns the stored credential, enrolling first if there is a
// token and no credential yet.
func (self *Tunnel) ensureEnrolled(ctx context.Context) (*Credential, error) {
	configuration := self.store.Current()

	credential, err := LoadCredential(configuration)
	if err != nil {
		return nil, err
	}

	// A credential for a different service is not this device's business to
	// reconcile: say so rather than authenticating against the wrong place.
	if credential != nil && credential.URL != configuration.Fleet.URL {
		return nil, fmt.Errorf("fleet: this device is enrolled with %s but is configured for %s; "+
			"unenrol it first", credential.URL, configuration.Fleet.URL)
	}
	if credential != nil {
		self.mutex.Lock()
		self.state.Enrolled = true
		self.mutex.Unlock()
		return credential, nil
	}

	if !configuration.Fleet.EnrollmentToken.IsSet() {
		return nil, fmt.Errorf("fleet: this device is not enrolled and has no enrolment token")
	}

	log.Noticef("enrolling with %s", configuration.Fleet.URL)
	credential, name, err := Enrol(ctx, configuration)
	if err != nil {
		return nil, err
	}
	if err := SaveCredential(configuration, credential); err != nil {
		return nil, err
	}

	// The token is used once. Clearing it means a token that leaks cannot be
	// used to enrol something else claiming to be this device.
	err = self.store.Update(func(updated *config.Configuration) error {
		updated.Fleet.EnrollmentToken = ""
		if name != "" {
			updated.Device.Name = name
		}
		return nil
	})
	if err != nil {
		log.Warningf("enrolled, but the enrolment token could not be cleared from the configuration: %s", err)
	}

	self.mutex.Lock()
	self.state.Enrolled = true
	self.mutex.Unlock()

	log.Noticef("enrolled with %s", credential.URL)
	return credential, nil
}

// connect opens the WebSocket, wraps it in a yamux session, and serves streams
// until the connection ends.
func (self *Tunnel) connect(ctx context.Context, credential *Credential) error {
	address, err := websocketURL(credential.URL, tunnelPath)
	if err != nil {
		return err
	}

	self.mutex.Lock()
	attempt := time.Now()
	self.state.LastAttempt = &attempt
	self.mutex.Unlock()

	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+credential.Secret)
	headers.Set("User-Agent", "cue/"+version.Version())
	headers.Set("X-Cue-Device", credential.DeviceIdentifier)

	connection, response, err := dialer.DialContext(ctx, address, headers)
	if err != nil {
		if response != nil {
			return fmt.Errorf("fleet: cannot connect to %s: %s: %w", address, response.Status, err)
		}
		return fmt.Errorf("fleet: cannot connect to %s: %w", address, err)
	}
	defer func() { _ = connection.Close() }()

	session, err := yamux.Client(newStreamOverWebSocket(connection), yamuxSettings())
	if err != nil {
		return fmt.Errorf("fleet: %w", err)
	}
	defer func() { _ = session.Close() }()

	self.mutex.Lock()
	connected := time.Now()
	self.state.Connected = true
	self.state.ConnectedAt = &connected
	self.state.LastError = ""
	self.state.Reconnects++
	self.mutex.Unlock()

	defer func() {
		self.mutex.Lock()
		self.state.Connected = false
		self.mutex.Unlock()
	}()

	log.Noticef("connected to %s", credential.URL)

	// One HTTP server over the yamux session. Every stream the service opens
	// is a connection to it, and it is the same handler the local interface
	// uses — so the service gets exactly that interface and nothing more.
	server := &http.Server{
		Handler:           self.countingHandler(),
		ReadHeaderTimeout: 30 * time.Second,
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	served := make(chan error, 1)
	go func() {
		defer deferutil.Recover()
		served <- server.Serve(session)
	}()

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		return nil
	}
}

// countingHandler wraps the interface's handler to count what the service
// asks for, which is the only thing about the tunnel worth showing.
func (self *Tunnel) countingHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		self.mutex.Lock()
		self.state.StreamsServed++
		self.mutex.Unlock()
		self.handler.ServeHTTP(response, request)
	})
}

func (self *Tunnel) recordFailure(err error) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.state.Connected = false
	self.state.LastError = err.Error()
}

// yamuxSettings quietens yamux's own logging and sets a keepalive short enough
// that a connection dropped by something in the middle is noticed in under a
// minute rather than when somebody tries to use it.
func yamuxSettings() *yamux.Config {
	settings := yamux.DefaultConfig()
	settings.KeepAliveInterval = 30 * time.Second
	settings.ConnectionWriteTimeout = 30 * time.Second
	settings.LogOutput = logWriter{}
	return settings
}

type logWriter struct{}

func (logWriter) Write(content []byte) (int, error) {
	log.Debugf("yamux: %s", strings.TrimSpace(string(content)))
	return len(content), nil
}

// websocketURL turns the service's https address into the ws address of one of
// its paths.
func websocketURL(base, path string) (string, error) {
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://") + path, nil
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://") + path, nil
	default:
		return "", fmt.Errorf("fleet: %q is not an http or https address", base)
	}
}

func sleep(ctx context.Context, duration time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(duration):
		return true
	}
}

// streamOverWebSocket makes a WebSocket carrying binary frames look like an
// ordinary connection, which is what yamux wants. Every frame is a piece of
// the byte stream, and a partly-read frame is kept until it has been consumed.
type streamOverWebSocket struct {
	connection *websocket.Conn

	readMutex sync.Mutex
	pending   []byte

	writeMutex sync.Mutex
}

func newStreamOverWebSocket(connection *websocket.Conn) net.Conn {
	return &streamOverWebSocket{connection: connection}
}

func (self *streamOverWebSocket) Read(buffer []byte) (int, error) {
	self.readMutex.Lock()
	defer self.readMutex.Unlock()

	for len(self.pending) == 0 {
		messageType, data, err := self.connection.ReadMessage()
		if err != nil {
			return 0, err
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		self.pending = data
	}

	count := copy(buffer, self.pending)
	self.pending = self.pending[count:]
	return count, nil
}

func (self *streamOverWebSocket) Write(buffer []byte) (int, error) {
	self.writeMutex.Lock()
	defer self.writeMutex.Unlock()

	if err := self.connection.WriteMessage(websocket.BinaryMessage, buffer); err != nil {
		return 0, err
	}
	return len(buffer), nil
}

func (self *streamOverWebSocket) Close() error {
	return self.connection.Close()
}

func (self *streamOverWebSocket) LocalAddr() net.Addr  { return self.connection.LocalAddr() }
func (self *streamOverWebSocket) RemoteAddr() net.Addr { return self.connection.RemoteAddr() }

func (self *streamOverWebSocket) SetDeadline(deadline time.Time) error {
	if err := self.SetReadDeadline(deadline); err != nil {
		return err
	}
	return self.SetWriteDeadline(deadline)
}

func (self *streamOverWebSocket) SetReadDeadline(deadline time.Time) error {
	return self.connection.SetReadDeadline(deadline)
}

func (self *streamOverWebSocket) SetWriteDeadline(deadline time.Time) error {
	return self.connection.SetWriteDeadline(deadline)
}
