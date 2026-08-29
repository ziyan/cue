// Package service holds open a connection to the hosted service and reports
// over it.
//
// A screen is usually behind a router with no way in, so nothing can connect
// to it. The device connects out instead, over a websocket, and keeps the line
// open. Everything it tells the service travels over that one connection.
//
// The connection carries many conversations at once, told apart by a stream
// identifier. Two kinds of message ride on it and the websocket message type
// is what tells them apart, rather than any field:
//
//   - a text message is JSON, and opens, accepts, refuses or closes a stream
//   - a binary message is bytes for one stream: a big-endian uint16 giving the
//     length of the stream identifier, then the identifier, then the payload
//
// The payload has no length of its own. The websocket message boundary is the
// length, which is why nothing here may write a whole image in one call: the
// service closes the connection on a message over its limit rather than
// failing the write, so an oversized frame looks like the network dying.
package service

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/util/security"
)

var log = logging.MustGetLogger("service")

const (
	// The only thing a device may open. It means the service itself; the
	// service refuses anything else.
	tunnelHost = "cue"
	tunnelPort = 80

	// The service closes a stream that has gone this long without a request,
	// so nothing here may assume one it opened earlier is still there.
	streamIdleTimeout = 2 * time.Minute

	// How long to wait for the service to accept or refuse a stream.
	openTimeout = 30 * time.Second

	// What one message may carry.
	//
	// Sixteen kilobytes, which is far below any limit the service has had.
	// The first version used half a megabyte on the strength of the service's
	// current limit being a megabyte, and it failed against the real service
	// within a minute: that limit is recent and the deployed one was still
	// 32 KB, so the first screenshot closed the connection with "message too
	// big" and the reporter never got a picture through.
	//
	// The reasoning that produced the larger number was wrong twice. It
	// assumed the service a device meets is the service in front of me, and a
	// device on a wall meets whatever is deployed, possibly for years. And it
	// assumed Go's HTTP client would never hand this a large write, because
	// it buffers bodies at four kilobytes -- but a *bytes.Reader implements
	// WriteTo, so io.Copy bypasses the buffer entirely and delivers the whole
	// image in one call.
	//
	// Small frames cost almost nothing over a websocket and remove the whole
	// class of failure, which on a screen presents as the connection dying
	// with nothing anywhere naming a size.
	maximumFrameBytes = 16 << 10
)

type controlFrame struct {
	Stream string `json:"stream"`
	Kind   string `json:"kind"`
	Host   string `json:"host,omitempty"`
	Port   int    `json:"port,omitempty"`
	Error  string `json:"error,omitempty"`
}

const (
	kindOpen   = "open"
	kindOpened = "opened"
	kindFailed = "failed"
	kindClose  = "close"
)

// tunnel is one live connection to the service.
type tunnel struct {
	connection *websocket.Conn

	// One writer. A websocket connection allows only one writer at a time,
	// and the streams write concurrently.
	writeMutex sync.Mutex

	mutex   sync.Mutex
	streams map[string]*stream
	closed  bool
	trouble error
}

// dial opens the connection and starts reading it.
func dial(ctx context.Context, address, credential string) (*tunnel, error) {
	endpoint, err := websocketURL(address)
	if err != nil {
		return nil, err
	}

	dialer := &websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+credential)

	connection, response, err := dialer.DialContext(ctx, endpoint, header)
	if err != nil {
		if response != nil {
			// The status is the useful part: 401 means the credential is no
			// longer good, which is worth saying differently from the network
			// being down.
			return nil, fmt.Errorf("service: cannot attach: %s (%w)", response.Status, err)
		}
		return nil, fmt.Errorf("service: cannot attach: %w", err)
	}
	// What may be read is separate from what may be written: the service
	// writes responses at whatever size it likes, and this is only a guard
	// against a reply nothing asked for.
	connection.SetReadLimit(1 << 20)

	self := &tunnel{connection: connection, streams: map[string]*stream{}}
	go self.read()
	return self, nil
}

// websocketURL turns the service address into the address of its device
// websocket, keeping the scheme's security: https becomes wss, http becomes ws.
func websocketURL(address string) (string, error) {
	base, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(address), "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("service: %q is not an address", address)
	}
	switch base.Scheme {
	case "https":
		base.Scheme = "wss"
	case "http":
		base.Scheme = "ws"
	default:
		return "", fmt.Errorf("service: %q is not an address this can attach to", address)
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/api/v1/device/websocket"
	return base.String(), nil
}

// read pumps the connection until it fails, handing each message to the stream
// it belongs to.
func (self *tunnel) read() {
	for {
		kind, payload, err := self.connection.ReadMessage()
		if err != nil {
			self.fail(err)
			return
		}
		switch kind {
		case websocket.TextMessage:
			self.handleControl(payload)
		case websocket.BinaryMessage:
			self.handleData(payload)
		}
	}
}

func (self *tunnel) handleControl(payload []byte) {
	var frame controlFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		log.Debugf("the service sent something undecodable: %s", err)
		return
	}

	// An open from the service is the service asking this device to dial
	// something inside its network. Nothing dials devices yet, so these are
	// ignored -- see the plan. Ignoring one costs the caller a timeout rather
	// than an error, and answering them is strictly additive.
	if frame.Kind == kindOpen {
		log.Debugf("the service asked this device to open a stream, which it does not do yet")
		return
	}

	self.mutex.Lock()
	one := self.streams[frame.Stream]
	self.mutex.Unlock()
	if one == nil {
		return
	}
	one.control(frame)
}

func (self *tunnel) handleData(payload []byte) {
	if len(payload) < 2 {
		return
	}
	length := int(binary.BigEndian.Uint16(payload[:2]))
	if len(payload) < 2+length {
		return
	}
	self.mutex.Lock()
	one := self.streams[string(payload[2:2+length])]
	self.mutex.Unlock()
	if one == nil {
		return
	}
	one.deliver(payload[2+length:])
}

// send writes one message. The connection allows a single writer, and streams
// write from whichever goroutine is making a request.
func (self *tunnel) send(kind int, payload []byte) error {
	self.writeMutex.Lock()
	defer self.writeMutex.Unlock()
	return self.connection.WriteMessage(kind, payload)
}

func (self *tunnel) sendControl(frame controlFrame) error {
	encoded, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return self.send(websocket.TextMessage, encoded)
}

// fail tears down every stream when the connection goes, so that a request in
// flight returns rather than waiting for a reply that cannot arrive.
func (self *tunnel) fail(err error) {
	self.mutex.Lock()
	if self.closed {
		self.mutex.Unlock()
		return
	}
	self.closed = true
	self.trouble = err
	streams := make([]*stream, 0, len(self.streams))
	for _, one := range self.streams {
		streams = append(streams, one)
	}
	self.streams = map[string]*stream{}
	self.mutex.Unlock()

	for _, one := range streams {
		one.fail(err)
	}
	_ = self.connection.Close()
}

// Close ends the connection.
func (self *tunnel) Close() error {
	self.fail(net.ErrClosed)
	return nil
}

// alive reports whether the connection is still usable.
func (self *tunnel) alive() bool {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return !self.closed
}

// open asks the service for a stream to itself and waits for the answer.
func (self *tunnel) open(ctx context.Context) (*stream, error) {
	// The opener chooses the identifier and it must be unique for the life of
	// the connection. A random one cannot collide with the service's own,
	// which is what a counter starting at one might.
	identifier := security.NewIdentifier()

	one := &stream{
		tunnel:     self,
		identifier: identifier,
		answered:   make(chan error, 1),
		incoming:   make(chan []byte, 16),
		done:       make(chan struct{}),
	}

	self.mutex.Lock()
	if self.closed {
		trouble := self.trouble
		self.mutex.Unlock()
		return nil, trouble
	}
	self.streams[identifier] = one
	self.mutex.Unlock()

	if err := self.sendControl(controlFrame{
		Stream: identifier, Kind: kindOpen, Host: tunnelHost, Port: tunnelPort,
	}); err != nil {
		self.forget(identifier)
		return nil, err
	}

	waiting, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	select {
	case err := <-one.answered:
		if err != nil {
			self.forget(identifier)
			return nil, err
		}
		return one, nil
	case <-waiting.Done():
		self.forget(identifier)
		return nil, fmt.Errorf("service: the service never answered the request to open a stream")
	}
}

func (self *tunnel) forget(identifier string) {
	self.mutex.Lock()
	delete(self.streams, identifier)
	self.mutex.Unlock()
}

// stream is one conversation over the tunnel, shaped as a net.Conn so that
// Go's own HTTP client can speak over it. That is the whole reason for the
// shape: the service expects ordinary HTTP/1.1 on the stream, and writing an
// HTTP client by hand to avoid implementing five trivial methods would be the
// wrong trade.
type stream struct {
	tunnel     *tunnel
	identifier string

	answered chan error
	incoming chan []byte
	// What is left of the last payload after a short Read.
	leftover []byte

	closeOnce sync.Once
	done      chan struct{}

	troubleMutex sync.Mutex
	trouble      error
}

func (self *stream) control(frame controlFrame) {
	switch frame.Kind {
	case kindOpened:
		select {
		case self.answered <- nil:
		default:
		}
	case kindFailed:
		reason := frame.Error
		if reason == "" {
			reason = "the service refused the stream"
		}
		select {
		case self.answered <- errors.New("service: " + reason):
		default:
		}
	case kindClose:
		self.fail(io.EOF)
	}
}

func (self *stream) deliver(payload []byte) {
	// Copied: the read pump reuses its buffer for the next message.
	kept := make([]byte, len(payload))
	copy(kept, payload)
	select {
	case self.incoming <- kept:
	case <-self.done:
	}
}

func (self *stream) fail(err error) {
	self.troubleMutex.Lock()
	if self.trouble == nil {
		self.trouble = err
	}
	self.troubleMutex.Unlock()
	self.closeOnce.Do(func() { close(self.done) })
}

func (self *stream) why() error {
	self.troubleMutex.Lock()
	defer self.troubleMutex.Unlock()
	if self.trouble == nil {
		return io.EOF
	}
	return self.trouble
}

func (self *stream) Read(into []byte) (int, error) {
	if len(self.leftover) > 0 {
		taken := copy(into, self.leftover)
		self.leftover = self.leftover[taken:]
		return taken, nil
	}
	select {
	case payload := <-self.incoming:
		taken := copy(into, payload)
		if taken < len(payload) {
			self.leftover = payload[taken:]
		}
		return taken, nil
	case <-self.done:
		// Anything already delivered is read before the end is reported, or a
		// response that arrived just as the stream closed would be lost.
		select {
		case payload := <-self.incoming:
			taken := copy(into, payload)
			if taken < len(payload) {
				self.leftover = payload[taken:]
			}
			return taken, nil
		default:
		}
		return 0, self.why()
	}
}

func (self *stream) Write(from []byte) (int, error) {
	select {
	case <-self.done:
		return 0, self.why()
	default:
	}

	// One frame per write, and the caller's writes are already small: Go's
	// HTTP client buffers a body at four kilobytes. Anything larger is split
	// rather than risking the connection, because the service enforces its
	// limit by closing rather than by refusing.
	written := 0
	for written < len(from) {
		end := written + maximumFrameBytes - len(self.identifier) - 2
		if end > len(from) {
			end = len(from)
		}
		frame := make([]byte, 2+len(self.identifier)+(end-written))
		binary.BigEndian.PutUint16(frame[:2], uint16(len(self.identifier)))
		copy(frame[2:], self.identifier)
		copy(frame[2+len(self.identifier):], from[written:end])
		if err := self.tunnel.send(websocket.BinaryMessage, frame); err != nil {
			self.fail(err)
			return written, err
		}
		written = end
	}
	return written, nil
}

func (self *stream) Close() error {
	self.closeOnce.Do(func() {
		close(self.done)
		_ = self.tunnel.sendControl(controlFrame{Stream: self.identifier, Kind: kindClose})
		self.tunnel.forget(self.identifier)
	})
	return nil
}

// The rest of net.Conn. Deadlines are not honoured: every caller here is an
// HTTP request made with a context, which is the deadline that matters, and
// pretending to support two would be worse than supporting one.
func (self *stream) LocalAddr() net.Addr              { return tunnelAddress{} }
func (self *stream) RemoteAddr() net.Addr             { return tunnelAddress{} }
func (self *stream) SetDeadline(time.Time) error      { return nil }
func (self *stream) SetReadDeadline(time.Time) error  { return nil }
func (self *stream) SetWriteDeadline(time.Time) error { return nil }

type tunnelAddress struct{}

func (tunnelAddress) Network() string { return "cue" }
func (tunnelAddress) String() string  { return tunnelHost }
