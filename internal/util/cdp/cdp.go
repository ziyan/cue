// Package cdp speaks the Chrome DevTools Protocol: the interface Chromium
// exposes on a local port when it is started with --remote-debugging-port,
// and the same one the browser's own developer tools use.
//
// The daemon drives the browser through this rather than through a browser
// extension. An extension has to be installed into the profile, configured by
// a file written next to it, and reloaded when that file changes, and it can
// only see what a page allows it to see. Speaking the protocol directly puts
// the whole policy — which page is shown, when it is reloaded, how it is
// logged in, whether it is still alive — in one place, in Go, where it can be
// changed while the daemon is running and tested without a browser.
//
// Two transports are involved and the difference matters:
//
//   - A small HTTP interface on the debugging port lists the open tabs and
//     opens, closes and activates them. It needs no connection state, so the
//     daemon uses it for everything it can.
//   - A WebSocket per tab carries the protocol proper, for the things the
//     HTTP interface cannot do: navigating, evaluating JavaScript, capturing
//     a screenshot, clearing the cache.
package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("cdp")

// Client talks to one browser's debugging interface.
type Client struct {
	address    string
	httpClient *http.Client
}

// Target is one thing the browser has open. A "page" target is a tab; the
// browser also reports its own service workers and extension backgrounds,
// which the daemon ignores.
type Target struct {
	Identifier           string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// IsPage reports whether this target is a tab, as opposed to one of the
// browser's internal targets.
func (self Target) IsPage() bool {
	return self.Type == "page"
}

// Version is what the browser says about itself, and the first thing the
// daemon asks for: a reply to this is proof the browser is up and listening.
type Version struct {
	Browser              string `json:"Browser"`
	ProtocolVersion      string `json:"Protocol-Version"`
	UserAgent            string `json:"User-Agent"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// New returns a client for a browser listening on the given address, which is
// always on the loopback interface.
func New(address string) *Client {
	return &Client{
		address: address,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				// The debugging port is on this machine, so there is no
				// reason to keep more than a couple of connections or to wait
				// long for one.
				MaxIdleConns:        4,
				IdleConnTimeout:     30 * time.Second,
				DialContext:         (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
				TLSHandshakeTimeout: 3 * time.Second,
			},
		},
	}
}

// Address is where this client is pointed.
func (self *Client) Address() string {
	return self.address
}

// Version asks the browser what it is. It is the readiness check: the port
// accepts connections a moment before the browser will answer on it.
func (self *Client) Version(ctx context.Context) (*Version, error) {
	var version Version
	if err := self.get(ctx, "/json/version", &version); err != nil {
		return nil, err
	}
	return &version, nil
}

// Targets lists everything the browser has open.
func (self *Client) Targets(ctx context.Context) ([]Target, error) {
	var targets []Target
	if err := self.get(ctx, "/json/list", &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

// Pages lists the tabs, in the order the browser reports them.
func (self *Client) Pages(ctx context.Context) ([]Target, error) {
	targets, err := self.Targets(ctx)
	if err != nil {
		return nil, err
	}
	pages := make([]Target, 0, len(targets))
	for _, target := range targets {
		if target.IsPage() {
			pages = append(pages, target)
		}
	}
	return pages, nil
}

// AttachBrowser opens a session to the browser itself rather than to a tab.
// Tabs are created, activated and closed through it, because the protocol's
// own commands take proper JSON parameters, whereas the equivalent HTTP
// endpoints take the address as a bare query string and disagree between
// versions about how it should be escaped.
func (self *Client) AttachBrowser(ctx context.Context) (*Session, error) {
	version, err := self.Version(ctx)
	if err != nil {
		return nil, err
	}
	if version.WebSocketDebuggerURL == "" {
		return nil, fmt.Errorf("cdp: the browser did not offer a debugging connection")
	}
	return self.Attach(ctx, Target{Identifier: "browser", WebSocketDebuggerURL: version.WebSocketDebuggerURL})
}

func (self *Client) get(ctx context.Context, path string, result interface{}) error {
	return self.do(ctx, http.MethodGet, path, result)
}

func (self *Client) do(ctx context.Context, method, path string, result interface{}) error {
	address := "http://" + self.address + path
	request, err := http.NewRequestWithContext(ctx, method, address, nil)
	if err != nil {
		return fmt.Errorf("cdp: %w", err)
	}
	// Chromium refuses a request whose Host header is not a literal address,
	// as a defence against a web page reaching the debugging port by name.
	request.Host = self.address

	response, err := self.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("cdp: %s %s: %w", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024))
	if err != nil {
		return fmt.Errorf("cdp: %s %s: %w", method, path, err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("cdp: %s %s: %s: %s", method, path, response.Status, strings.TrimSpace(string(body)))
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("cdp: %s %s returned something that is not the expected JSON: %w", method, path, err)
	}
	return nil
}
