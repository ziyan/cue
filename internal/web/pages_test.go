package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/cdp"
)

// TestEveryPageRendersWithoutFaulting opens the interface in the browser the
// image ships and walks every page of it.
//
// It exists because the two checks that came before it cannot see this class
// of mistake. Go does not read the interface's JavaScript at all, and
// "node --check" only parses it: a name that is used and never declared is
// perfectly good syntax, so a reference to a variable that an edit deleted
// parses clean and then throws the moment the page is drawn. That has now
// reached a device three times — once as a stray word left inside an
// expression, twice as code removed along with the lines around it. The only
// check that sees it is running the page.
//
// A fault is an exception that nothing caught: window.onerror for the
// synchronous ones and unhandledrejection for the rest, which is where a
// throw inside an awaited render lands. Console noise is deliberately not a
// fault — the pages talk to a daemon that is only half there in a test, and
// failing on that would make this test cry wolf until somebody turned it off.

// pagesTheInterfaceHas reads the page list out of the interface's own source,
// so that a page added there is tested here without anybody remembering to add
// it twice.
//
// It was a written list, and a written list is how the Upgrade page was added
// with this test passing over it. That is the same shape as the fault this
// whole test exists for: the interface had a page nobody had opened, and
// nothing said so.
func pagesTheInterfaceHas(t *testing.T) []string {
	t.Helper()

	source, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("cannot read the interface's own source: %s", err)
	}

	found := regexp.MustCompile(`\{\s*path:\s*"([^"]*)"`).FindAllStringSubmatch(string(source), -1)
	if len(found) == 0 {
		t.Fatal("no pages found in app.js; has the page list changed shape?")
	}

	paths := make([]string, 0, len(found))
	for _, one := range found {
		paths = append(paths, one[1])
	}
	return paths
}

func TestEveryPageRendersWithoutFaulting(t *testing.T) {
	executable := findBrowser()
	if executable == "" {
		t.Skip("no Chromium on this machine; the image has one and CI runs this there")
	}

	server := newTestServer(t, config.Default())
	if response := do(server, "POST", "/api/v1/setup", map[string]string{"password": testPassword}, nil); response.Code != 200 {
		t.Fatalf("setting the device up returned %d: %s", response.Code, response.Body)
	}

	site := httptest.NewServer(server.router)
	defer site.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	session, stop := startBrowser(ctx, t, executable)
	defer stop()

	// The collector has to be in place before the interface's own code runs,
	// so it is installed as something the browser evaluates on every new
	// document rather than after the page is already up.
	if err := session.Call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]interface{}{
		"source": faultCollector,
	}, nil); err != nil {
		t.Fatalf("installing the fault collector: %s", err)
	}
	for _, method := range []string{"Page.enable", "Runtime.enable"} {
		if err := session.Call(ctx, method, map[string]interface{}{}, nil); err != nil {
			t.Fatalf("%s: %s", method, err)
		}
	}

	if err := session.Call(ctx, "Page.navigate", map[string]interface{}{"url": site.URL}, nil); err != nil {
		t.Fatalf("navigating to the interface: %s", err)
	}
	if err := waitFor(ctx, session, "!!window.__faults"); err != nil {
		t.Fatalf("the interface never loaded: %s", err)
	}

	// Signing in through the same call the sign-in box makes, so that the
	// pages behind it are the ones an operator sees and not the gate.
	signIn := fmt.Sprintf(`fetch("/api/v1/session", {
		method: "POST",
		headers: {"Content-Type": "application/json"},
		body: JSON.stringify({password: %q}),
	}).then((response) => response.status)`, testPassword)
	var status float64
	if err := evaluate(ctx, session, signIn, true, &status); err != nil {
		t.Fatalf("signing in: %s", err)
	}
	if status != 200 {
		t.Fatalf("signing in returned %v, want 200", status)
	}

	for _, path := range pagesTheInterfaceHas(t) {
		name := path
		if name == "" {
			name = "overview"
		}
		// Reloading rather than only moving the hash, so that every page is
		// drawn from nothing the way it is when somebody opens a link to it.
		address := site.URL + "/#/" + path
		if err := evaluate(ctx, session, fmt.Sprintf(`location.href = %q; location.reload(), 0`, address), false, nil); err != nil {
			t.Fatalf("%s: opening the page: %s", name, err)
		}
		if err := waitFor(ctx, session, `!!window.__faults && document.querySelector("main") !== null`); err != nil {
			t.Errorf("%s: the page never drew anything: %s", name, err)
			continue
		}
		// Long enough for what the page fetches on the way up to come back
		// and for anything it throws afterwards to be collected.
		time.Sleep(2 * time.Second)

		var faults []string
		if err := evaluate(ctx, session, "window.__faults", false, &faults); err != nil {
			t.Fatalf("%s: reading the faults back: %s", name, err)
		}
		for _, fault := range faults {
			t.Errorf("%s page: %s", name, fault)
		}
	}
}

// faultCollector is the script the browser runs before the interface does. It
// records what nothing caught, both kinds, and leaves it somewhere the test
// can read.
const faultCollector = `
window.__faults = [];
window.addEventListener("error", (event) => {
  const error = event.error;
  window.__faults.push((error && error.stack) ? String(error.stack) : String(event.message));
});
window.addEventListener("unhandledrejection", (event) => {
  const reason = event.reason;
  window.__faults.push("unhandled rejection: " + ((reason && reason.stack) ? String(reason.stack) : String(reason)));
});
`

func findBrowser() string {
	for _, candidate := range []string{
		"/usr/lib/chromium/chromium",
		"/usr/lib/chromium-browser/chromium-browser",
		"/opt/google/chrome/chrome",
		"/usr/lib/chrome/chrome",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	found, err := exec.LookPath("chromium")
	if err != nil {
		return ""
	}
	return found
}

// startBrowser runs a Chromium of its own — not the daemon's — with a profile
// that is thrown away, and attaches to its first page.
func startBrowser(ctx context.Context, t *testing.T, executable string) (*cdp.Session, func()) {
	t.Helper()
	// Not t.TempDir: the browser keeps writing into its profile for a moment
	// after it is killed, and the framework's own cleanup runs after this
	// test's and trips over the files that appear under it.
	profile, err := os.MkdirTemp("", "cue-pages-")
	if err != nil {
		t.Fatalf("making a profile directory: %s", err)
	}
	command := exec.Command(executable,
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--remote-debugging-port=0",
		"--user-data-dir="+profile,
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank",
	)
	command.Stderr = nil
	if err := command.Start(); err != nil {
		t.Fatalf("starting the browser: %s", err)
	}
	stop := func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		_ = os.RemoveAll(profile)
	}

	// Chromium writes the port it settled on into the profile once it is
	// listening. Asking for port 0 and reading it back is what keeps two of
	// these running at once from colliding.
	var address string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		content, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if err == nil {
			if port, _, found := strings.Cut(strings.TrimSpace(string(content)), "\n"); found || port != "" {
				address = "127.0.0.1:" + port
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if address == "" {
		stop()
		t.Fatalf("the browser never said which port it was listening on")
	}

	client := cdp.New(address)
	var pages []cdp.Target
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		found, err := client.Pages(ctx)
		if err == nil && len(found) > 0 {
			pages = found
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(pages) == 0 {
		stop()
		t.Fatalf("the browser never offered a page to attach to")
	}

	session, err := client.Attach(ctx, pages[0])
	if err != nil {
		stop()
		t.Fatalf("attaching to the page: %s", err)
	}
	return session, func() { session.Close(); stop() }
}

func evaluate(ctx context.Context, session *cdp.Session, expression string, await bool, result interface{}) error {
	var reply struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	parameters := map[string]interface{}{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  await,
	}
	if err := session.Call(ctx, "Runtime.evaluate", parameters, &reply); err != nil {
		return err
	}
	if reply.Exception != nil {
		return fmt.Errorf("%s", reply.Exception.Text)
	}
	if result == nil || len(reply.Result.Value) == 0 {
		return nil
	}
	return json.Unmarshal(reply.Result.Value, result)
}

// waitFor polls an expression until it is true, because a page coming up is
// not something the protocol reports at the moment this test cares about.
func waitFor(ctx context.Context, session *cdp.Session, expression string) error {
	deadline := time.Now().Add(30 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		var truthy bool
		// A navigation in flight makes evaluating throw; that is not a
		// failure, it is a reason to ask again in a moment.
		if err := evaluate(ctx, session, expression, false, &truthy); err != nil {
			last = err
		} else if truthy {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if last != nil {
		return last
	}
	return fmt.Errorf("%s never became true", expression)
}

// Everything the screen's own browser is allowed to do without a password
// rests on one assumption about browsers: that a page cannot lie about where
// it came from, and that a same-origin POST says where it came from at all.
//
// The second half is worth pinning with a real browser. If Chromium ever
// stopped sending Origin on a same-origin POST, the screen would quietly stop
// moving on at the end of a video and nothing would say why.
func TestOurOwnPageSaysWhereItCameFromOnAPost(t *testing.T) {
	executable := findBrowser()
	if executable == "" {
		t.Skip("no Chromium on this machine; the image has one and CI runs this there")
	}

	seen := make(chan string, 4)
	mux := http.NewServeMux()
	mux.HandleFunc("/asked", func(response http.ResponseWriter, request *http.Request) {
		seen <- request.Header.Get("Origin")
		response.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html")
		_, _ = response.Write([]byte(`<!doctype html><body><script>
			fetch("/asked", {method: "POST"});
		</script></body>`))
	})
	site := httptest.NewServer(mux)
	defer site.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	session, stop := startBrowser(ctx, t, executable)
	defer stop()

	if err := session.Call(ctx, "Page.navigate", map[string]interface{}{"url": site.URL}, nil); err != nil {
		t.Fatalf("navigating: %s", err)
	}

	select {
	case origin := <-seen:
		if origin != site.URL {
			t.Errorf("a same-origin POST arrived with Origin %q, want %q. "+
				"Everything the screen may do without a password depends on this.",
				origin, site.URL)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the page never made the request")
	}
}
