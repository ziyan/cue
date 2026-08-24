package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// The real thing, taken off the device it was found on: Chromium's zoom is
// logarithmic, and 1.2 to the power of -1.5778829311823859 is three quarters.
const rememberedZoom = `{"partition":{"per_host_zoom_levels":{"x":{"192.0.2.254":` +
	`{"last_modified":"13432000007785477","zoom_level":-1.5778829311823859}}}},` +
	`"profile":{"exit_type":"Normal","exited_cleanly":true}}`

func TestARememberedZoomIsRemoved(t *testing.T) {
	// One keystroke sets it — ctrl and minus, or ctrl and a scroll wheel —
	// and on a screen on a wall nobody is standing there to notice. It
	// survives every restart and shows up nowhere: the window is the right
	// size, the screen is the right size, and the page is drawn shrunk into a
	// corner with black down two sides.
	browser := newTestBrowser(t, nil)
	directory := filepath.Join(browser.profileDirectory(), "Default")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(directory, "Preferences")
	if err := os.WriteFile(filename, []byte(rememberedZoom), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := browser.clearZoomLevels(); err != nil {
		t.Fatalf("clearing the zoom failed: %s", err)
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "zoom_level") {
		t.Errorf("the zoom is still there:\n%s", content)
	}
	// And the rest of the file has to survive: the crash flag lives here too,
	// and losing it puts a "didn't shut down correctly" bar across the screen.
	var preferences map[string]interface{}
	if err := json.Unmarshal(content, &preferences); err != nil {
		t.Fatalf("the file is no longer valid JSON: %s", err)
	}
	profile, _ := preferences["profile"].(map[string]interface{})
	if profile["exit_type"] != "Normal" {
		t.Errorf("the rest of the preferences was lost: %v", preferences)
	}
}

func TestAProfileWithNoZoomIsLeftAlone(t *testing.T) {
	browser := newTestBrowser(t, nil)
	directory := filepath.Join(browser.profileDirectory(), "Default")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(directory, "Preferences")
	const original = `{"profile":{"exit_type":"Normal"}}`
	if err := os.WriteFile(filename, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := browser.clearZoomLevels(); err != nil {
		t.Fatal(err)
	}

	content, _ := os.ReadFile(filename)
	if string(content) != original {
		t.Errorf("a profile with no zoom was rewritten anyway:\n%s", content)
	}
}

func TestNoProfileYetIsNotAnError(t *testing.T) {
	browser := newTestBrowser(t, nil)
	if err := browser.clearZoomLevels(); err != nil {
		t.Errorf("a first run reported an error: %s", err)
	}
}
