package service

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ziyan/cue/internal/config"
)

// A stub of the service's device websocket, implementing the framing from the
// other side: text messages are control JSON, binary messages carry a
// big-endian uint16 length, the stream identifier, then the payload.
//
// It serves the real device routes over the stream with Go's own HTTP server,
// which is the point of the exercise: if this device's half of the framing is
// wrong, net/http on this side will not be able to talk to net/http on that
// side, and no amount of asserting on bytes would prove that it can.
type stubService struct {
	server *httptest.Server

	credential  string
	screenshots atomic.Int64
	opens       atomic.Int64
	refuse      atomic.Bool

	mutex     sync.Mutex
	lastImage []byte
	lastType  string
}

func newStubService(t *testing.T) *stubService {
	t.Helper()
	stub := &stubService{credential: "an-example-credential"}

	routes := http.NewServeMux()
	routes.HandleFunc("/api/v1/device/screenshot", func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(request.Body, 8<<20))
		stub.mutex.Lock()
		stub.lastImage = body
		stub.lastType = request.Header.Get("Content-Type")
		stub.mutex.Unlock()
		stub.screenshots.Add(1)
		response.WriteHeader(http.StatusNoContent)
	})
	routes.HandleFunc("/api/v1/device/self", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{"id": "device-1", "name": "carbon"})
	})

	upgrader := websocket.Upgrader{}
	stub.server = httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/api/v1/device/websocket" {
				http.NotFound(response, request)
				return
			}
			if request.Header.Get("Authorization") != "Bearer "+stub.credential {
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			connection, err := upgrader.Upgrade(response, request, nil)
			if err != nil {
				return
			}
			stub.serve(t, connection, routes)
		}))
	t.Cleanup(stub.server.Close)
	return stub
}

// serve reads the connection and gives each stream to the HTTP server.
func (self *stubService) serve(t *testing.T, connection *websocket.Conn, routes http.Handler) {
	defer func() { _ = connection.Close() }()

	var writeMutex sync.Mutex
	send := func(kind int, payload []byte) error {
		writeMutex.Lock()
		defer writeMutex.Unlock()
		return connection.WriteMessage(kind, payload)
	}

	// One pipe per stream: what the device writes goes in, and what the HTTP
	// server writes comes back out as data frames.
	incoming := map[string]*io.PipeWriter{}
	var streamsMutex sync.Mutex

	for {
		kind, payload, err := connection.ReadMessage()
		if err != nil {
			streamsMutex.Lock()
			for _, writer := range incoming {
				_ = writer.Close()
			}
			streamsMutex.Unlock()
			return
		}

		switch kind {
		case websocket.TextMessage:
			var frame controlFrame
			if err := json.Unmarshal(payload, &frame); err != nil {
				continue
			}
			if frame.Kind != kindOpen {
				continue
			}
			self.opens.Add(1)
			if self.refuse.Load() {
				_ = send(websocket.TextMessage, mustEncode(controlFrame{
					Stream: frame.Stream, Kind: kindFailed, Error: "refused for the test",
				}))
				continue
			}
			if frame.Host != tunnelHost || frame.Port != tunnelPort {
				t.Errorf("the device tried to open %s:%d", frame.Host, frame.Port)
				_ = send(websocket.TextMessage, mustEncode(controlFrame{
					Stream: frame.Stream, Kind: kindFailed,
					Error: "only the cue tunnel may be opened from a device",
				}))
				continue
			}

			// What the device writes is fed in here; what the server writes
			// goes back out as data frames. Two directions, kept separate:
			// giving the server one end of a pipe and also reading that end
			// meant the server and the echo raced for the request bytes, and
			// the device read its own request back as a response.
			frameStream := frame.Stream
			fromDevice, feed := io.Pipe()
			side := &servedStream{
				reader: fromDevice,
				write: func(payload []byte) error {
					frame := make([]byte, 2+len(frameStream)+len(payload))
					binary.BigEndian.PutUint16(frame[:2], uint16(len(frameStream)))
					copy(frame[2:], frameStream)
					copy(frame[2+len(frameStream):], payload)
					return send(websocket.BinaryMessage, frame)
				},
			}

			streamsMutex.Lock()
			incoming[frame.Stream] = feed
			streamsMutex.Unlock()

			_ = send(websocket.TextMessage, mustEncode(controlFrame{
				Stream: frame.Stream, Kind: kindOpened,
			}))

			go serveHTTP(side, routes)

		case websocket.BinaryMessage:
			if len(payload) < 2 {
				continue
			}
			length := int(binary.BigEndian.Uint16(payload[:2]))
			if len(payload) < 2+length {
				continue
			}
			streamsMutex.Lock()
			writer := incoming[string(payload[2:2+length])]
			streamsMutex.Unlock()
			if writer != nil {
				_, _ = writer.Write(payload[2+length:])
			}
		}
	}
}

func mustEncode(frame controlFrame) []byte {
	encoded, _ := json.Marshal(frame)
	return encoded
}

func newStore(t *testing.T, address, credential string) *config.Store {
	t.Helper()
	configuration := config.Default()
	configuration.Service.Address = address
	configuration.Service.Secret = config.Secret(credential)
	configuration.Service.DeviceID = "device-1"
	configuration.Normalize()
	return config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)
}

func waitFor(t *testing.T, within time.Duration, why string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

// The whole point: a picture sent by this device arrives at the service, with
// net/http on both ends of a connection this package framed itself.
func TestAPictureReachesTheServiceOverTheTunnel(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL, stub.credential)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("not really a jpeg, but bytes"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "a picture to arrive", func() bool {
		return stub.screenshots.Load() > 0
	})

	stub.mutex.Lock()
	body, contentType := stub.lastImage, stub.lastType
	stub.mutex.Unlock()
	if string(body) != "not really a jpeg, but bytes" {
		t.Errorf("the service received %q", body)
	}
	if contentType != "image/jpeg" {
		t.Errorf("the picture arrived as %q", contentType)
	}

	if state := reporter.State(); !state.Attached || state.LastReportedAt == nil {
		t.Errorf("the reporter says %+v", state)
	}
}

// An unlinked device says nothing to anybody.
func TestAnUnlinkedDeviceDoesNotReport(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL, "")

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	time.Sleep(500 * time.Millisecond)

	if opened := stub.opens.Load(); opened != 0 {
		t.Errorf("an unlinked device opened %d streams", opened)
	}
	if sent := stub.screenshots.Load(); sent != 0 {
		t.Errorf("an unlinked device sent %d pictures", sent)
	}
	if state := reporter.State(); state.Attached {
		t.Error("an unlinked device reports itself attached")
	}
}

// A screen that cannot be photographed is not a reason to drop the connection.
func TestAPictureThatCannotBeTakenKeepsTheConnection(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL, stub.credential)

	var tries atomic.Int64
	reporter := New(store, func(context.Context) ([]byte, string, error) {
		if tries.Add(1) == 1 {
			return nil, "", io.ErrUnexpectedEOF
		}
		return []byte("a picture"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the reporter to attach", func() bool {
		return reporter.State().Attached
	})
	if tries.Load() < 1 {
		t.Error("no picture was attempted")
	}
	if state := reporter.State(); !state.Attached {
		t.Error("a failed photograph dropped the connection")
	}
}

// The device must ask for the one thing the service allows, and nothing else.
func TestTheDeviceOnlyOpensTheServiceItself(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL, stub.credential)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "a stream to be opened", func() bool {
		return stub.opens.Load() > 0
	})
	// The stub fails the test itself if the device names anything else.
}

// A refused stream is not the end of the world: the connection is dropped and
// attached again rather than the device sitting there believing in a stream it
// does not have.
func TestARefusedStreamIsRecoveredFrom(t *testing.T) {
	stub := newStubService(t)
	stub.refuse.Store(true)
	store := newStore(t, stub.server.URL, stub.credential)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the device to try", func() bool { return stub.opens.Load() > 0 })

	stub.refuse.Store(false)
	waitFor(t, 20*time.Second, "a picture to arrive once streams are allowed", func() bool {
		return stub.screenshots.Load() > 0
	})
}

// serveHTTP runs one HTTP conversation over a stream, which is what the
// service does with a stream a device opens.
// servedStream is the service's end of one stream: it reads what the device
// wrote and writes back through the tunnel.
type servedStream struct {
	reader io.Reader
	write  func(payload []byte) error
}

func (self *servedStream) Read(into []byte) (int, error) { return self.reader.Read(into) }
func (self *servedStream) Write(from []byte) (int, error) {
	kept := make([]byte, len(from))
	copy(kept, from)
	if err := self.write(kept); err != nil {
		return 0, err
	}
	return len(from), nil
}
func (self *servedStream) Close() error                     { return nil }
func (self *servedStream) LocalAddr() net.Addr              { return tunnelAddress{} }
func (self *servedStream) RemoteAddr() net.Addr             { return tunnelAddress{} }
func (self *servedStream) SetDeadline(time.Time) error      { return nil }
func (self *servedStream) SetReadDeadline(time.Time) error  { return nil }
func (self *servedStream) SetWriteDeadline(time.Time) error { return nil }

func serveHTTP(side net.Conn, routes http.Handler) {
	server := &http.Server{Handler: routes, ReadHeaderTimeout: 30 * time.Second}
	_ = server.Serve(&oneConnection{connection: side})
}

// oneConnection hands a single connection to http.Server and then blocks, so
// the server serves it and does not go looking for another.
type oneConnection struct {
	connection net.Conn
	given      bool
	mutex      sync.Mutex
}

func (self *oneConnection) Accept() (net.Conn, error) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	if self.given {
		return nil, io.EOF
	}
	self.given = true
	return self.connection, nil
}

func (self *oneConnection) Close() error   { return nil }
func (self *oneConnection) Addr() net.Addr { return tunnelAddress{} }

// A credential that works but is for another device must not be used to report
// this screen as that one.
func TestACredentialForAnotherDeviceIsNotReportedWith(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL, stub.credential)
	// The service says this credential is device-1; the file says this device
	// is something else.
	_ = store.Update(func(configuration *config.Configuration) error {
		configuration.Service.DeviceID = "a-different-device"
		return nil
	})

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the device to notice", func() bool {
		return reporter.State().Trouble != ""
	})

	if sent := stub.screenshots.Load(); sent != 0 {
		t.Errorf("%d pictures were sent with a credential for another device", sent)
	}
	if state := reporter.State(); state.Attached {
		t.Error("the device stayed attached with a credential for another device")
	}
}

// The tunnel carries a whole HTTP conversation, so a run of reports shares one
// stream rather than opening a new one for each.
func TestReportsShareOneStream(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL, stub.credential)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the first picture", func() bool {
		return stub.screenshots.Load() > 0
	})

	// The identity call and the first report both went down the tunnel; if
	// each request opened its own stream there would be one per request.
	if opened := stub.opens.Load(); opened != 1 {
		t.Errorf("%d streams were opened for two requests; keep-alive is not working", opened)
	}
}
