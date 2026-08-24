package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ziyan/cue/internal/util/deferutil"
)

// Session is a WebSocket connection to one tab. Commands are sent with an
// identifier and replies come back out of order, so a session runs a reader
// that matches each reply to the caller waiting for it.
type Session struct {
	connection *websocket.Conn

	writeMutex sync.Mutex

	mutex     sync.Mutex
	nextId    int64
	pending   map[int64]chan message
	closed    bool
	closeErr  error
	closeOnce sync.Once
	done      chan struct{}
}

// message is one frame of the protocol, in either direction. A reply carries
// the identifier of the command it answers; an event carries a method name and
// no identifier.
type message struct {
	Identifier int64           `json:"id,omitempty"`
	Method     string          `json:"method,omitempty"`
	Parameters json.RawMessage `json:"params,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      *protocolError  `json:"error,omitempty"`
}

type protocolError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (self *protocolError) Error() string {
	if self.Data != "" {
		return fmt.Sprintf("%s (%s)", self.Message, self.Data)
	}
	return self.Message
}

// Attach opens a session to a tab.
func (self *Client) Attach(ctx context.Context, target Target) (*Session, error) {
	if target.WebSocketDebuggerURL == "" {
		return nil, fmt.Errorf("cdp: the tab %s has no debugging connection, which means something else is already attached to it", target.Identifier)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		// The protocol sends whole documents in one frame — a screenshot is a
		// base64 PNG — so the read limit has to be generous.
		ReadBufferSize:  64 * 1024,
		WriteBufferSize: 64 * 1024,
	}
	connection, response, err := dialer.DialContext(ctx, target.WebSocketDebuggerURL, nil)
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("cdp: cannot attach to the tab %s: %s: %w", target.Identifier, response.Status, err)
		}
		return nil, fmt.Errorf("cdp: cannot attach to the tab %s: %w", target.Identifier, err)
	}
	connection.SetReadLimit(64 * 1024 * 1024)

	session := &Session{
		connection: connection,
		pending:    map[int64]chan message{},
		done:       make(chan struct{}),
	}
	go func() {
		defer deferutil.Recover()
		session.read()
	}()
	return session, nil
}

// Closed reports whether this session's connection has gone. A caller that
// keeps sessions — the browser keeps one per tab, because the rules are
// evaluated every few seconds — has to ask, or a tab whose renderer crashed
// leaves a dead connection cached and every call on it fails forever.
func (self *Session) Closed() bool {
	select {
	case <-self.done:
		return true
	default:
	}
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.closed
}

// Close ends the session. Everything waiting for a reply is released.
func (self *Session) Close() {
	self.closeOnce.Do(func() {
		_ = self.connection.Close()
		close(self.done)
	})
}

// Call sends a command and waits for its reply. Passing nil for result
// discards it.
func (self *Session) Call(ctx context.Context, method string, parameters interface{}, result interface{}) error {
	self.mutex.Lock()
	if self.closed {
		err := self.closeErr
		self.mutex.Unlock()
		if err == nil {
			err = fmt.Errorf("the session is closed")
		}
		return fmt.Errorf("cdp: %s: %w", method, err)
	}
	self.nextId++
	identifier := self.nextId
	reply := make(chan message, 1)
	self.pending[identifier] = reply
	self.mutex.Unlock()

	defer func() {
		self.mutex.Lock()
		delete(self.pending, identifier)
		self.mutex.Unlock()
	}()

	outgoing := map[string]interface{}{"id": identifier, "method": method}
	if parameters != nil {
		outgoing["params"] = parameters
	}
	encoded, err := json.Marshal(outgoing)
	if err != nil {
		return fmt.Errorf("cdp: %s: %w", method, err)
	}

	if err := self.write(ctx, encoded); err != nil {
		return fmt.Errorf("cdp: %s: %w", method, err)
	}

	select {
	case incoming, delivered := <-reply:
		if !delivered {
			// The reader closed every pending reply channel because the
			// connection has gone. Without this check the zero value would be
			// read as a reply with no error and no result, and a command that
			// wants no result — navigating, reloading, switching tab — would
			// report success on a connection that no longer exists. A kiosk
			// then sits on the wrong page with nothing in any log.
			return fmt.Errorf("cdp: %s: the browser closed the connection", method)
		}
		if incoming.Error != nil {
			return fmt.Errorf("cdp: %s: %w", method, incoming.Error)
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(incoming.Result, result); err != nil {
			return fmt.Errorf("cdp: %s returned something unexpected: %w", method, err)
		}
		return nil
	case <-self.done:
		return fmt.Errorf("cdp: %s: the browser closed the connection", method)
	case <-ctx.Done():
		// A command that does not come back is the signature of a wedged
		// renderer, which is exactly what the watchdog is looking for, so the
		// deadline is reported plainly rather than retried here.
		return fmt.Errorf("cdp: %s: %w", method, ctx.Err())
	}
}

func (self *Session) write(ctx context.Context, data []byte) error {
	self.writeMutex.Lock()
	defer self.writeMutex.Unlock()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	if err := self.connection.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return self.connection.WriteMessage(websocket.TextMessage, data)
}

// read is the one goroutine that touches the connection for reading. It hands
// each reply to whoever is waiting for it and drops events on the floor: this
// daemon asks questions rather than subscribing to anything.
func (self *Session) read() {
	defer func() {
		self.mutex.Lock()
		self.closed = true
		pending := self.pending
		self.pending = map[int64]chan message{}
		self.mutex.Unlock()
		for _, reply := range pending {
			close(reply)
		}
		self.Close()
	}()

	for {
		_, data, err := self.connection.ReadMessage()
		if err != nil {
			self.mutex.Lock()
			self.closeErr = err
			self.mutex.Unlock()
			return
		}

		var incoming message
		if err := json.Unmarshal(data, &incoming); err != nil {
			log.Debugf("cannot decode a message from the browser: %s", err)
			continue
		}
		if incoming.Identifier == 0 {
			continue
		}

		self.mutex.Lock()
		reply, waiting := self.pending[incoming.Identifier]
		self.mutex.Unlock()
		if waiting {
			reply <- incoming
		}
	}
}

// --- the handful of commands this project uses -----------------------------

// WindowBounds is where a browser window is and how big it is.
//
// Every field is omitted when it is zero, which is not tidiness: the protocol
// refuses a request that names both a state and a rectangle, so asking for
// full screen means sending nothing but the state.
type WindowBounds struct {
	Left        int    `json:"left,omitempty"`
	Top         int    `json:"top,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	WindowState string `json:"windowState,omitempty"`
}

// WindowForTarget returns the window one tab is in, and its current bounds.
func (self *Session) WindowForTarget(ctx context.Context, target string) (int, WindowBounds, error) {
	var reply struct {
		WindowId int          `json:"windowId"`
		Bounds   WindowBounds `json:"bounds"`
	}
	parameters := map[string]interface{}{}
	if target != "" {
		parameters["targetId"] = target
	}
	if err := self.Call(ctx, "Browser.getWindowForTarget", parameters, &reply); err != nil {
		return 0, WindowBounds{}, err
	}
	return reply.WindowId, reply.Bounds, nil
}

// SetWindowBounds moves and resizes a browser window.
//
// This is how the window gets to be the size of the screen. It cannot be done
// with a command line flag: --kiosk and --start-fullscreen both work by asking
// the window manager to make the window full screen, and this image has no
// window manager to ask, so the window stays whatever size Chromium opened it
// at. Setting the bounds over the protocol asks nobody and always works.
func (self *Session) SetWindowBounds(ctx context.Context, window int, bounds WindowBounds) error {
	return self.Call(ctx, "Browser.setWindowBounds", map[string]interface{}{
		"windowId": window,
		"bounds":   bounds,
	}, nil)
}

// CreateTarget opens a tab and returns its identifier. Sent on a browser
// session, not a tab session.
func (self *Session) CreateTarget(ctx context.Context, address string) (string, error) {
	var reply struct {
		TargetId string `json:"targetId"`
	}
	if err := self.Call(ctx, "Target.createTarget", map[string]interface{}{"url": address}, &reply); err != nil {
		return "", err
	}
	return reply.TargetId, nil
}

// ActivateTarget brings a tab to the front. In kiosk mode there is nothing to
// see but the front tab, so this is what "show that page now" means.
func (self *Session) ActivateTarget(ctx context.Context, identifier string) error {
	return self.Call(ctx, "Target.activateTarget", map[string]interface{}{"targetId": identifier}, nil)
}

// CloseTarget closes a tab.
func (self *Session) CloseTarget(ctx context.Context, identifier string) error {
	return self.Call(ctx, "Target.closeTarget", map[string]interface{}{"targetId": identifier}, nil)
}

// Navigate loads an address in the tab.
func (self *Session) Navigate(ctx context.Context, address string) error {
	return self.Call(ctx, "Page.navigate", map[string]interface{}{"url": address}, nil)
}

// Reload fetches the page again. Ignoring the cache is the heavier form, used
// when a page is suspected of being stuck on something it cached.
func (self *Session) Reload(ctx context.Context, ignoreCache bool) error {
	return self.Call(ctx, "Page.reload", map[string]interface{}{"ignoreCache": ignoreCache}, nil)
}

// ClearCache empties the browser's HTTP cache. This is a step on the
// watchdog's recovery ladder: a corrupted cache produces a page that will not
// load while everything else looks healthy.
func (self *Session) ClearCache(ctx context.Context) error {
	if err := self.Call(ctx, "Network.enable", nil, nil); err != nil {
		return err
	}
	return self.Call(ctx, "Network.clearBrowserCache", nil, nil)
}

// evaluateResult is the shape Runtime.evaluate answers with.
type evaluateResult struct {
	Result struct {
		Type        string          `json:"type"`
		Subtype     string          `json:"subtype"`
		Value       json.RawMessage `json:"value"`
		Description string          `json:"description"`
	} `json:"result"`
	ExceptionDetails *struct {
		Text      string `json:"text"`
		Exception struct {
			Description string `json:"description"`
		} `json:"exception"`
	} `json:"exceptionDetails"`
}

// Evaluate runs an expression in the page and decodes what it returned into
// result, which may be nil.
//
// awaitPromise makes the call wait for a promise the expression returns, which
// is how the watchdog asks a question that only a page whose rendering is
// still running can answer.
func (self *Session) Evaluate(ctx context.Context, expression string, awaitPromise bool, result interface{}) error {
	var reply evaluateResult
	parameters := map[string]interface{}{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  awaitPromise,
	}
	if err := self.Call(ctx, "Runtime.evaluate", parameters, &reply); err != nil {
		return err
	}
	if reply.ExceptionDetails != nil {
		description := reply.ExceptionDetails.Exception.Description
		if description == "" {
			description = reply.ExceptionDetails.Text
		}
		return fmt.Errorf("cdp: the page raised an exception: %s", description)
	}
	if result == nil || len(reply.Result.Value) == 0 {
		return nil
	}
	if err := json.Unmarshal(reply.Result.Value, result); err != nil {
		return fmt.Errorf("cdp: the page returned something unexpected: %w", err)
	}
	return nil
}

// CurrentURL asks the page what address it is actually on, which is not
// always what it was told to load: the case this whole project exists for is a
// dashboard that redirects itself to a login page hours later.
func (self *Session) CurrentURL(ctx context.Context) (string, error) {
	var address string
	if err := self.Evaluate(ctx, "location.href", false, &address); err != nil {
		return "", err
	}
	return address, nil
}
