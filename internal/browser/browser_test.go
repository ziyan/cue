package browser

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestThePortComesFromTheBrowserItself(t *testing.T) {
	// The address the daemon connects to is whatever the browser wrote in its
	// own profile, because that is the only number that cannot belong to
	// somebody else. There is no setting for it, which is the point: when
	// there was one, fixing it at 9222 made the daemon drive another
	// container's browser, and changing the default to 0 fixed new devices
	// and did nothing at all for the one already deployed, whose file still
	// said 9222.
	browser := newTestBrowser(t, nil)

	if err := os.MkdirAll(browser.profileDirectory(), 0o755); err != nil {
		t.Fatal(err)
	}
	const chosen = 41735
	if err := os.WriteFile(browser.activePortFilename(),
		[]byte(strconv.Itoa(chosen)+"\n/devtools/browser/abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	client, err := browser.resolveClient()
	if err != nil {
		t.Fatalf("could not work out where the browser is: %s", err)
	}
	if address := client.Address(); !strings.Contains(address, strconv.Itoa(chosen)) {
		t.Errorf("connected to %s, which is not the port the browser wrote (%d)", address, chosen)
	}
	if address := client.Address(); strings.Contains(address, "9222") {
		t.Errorf("connected to %s — the configured port, which may be anybody's browser", address)
	}
}

func TestWithoutThePortFileThereIsNoBrowserToDrive(t *testing.T) {
	// Falling back to the configured port here is what caused the fault: a
	// browser that has not started yet, and a port that answers, is exactly
	// the case where the daemon must refuse rather than connect.
	browser := newTestBrowser(t, nil)

	if _, err := browser.resolveClient(); err == nil {
		t.Fatal("resolved a browser with no DevToolsActivePort file, which can only be somebody else's")
	}
}
