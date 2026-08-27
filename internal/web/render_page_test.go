package web

import (
	"os"

	"github.com/ziyan/cue/internal/network"
	"testing"
)

// Not a test: a way to look at the welcome page while working on it.
// Run with: go test ./internal/web/ -run TestWriteWelcomePage -page /tmp/welcome.html
func TestWriteWelcomePage(t *testing.T) {
	where := os.Getenv("CUE_WELCOME_PAGE")
	if where == "" {
		t.Skip("set CUE_WELCOME_PAGE to write the page somewhere")
	}
	server := newTestServer(t, defaultConfigurationForTest())
	if os.Getenv("CUE_ONBOARDING") != "" {
		device := server.device.(*fakeDevice)
		device.setupNetwork = network.Credentials{SSID: "cue-4k2p9x", Passphrase: "hd7Rk2m9Qw4x"}
		device.onboarding = true
	}
	body := do(server, "GET", "/welcome", nil, nil).Body.String()
	if err := os.WriteFile(where, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d bytes to %s", len(body), where)
}
