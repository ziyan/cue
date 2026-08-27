package web

import (
	"os"
	"testing"
)

// Not a test: a way to look at the setup portal while working on it.
func TestWritePortalPage(t *testing.T) {
	where := os.Getenv("CUE_PORTAL_PAGE")
	if where == "" {
		t.Skip("set CUE_PORTAL_PAGE to write the page somewhere")
	}
	server, _ := setupServer(t, true)
	body := do(server, "GET", "/portal", nil, nil).Body.String()
	if err := os.WriteFile(where, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d bytes to %s", len(body), where)
}
