package web

import (
	"os"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// Not a test: writes the injected script out so it can be looked at on a page.
func TestWriteWayBackScript(t *testing.T) {
	where := os.Getenv("CUE_WAYBACK_SCRIPT")
	if where == "" {
		t.Skip("set CUE_WAYBACK_SCRIPT to write the script somewhere")
	}
	server := newTestServer(t, config.Default())
	if err := os.WriteFile(where, []byte(server.WayBackScript()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d bytes", len(server.WayBackScript()))
}
