package web

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ziyan/cue/internal/util/deferutil"
)

const (
	// vncPingInterval keeps a quiet connection punched through whatever is
	// between the browser and the device: a proxy's idle timeout, a NAT
	// table, a corporate firewall.
	vncPingInterval = 30 * time.Second

	// vncPongWait is how long to wait for the browser to answer a ping before
	// the connection is treated as dead. Two and a half pings, so that losing
	// one does not tear down a healthy session.
	vncPongWait = 75 * time.Second

	// vncWriteDeadline bounds a single frame, so that a viewer that has
	// stopped reading cannot wedge the goroutine feeding it.
	vncWriteDeadline = 10 * time.Second
)

// vnc bridges an authenticated WebSocket to the VNC server on the loopback
// address, which is what lets a browser tab be the viewer.
//
// The VNC server does not listen on the network. This is on purpose: VNC's own
// authentication is a challenge over DES with an eight character password, and
// exposing it would mean the picture of the screen — and control of it — was
// protected by that. Coming through here instead means watching the screen
// needs the same session as changing what is on it.
func (self *Server) vnc(response http.ResponseWriter, request *http.Request) {
	address := self.device.VNCAddress()
	if host, port, err := net.SplitHostPort(address); err == nil && (host == "" || host == "0.0.0.0" || host == "::") {
		address = net.JoinHostPort("127.0.0.1", port)
	}

	stream, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "the VNC server is not running: "+err.Error())
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: self.isOriginAllowed,
		// noVNC asks for this subprotocol, and refusing to name it makes some
		// versions fall back to a mode this bridge does not implement.
		Subprotocols:    []string{"binary"},
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 32 * 1024,
	}
	connection, err := upgrader.Upgrade(response, request, nil)
	if err != nil {
		// Upgrade has already written a response by this point.
		log.Warningf("cannot upgrade a VNC connection from %s: %s", clientAddress(request), err)
		_ = stream.Close()
		return
	}

	log.Noticef("a viewer from %s is watching the screen", clientAddress(request))
	defer log.Noticef("the viewer from %s has stopped watching", clientAddress(request))

	bridge(connection, stream)
}

// bridge shuttles bytes between the WebSocket and the VNC server until either
// side closes.
func bridge(connection *websocket.Conn, stream net.Conn) {
	defer func() { _ = connection.Close() }()
	defer func() { _ = stream.Close() }()

	_ = connection.SetReadDeadline(time.Now().Add(vncPongWait))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(vncPongWait))
	})

	// One mutex around every write: the frames carrying screen updates and
	// the ping control frames come from different goroutines, and the
	// WebSocket library allows only one writer at a time.
	var writeMutex sync.Mutex

	var waitGroup sync.WaitGroup
	waitGroup.Add(3)

	go func() {
		defer deferutil.Recover()
		defer waitGroup.Done()
		ticker := time.NewTicker(vncPingInterval)
		defer ticker.Stop()
		for range ticker.C {
			writeMutex.Lock()
			err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(vncWriteDeadline))
			writeMutex.Unlock()
			if err != nil {
				return
			}
		}
	}()

	// The screen to the browser.
	go func() {
		defer deferutil.Recover()
		defer waitGroup.Done()
		defer func() { _ = connection.Close() }()
		defer func() { _ = stream.Close() }()

		buffer := make([]byte, 32*1024)
		for {
			count, err := stream.Read(buffer)
			if count > 0 {
				writeMutex.Lock()
				_ = connection.SetWriteDeadline(time.Now().Add(vncWriteDeadline))
				writeErr := connection.WriteMessage(websocket.BinaryMessage, buffer[:count])
				writeMutex.Unlock()
				if writeErr != nil {
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.Debugf("reading from the VNC server: %s", err)
				}
				return
			}
		}
	}()

	// The keyboard and mouse to the screen.
	go func() {
		defer deferutil.Recover()
		defer waitGroup.Done()
		defer func() { _ = connection.Close() }()
		defer func() { _ = stream.Close() }()

		for {
			messageType, data, err := connection.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Debugf("reading from the viewer: %s", err)
				}
				return
			}
			if messageType != websocket.BinaryMessage {
				continue
			}
			if _, err := stream.Write(data); err != nil {
				return
			}
		}
	}()

	waitGroup.Wait()
}

// isOriginAllowed decides whether a page is allowed to open this WebSocket.
//
// The default in the WebSocket library is to allow any origin, which would
// mean a page on the internet, open in a browser that has a session cookie for
// this device, could watch and drive the screen. So: same host as the request,
// or an origin the operator has listed.
func (self *Server) isOriginAllowed(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		// Not a browser. noVNC always sends one; a command line client does
		// not, and cannot be tricked by a page.
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(parsed.Host, request.Host) {
		return true
	}

	for _, allowed := range self.store.Current().Web.TrustedOrigins {
		if strings.EqualFold(allowed, origin) || strings.EqualFold(allowed, parsed.Host) {
			return true
		}
	}

	log.Warningf("refused a VNC connection from the origin %q; add it to web.trustedOrigins if that is a proxy in front of this device", origin)
	return false
}
