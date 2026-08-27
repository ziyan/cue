package web

import (
	"net/http"
	"net/http/httptest"
	"os"
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
