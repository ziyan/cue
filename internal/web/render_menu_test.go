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

// The linking code has a size, and the panel holding it has a layout.
//
// Both were missing and both were invisible in review. An SVG carrying only a
// viewBox has no dimensions of its own, so an img with no width and no height
// drew as nothing -- the picture was fetched, was valid, and the screen showed
// an empty gap where the thing somebody had come to scan should have been. And
// the panel was left out of the rule that gives every other one a column with
// a gap, so its contents sat directly on top of each other.
func TestTheLinkingPanelIsLaidOut(t *testing.T) {
	server := newTestServer(t, config.Default())
	request := httptest.NewRequest(http.MethodGet, "/menu", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	page := response.Body.String()

	// A size for the code. Without one it is fetched and not seen.
	if !strings.Contains(page, "#link-code {") {
		t.Error("the linking code has no rule of its own, so it has no size")
	}
	for _, needed := range []string{"width:", "height:"} {
		rule := page[strings.Index(page, "#link-code {"):]
		rule = rule[:strings.Index(rule, "}")]
		if !strings.Contains(rule, needed) {
			t.Errorf("the linking code has no %s, so it draws as nothing", strings.TrimSuffix(needed, ":"))
		}
	}

	// And the panel is laid out like the others, rather than being the one
	// that was forgotten.
	layout := page[strings.Index(page, "#network, #wireless"):]
	layout = layout[:strings.Index(layout, "}")]
	if !strings.Contains(layout, "#link") {
		t.Error("the linking panel is not in the rule that lays every other panel out")
	}

	// The address is not shown. Nobody types sixty characters off a wall, and
	// it overflowed the panel when it was there.
	if strings.Contains(page, `id="link-url"`) {
		t.Error("the linking address is on the screen, where it is no use to anybody")
	}

	// And the picture is fetched rather than pointed at. The endpoint wants
	// this screen's pass in a header, which an img cannot send, so a src
	// aimed straight at it is refused every time -- see
	// TestTheScreensCodeIsRefusedWithoutTheHeader.
	if strings.Contains(page, `linkCode.src = "/api`) {
		t.Error("the code is pointed at rather than fetched, so it will never load")
	}
	if !strings.Contains(page, "async function drawCode") {
		t.Error("nothing fetches the code with the pass this page holds")
	}
}
