package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// Not a test: writes the menu as a device that has an owner shows it.
func TestWriteLockedMenu(t *testing.T) {
	where := os.Getenv("CUE_LOCKED_MENU")
	if where == "" {
		t.Skip("set CUE_LOCKED_MENU to write the page somewhere")
	}
	server := newTestServer(t, config.Default())
	signedIn(t, server)

	request := httptest.NewRequest(http.MethodGet, "/menu", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if err := os.WriteFile(where, response.Body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d bytes", response.Body.Len())
}
