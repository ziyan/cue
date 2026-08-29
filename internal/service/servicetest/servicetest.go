// Package servicetest stands in for the hosted service in tests.
//
// It implements the device websocket the way the service does, from the
// service's own description rather than by calling the code under test: the
// framing constants and the control frames are written out again here. If the
// two ever disagree, a test using this notices, which is the whole reason it
// is a separate package rather than a helper inside the one it tests.
//
// What it serves over a stream is an ordinary http.Handler, run by Go's own
// HTTP server. That is the strongest available check on the framing: if the
// device's half is wrong, net/http on this side cannot talk to net/http on
// that side, and no assertion about bytes would tell you whether it could.
package servicetest

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Stub is a stand-in service.
type Stub struct {
	Server *httptest.Server

	// Credential is what a device must present to attach.
	Credential string

	// Opens counts the streams devices have asked for.
	Opens atomic.Int64

	// Refuse makes every stream request fail.
	Refuse atomic.Bool

	// Attaches counts accepted websocket connections.
	Attaches atomic.Int64

	// The connections currently attached, so a test can drop one. The
	// server's own CloseClientConnections does not reach these: a websocket
	// upgrade hijacks the connection, and a hijacked connection is no longer
	// the server's to close.
	liveMutex sync.Mutex
	live      []*websocket.Conn
}

// Disconnect drops every attached device, as a service restart or a network
// blink would.
func (self *Stub) Disconnect() {
	self.liveMutex.Lock()
	attached := self.live
	self.live = nil
	self.liveMutex.Unlock()
	for _, connection := range attached {
		_ = connection.Close()
	}
}

// New returns a stub serving routes over the tunnel. The websocket lives at
// /api/v1/device/websocket, and anything else on the listener is served
// directly, so a test can offer public endpoints as well.
func New(t *testing.T, routes http.Handler, public http.Handler) *Stub {
	t.Helper()
	stub := &Stub{Credential: "an-example-credential"}

	upgrader := websocket.Upgrader{}
	stub.Server = httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/api/v1/device/websocket" {
				if public != nil {
					public.ServeHTTP(response, request)
					return
				}
				http.NotFound(response, request)
				return
			}
			if request.Header.Get("Authorization") != "Bearer "+stub.Credential {
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			connection, err := upgrader.Upgrade(response, request, nil)
			if err != nil {
				return
			}
			stub.Attaches.Add(1)
			stub.liveMutex.Lock()
			stub.live = append(stub.live, connection)
			stub.liveMutex.Unlock()
			stub.carry(connection, routes)
		}))
	t.Cleanup(stub.Server.Close)
	return stub
}

// carry reads one connection and gives each stream to the HTTP server.
func (self *Stub) carry(connection *websocket.Conn, routes http.Handler) {
	defer func() { _ = connection.Close() }()
	connection.SetReadLimit(1 << 20)

	var writeMutex sync.Mutex
	send := func(kind int, payload []byte) error {
		writeMutex.Lock()
		defer writeMutex.Unlock()
		return connection.WriteMessage(kind, payload)
	}

	var streamsMutex sync.Mutex
	streams := map[string]*io.PipeWriter{}

	for {
		kind, payload, err := connection.ReadMessage()
		if err != nil {
			streamsMutex.Lock()
			for _, writer := range streams {
				_ = writer.Close()
			}
			streamsMutex.Unlock()
			return
		}

		switch kind {
		// A text message is control JSON. There is no field saying so: the
		// message type is the discriminator.
		case websocket.TextMessage:
			var frame struct {
				Stream string `json:"stream"`
				Kind   string `json:"kind"`
				Host   string `json:"host"`
				Port   int    `json:"port"`
			}
			if err := json.Unmarshal(payload, &frame); err != nil || frame.Kind != "open" {
				continue
			}
			self.Opens.Add(1)

			answer := func(kind, reason string) {
				encoded, _ := json.Marshal(map[string]any{
					"stream": frame.Stream, "kind": kind, "error": reason,
				})
				_ = send(websocket.TextMessage, encoded)
			}
			if self.Refuse.Load() {
				answer("failed", "refused for the test")
				continue
			}
			// The only thing a device may open.
			if frame.Host != "cue" || frame.Port != 80 {
				answer("failed", "only the cue tunnel may be opened from a device")
				continue
			}

			identifier := frame.Stream
			fromDevice, feed := io.Pipe()
			streamsMutex.Lock()
			streams[identifier] = feed
			streamsMutex.Unlock()

			answer("opened", "")
			go serve(&served{
				reader: fromDevice,
				write: func(body []byte) error {
					// A big-endian uint16 length of the identifier, the
					// identifier, then the payload. The websocket message
					// boundary is the payload's length.
					frame := make([]byte, 2+len(identifier)+len(body))
					binary.BigEndian.PutUint16(frame[:2], uint16(len(identifier)))
					copy(frame[2:], identifier)
					copy(frame[2+len(identifier):], body)
					return send(websocket.BinaryMessage, frame)
				},
			}, routes)

		case websocket.BinaryMessage:
			if len(payload) < 2 {
				continue
			}
			length := int(binary.BigEndian.Uint16(payload[:2]))
			if len(payload) < 2+length {
				continue
			}
			streamsMutex.Lock()
			writer := streams[string(payload[2:2+length])]
			streamsMutex.Unlock()
			if writer != nil {
				_, _ = writer.Write(payload[2+length:])
			}
		}
	}
}

// served is the service's end of one stream.
type served struct {
	reader io.Reader
	write  func(body []byte) error
}

func (self *served) Read(into []byte) (int, error) { return self.reader.Read(into) }

func (self *served) Write(from []byte) (int, error) {
	kept := make([]byte, len(from))
	copy(kept, from)
	if err := self.write(kept); err != nil {
		return 0, err
	}
	return len(from), nil
}

func (self *served) Close() error                     { return nil }
func (self *served) LocalAddr() net.Addr              { return address{} }
func (self *served) RemoteAddr() net.Addr             { return address{} }
func (self *served) SetDeadline(time.Time) error      { return nil }
func (self *served) SetReadDeadline(time.Time) error  { return nil }
func (self *served) SetWriteDeadline(time.Time) error { return nil }

type address struct{}

func (address) Network() string { return "cue" }
func (address) String() string  { return "cue" }

// serve runs one HTTP conversation over a stream.
func serve(side net.Conn, routes http.Handler) {
	server := &http.Server{Handler: routes, ReadHeaderTimeout: 30 * time.Second}
	_ = server.Serve(&once{connection: side})
}

// once hands a single connection to http.Server and then stops, so the server
// serves that one and does not go looking for another.
type once struct {
	connection net.Conn
	given      bool
	mutex      sync.Mutex
}

func (self *once) Accept() (net.Conn, error) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	if self.given {
		return nil, io.EOF
	}
	self.given = true
	return self.connection, nil
}

func (self *once) Close() error   { return nil }
func (self *once) Addr() net.Addr { return address{} }
