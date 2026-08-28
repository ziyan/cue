package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// Closing the menu sends the tab back to the page it came from, and that
// address arrives on the query string from the page itself.
//
// So it is checked before it is used. Without the check a page on the screen
// could send the menu to a javascript: address — and the menu is served by
// this daemon, so that would be somebody else's code running with the daemon's
// own origin, which is the origin everything here trusts.
func TestTheMenuOnlyGoesBackToAnOrdinaryAddress(t *testing.T) {
	server := newTestServer(t, config.Default())

	request := httptest.NewRequest(http.MethodGet,
		"/menu?from="+"javascript:alert(1)", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("the menu answered %d", response.Code)
	}
	body := response.Body.String()

	// The page must decide this at the moment it navigates, on the value it
	// reads then — not by having the daemon paste it into the markup.
	if strings.Contains(body, "javascript:alert(1)") {
		t.Error("the address the page supplied was written into the menu itself")
	}
	if !strings.Contains(body, `/^https?:\/\//i.test(from)`) {
		t.Error("the menu does not check the address before going back to it")
	}
}
