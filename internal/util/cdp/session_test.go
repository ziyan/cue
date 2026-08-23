package cdp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A browser that answers, so that the client can be exercised without one.
func newStubBrowser(t *testing.T, handle func(*websocket.Conn)) *Client {
	t.Helper()

	router := http.NewServeMux()
	var address string

	router.HandleFunc("/json/version", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"Browser":"Chrome/stub","webSocketDebuggerUrl":"ws://` + address + `/devtools/browser"}`))
	})
	router.HandleFunc("/json/list", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`[{"id":"tab-1","type":"page","title":"a page","url":"https://example.com/",
			"webSocketDebuggerUrl":"ws://` + address + `/devtools/page/tab-1"}]`))
	})
	router.HandleFunc("/devtools/", func(response http.ResponseWriter, request *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		connection, err := upgrader.Upgrade(response, request, nil)
		if err != nil {
			return
		}
		handle(connection)
	})

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	address = strings.TrimPrefix(server.URL, "http://")

	return New(address)
}

func TestASessionKnowsWhenItsConnectionHasGone(t *testing.T) {
	// This is what stops a tab whose renderer crashed from leaving a dead
	// connection cached, with every rule and every probe on it failing
	// forever while the browser itself is perfectly healthy.
	closed := make(chan struct{})
	client := newStubBrowser(t, func(connection *websocket.Conn) {
		<-closed
		_ = connection.Close()
	})

	pages, err := client.Pages(context.Background())
	if err != nil {
		t.Fatalf("pages: %s", err)
	}
	if len(pages) != 1 {
		t.Fatalf("the stub offered %d pages, want 1", len(pages))
	}

	session, err := client.Attach(context.Background(), pages[0])
	if err != nil {
		t.Fatalf("attach: %s", err)
	}
	defer session.Close()

	if session.Closed() {
		t.Fatal("a fresh session reports itself closed")
	}

	close(closed)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if session.Closed() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("the session did not notice its connection had gone")
}

func TestACallOnAClosedSessionFailsRatherThanHanging(t *testing.T) {
	client := newStubBrowser(t, func(connection *websocket.Conn) {
		_ = connection.Close()
	})

	pages, err := client.Pages(context.Background())
	if err != nil {
		t.Fatalf("pages: %s", err)
	}
	session, err := client.Attach(context.Background(), pages[0])
	if err != nil {
		t.Fatalf("attach: %s", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	started := time.Now()
	err = session.Call(ctx, "Runtime.evaluate", map[string]interface{}{"expression": "1+1"}, nil)
	if err == nil {
		t.Fatal("a call on a dead connection should fail")
	}
	if time.Since(started) > 4*time.Second {
		t.Errorf("the call took %s to fail; it should not wait out the deadline", time.Since(started))
	}
}

func TestVersionIsTheReadinessCheck(t *testing.T) {
	client := newStubBrowser(t, func(*websocket.Conn) {})
	version, err := client.Version(context.Background())
	if err != nil {
		t.Fatalf("version: %s", err)
	}
	if version.Browser != "Chrome/stub" {
		t.Errorf("the browser is %q", version.Browser)
	}
}

func TestOnlyPagesAreReportedAsPages(t *testing.T) {
	client := newStubBrowser(t, func(*websocket.Conn) {})
	pages, err := client.Pages(context.Background())
	if err != nil {
		t.Fatalf("pages: %s", err)
	}
	for _, page := range pages {
		if !page.IsPage() {
			t.Errorf("%s is a %q, which is not a tab", page.Identifier, page.Type)
		}
	}
}
