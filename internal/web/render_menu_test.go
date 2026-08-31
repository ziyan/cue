package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// Not a test: a way to look at the menu while working on it.
func TestWriteMenuPage(t *testing.T) {
	where := os.Getenv("CUE_MENU_PAGE")
	if where == "" {
		t.Skip("set CUE_MENU_PAGE to write the page somewhere")
	}
	server := newTestServer(t, config.Default())
	request := httptest.NewRequest(http.MethodGet, "/menu", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if err := os.WriteFile(where, response.Body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d bytes", response.Body.Len())
}

// The way out of the menu has to survive a press that achieves nothing.
//
// Closing is not guaranteed: the daemon moves the tab and may not, and the
// fallback needs either an address to return to or a history to go back
// through, neither of which a tab whose history has been reset has. When the
// close was guarded by a flag that was set once and never cleared, the menu
// stayed on the screen and every press afterwards did nothing at all -- the X
// was dead and the only way out was restarting the browser.
func TestTheMenuCloseSurvivesAPressThatDoesNothing(t *testing.T) {
	server := newTestServer(t, config.Default())
	request := httptest.NewRequest(http.MethodGet, "/menu", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("the menu answered %d", response.Code)
	}
	page := response.Body.String()

	// The requests must not be repeated, so something guards them.
	if !strings.Contains(page, "if (!closing)") {
		t.Error("nothing stops the closing requests being sent twice")
	}
	// But the guard must not be an early return out of the whole function,
	// which is what made the button dead.
	if strings.Contains(page, "if (closing) return") {
		t.Error("closing returns early, so a press that achieves nothing disables the button for good")
	}
	// And the button is given back if the page is still here afterwards.
	if !strings.Contains(page, `removeAttribute("aria-disabled")`) {
		t.Error("the button is never re-enabled, so a failed close leaves a dead X")
	}
}
