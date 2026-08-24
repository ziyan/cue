// Command smoke runs the whole daemon inside the container image against a
// virtual screen and proves that it works, end to end, without any hardware.
//
// It is what "make docker-smoke" runs and what continuous integration runs on
// every change. The point is that everything is exercised — the X server, the
// browser, the rotation, the login rules, the VNC server, the web interface,
// the screenshot — on a machine with no display attached, so a change that
// breaks the picture is caught before it reaches a device somebody has to
// drive to.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	image := flag.String("image", "cue:dev", "the container image to test")
	port := flag.Int("port", 18080, "a free port on this machine to publish the interface on")
	keep := flag.Bool("keep", false, "leave the container running afterwards, to look at it")
	flag.Parse()

	if err := run(*image, *port, *keep); err != nil {
		fmt.Fprintf(os.Stderr, "\nsmoke: FAILED: %s\n", err)
		os.Exit(1)
	}
	fmt.Println("\nsmoke: everything works")
}

const containerName = "cue-smoke"

func run(image string, port int, keep bool) error {
	directory, err := os.MkdirTemp("", "cue-smoke-")
	if err != nil {
		return err
	}
	if !keep {
		defer func() { _ = os.RemoveAll(directory) }()
	}

	configuration := filepath.Join(directory, "cue.yaml")
	if err := os.WriteFile(configuration, []byte(smokeConfiguration), 0o644); err != nil {
		return err
	}
	// The daemon rewrites this file as the account inside the container.
	if err := os.Chmod(directory, 0o777); err != nil {
		return err
	}

	_ = exec.Command("docker", "rm", "-f", containerName).Run()
	if !keep {
		defer func() { _ = exec.Command("docker", "rm", "-f", containerName).Run() }()
	}

	step("starting %s", image)
	start := exec.Command("docker", "run", "-d",
		"--name", containerName,
		"--shm-size=1g",
		"-p", fmt.Sprintf("127.0.0.1:%d:8080", port),
		"-v", directory+":/etc/cue",
		image, "run")
	if output, err := start.CombinedOutput(); err != nil {
		return fmt.Errorf("cannot start the container: %s: %s", err, output)
	}

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	if err := waitFor("the daemon to answer", 90*time.Second, func() error {
		response, err := http.Get(base + "/api/v1/setup")
		if err != nil {
			return err
		}
		return response.Body.Close()
	}); err != nil {
		return withLogs(err)
	}

	step("setting the device up")
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := post(client, base+"/api/v1/setup", map[string]string{
		"name":     "Smoke test",
		"password": "smoke-test-password",
	}, nil); err != nil {
		return withLogs(err)
	}

	step("waiting for the browser to show the playlist")
	var status map[string]interface{}
	if err := waitFor("a page to be on the screen", 120*time.Second, func() error {
		if err := get(client, base+"/api/v1/status", &status); err != nil {
			return err
		}
		browser, _ := status["browser"].(map[string]interface{})
		if browser == nil || browser["ready"] != true {
			return fmt.Errorf("the browser is not ready yet")
		}
		if address, _ := browser["currentUrl"].(string); address == "" {
			return fmt.Errorf("nothing is on the screen yet")
		}
		return nil
	}); err != nil {
		return withLogs(err)
	}

	step("checking that every program is running")
	programs, _ := status["programs"].([]interface{})
	running := map[string]bool{}
	for _, entry := range programs {
		program, _ := entry.(map[string]interface{})
		name, _ := program["name"].(string)
		state, _ := program["state"].(string)
		running[name] = state == "running"
		fmt.Printf("    %-10s %s\n", name, state)
	}
	for _, name := range []string{"xvfb", "chromium", "x11vnc"} {
		if !running[name] {
			return withLogs(fmt.Errorf("%s is not running", name))
		}
	}

	step("checking the health endpoint")
	response, err := http.Get(base + "/healthz")
	if err != nil {
		return withLogs(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return withLogs(fmt.Errorf("/healthz answered %s", response.Status))
	}

	step("taking a screenshot")
	image_, err := fetch(client, base+"/api/v1/screenshot.png")
	if err != nil {
		return withLogs(err)
	}
	width, height, err := pngSize(image_)
	if err != nil {
		return withLogs(err)
	}
	fmt.Printf("    a %dx%d PNG of %d bytes\n", width, height, len(image_))
	// Exactly the configured screen, not merely a plausible size. The
	// screenshot is of the browser's window, so this is the check that the
	// window fills the screen — and with no window manager here, nothing but
	// this daemon will make it.
	//
	// It was 1152x864 on a screen of 1280x1024 on the first machine this was
	// migrated to, with a black band down two sides, because the screen size
	// was read out of the X connection setup: a block sent once when the
	// client connects and never updated, so it still held the size from
	// before this daemon resized the screen. Every log line said the right
	// mode had been set, and it had.
	// Within a pixel or two: Chromium reports a viewport a pixel short of the
	// window it was given, consistently and in both directions, and chasing
	// that is not what this check is for. The fault it exists to catch is off
	// by a hundred and twenty-eight.
	const slack = 4
	if abs(width-screenWidth) > slack || abs(height-screenHeight) > slack {
		return withLogs(fmt.Errorf("the screenshot is %dx%d and the screen is %dx%d, "+
			"so the browser's window does not fill it", width, height, screenWidth, screenHeight))
	}

	step("checking that the small screenshot really is smaller")
	small, err := fetch(client, base+"/api/v1/screenshot.png?small=1")
	if err != nil {
		return withLogs(err)
	}
	// The interface asks for a new one every few seconds. On a 4K screen the
	// full-size lossless picture was 5.6 MB, which is a hundred megabytes a
	// minute to leave a browser tab open on.
	// JPEG, because the small one always is: most of what is on these
	// screens is video from a camera, which PNG stores appallingly.
	smallConfig, err := jpeg.DecodeConfig(bytes.NewReader(small))
	if err != nil {
		return withLogs(fmt.Errorf("the small screenshot is not the JPEG it should be: %w", err))
	}
	fmt.Printf("    full %d bytes %dx%d, small %d bytes %dx%d\n",
		len(image_), width, height, len(small), smallConfig.Width, smallConfig.Height)
	// The width, not the byte count: a flat test page compresses to about the
	// same size either way, and a picture that is still full size would slip
	// through a check on bytes alone.
	if smallConfig.Width >= width {
		return withLogs(fmt.Errorf("the small screenshot is %d pixels wide against the full %d, so it is not smaller",
			smallConfig.Width, width))
	}
	if smallConfig.Height*width != smallConfig.Width*height {
		return withLogs(fmt.Errorf("the small screenshot is %dx%d, which is not the shape of the %dx%d screen",
			smallConfig.Width, smallConfig.Height, width, height))
	}

	step("checking that dark mode reaches the page")
	// This measures what is painted, not what a flag says. Two of the three
	// dark-mode flags this daemon used to pass do not exist in this Chromium,
	// and Chromium ignores a switch it does not know without a word: the
	// command line said dark, every setting said dark, and the screen was
	// white. Nothing short of looking at the pixels would have caught it.
	//
	// The page on screen is the daemon's own interface, which honours
	// prefers-color-scheme — so a dark screen here means the browser really
	// is telling pages to be dark.
	brightness, err := averageBrightness(image_)
	if err != nil {
		return withLogs(err)
	}
	fmt.Printf("    the screen averages %.0f/255\n", brightness)
	if brightness > 128 {
		return withLogs(fmt.Errorf("the screen averages %.0f/255, which is not dark: "+
			"darkMode is on and the page is painted light", brightness))
	}

	step("checking that the playlist rotates")
	first, _ := status["browser"].(map[string]interface{})["currentUrl"].(string)
	if err := waitFor("the next page", 60*time.Second, func() error {
		if err := get(client, base+"/api/v1/status", &status); err != nil {
			return err
		}
		current, _ := status["browser"].(map[string]interface{})["currentUrl"].(string)
		if current == first {
			return fmt.Errorf("still showing %s", first)
		}
		fmt.Printf("    moved from %s to %s\n", first, current)
		return nil
	}); err != nil {
		return withLogs(err)
	}

	step("checking that the watchdog is answering")
	watchdog, _ := status["watchdog"].(map[string]interface{})
	if failures, _ := watchdog["consecutiveFailures"].(float64); failures > 0 {
		return withLogs(fmt.Errorf("the watchdog reports %.0f failed probes", failures))
	}

	// The two features that exist because of somebody's real dashboard: an
	// announcement clicked away, and a session signed back in. The page
	// reports both in its own title.
	step("checking that the announcement is dismissed and the page is signed in")
	if err := waitFor("the awkward page to be dealt with", 90*time.Second, func() error {
		if err := get(client, base+"/api/v1/status", &status); err != nil {
			return err
		}
		title := tabTitle(status, "awkwardpagexxxxx")
		if title != "dismissed-signedin" {
			return fmt.Errorf("its title is %q", title)
		}
		return nil
	}); err != nil {
		return withLogs(err)
	}

	// The remote view of the screen. Nothing else exercises it: the VNC server
	// listens on the loopback address and only this bridge reaches it, so a
	// change that broke the bridge would show up as a blank Screen page and
	// nothing in any log.
	step("checking that the screen can be watched over the bridge")
	if err := checkVNCBridge(base, client); err != nil {
		return withLogs(err)
	}

	tabs, _ := status["browser"].(map[string]interface{})["tabs"].([]interface{})
	for _, entry := range tabs {
		tab, _ := entry.(map[string]interface{})
		if item, _ := tab["item"].(string); item != "awkwardpagexxxxx" {
			continue
		}
		logins, _ := tab["logins"].(float64)
		dismissed, _ := tab["dismissed"].(float64)
		fmt.Printf("    signed in %.0f time(s), dismissed %.0f thing(s)\n", logins, dismissed)
		if logins < 1 {
			return withLogs(fmt.Errorf("the page reports itself signed in but no login was recorded"))
		}
		if dismissed < 1 {
			return withLogs(fmt.Errorf("the announcement is gone but no dismissal was recorded"))
		}
	}

	return nil
}

// checkVNCBridge opens the WebSocket the browser's viewer uses and speaks
// enough of the remote framebuffer protocol to prove that bytes reach the VNC
// server and come back.
//
// The protocol opens with the server stating its version as twelve bytes,
// "RFB 003.008\n". That single exchange is enough: it can only arrive if the
// WebSocket upgraded, the origin check passed, the session was accepted, the
// bridge dialled the VNC server, and the VNC server is attached to a running
// X display.
func checkVNCBridge(base string, self *client) error {
	address := "ws" + strings.TrimPrefix(base, "http") + "/api/v1/vnc"

	parsed, err := url.Parse(base)
	if err != nil {
		return err
	}

	headers := http.Header{}
	for _, cookie := range self.http.Jar.Cookies(parsed) {
		headers.Add("Cookie", cookie.Name+"="+cookie.Value)
	}
	// The bridge refuses an origin that is not its own host, so that a page
	// elsewhere cannot use a browser's session to watch the screen. Sending
	// the right one also exercises that check.
	headers.Set("Origin", base)

	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Subprotocols: []string{"binary"}}
	connection, response, err := dialer.Dial(address, headers)
	if err != nil {
		if response != nil {
			return fmt.Errorf("cannot open the VNC bridge: %s: %w", response.Status, err)
		}
		return fmt.Errorf("cannot open the VNC bridge: %w", err)
	}
	defer func() { _ = connection.Close() }()

	_ = connection.SetReadDeadline(time.Now().Add(15 * time.Second))

	greeting, err := readAtLeast(connection, 12)
	if err != nil {
		return fmt.Errorf("the VNC server said nothing through the bridge: %w", err)
	}
	if !strings.HasPrefix(string(greeting), "RFB ") {
		return fmt.Errorf("what came back through the bridge is not the remote framebuffer protocol: %q", greeting)
	}
	fmt.Printf("    the screen answered %q\n", strings.TrimSpace(string(greeting[:12])))

	// Answer with the same version, which proves the other direction works:
	// a server that never hears from the client closes the connection.
	if err := connection.WriteMessage(websocket.BinaryMessage, greeting[:12]); err != nil {
		return fmt.Errorf("cannot write to the VNC bridge: %w", err)
	}

	// The server replies with the security types it will accept, which it
	// only sends after reading the version. One byte is enough.
	if _, err := readAtLeast(connection, 1); err != nil {
		return fmt.Errorf("the VNC server did not answer the version the viewer sent: %w", err)
	}
	return nil
}

// readAtLeast collects binary frames until it has the wanted number of bytes.
// A WebSocket frame is not a fixed slice of the byte stream, so the twelve
// bytes of a greeting may arrive as one frame or several.
func readAtLeast(connection *websocket.Conn, wanted int) ([]byte, error) {
	var collected []byte
	for len(collected) < wanted {
		messageType, data, err := connection.ReadMessage()
		if err != nil {
			return collected, err
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		collected = append(collected, data...)
	}
	return collected, nil
}

// tabTitle finds one playlist item's tab in a status response and returns the
// title its page is reporting.
func tabTitle(status map[string]interface{}, item string) string {
	browser, _ := status["browser"].(map[string]interface{})
	tabs, _ := browser["tabs"].([]interface{})
	for _, entry := range tabs {
		tab, _ := entry.(map[string]interface{})
		if name, _ := tab["item"].(string); name == item {
			title, _ := tab["title"].(string)
			return title
		}
	}
	return ""
}

// The playlist needs no network beyond the container: two coloured pages to
// watch the rotation, and one page that behaves like the dashboard this
// project was built for — it puts an announcement on top of itself and asks to
// be signed in.
//
// That third page is how the two features that are otherwise only testable
// against somebody's real system get tested here, on every change.
var smokeConfiguration = `
device:
  name: Smoke test
log:
  level: INFO
display:
  server: xvfb
  number: 0
  # Deliberately not the size Xvfb would start at on its own, so that the
  # daemon has to resize the screen and the checks below see the result.
  framebuffer: 1280x720
browser:
  user: cue
  sandbox: false
  darkMode: true
playlist:
  interval: 5s
  items:
    # The daemon's own interface. A real page over HTTP that honours
    # prefers-color-scheme, which is what the dark-mode check below measures —
    # a data: URL would not do, because Chromium's own dark handling treats
    # those differently.
    - url: "http://127.0.0.1:8080/"
    - url: "data:text/html,<title>Second</title><body style='background:teal'>"
    - identifier: awkwardpagexxxxx
      title: An awkward page
      url: '` + awkwardPage + `'
      login:
        whenSelectorExists: "input[type=password]"
        usernameSelector: "input[name=username]"
        passwordSelector: "input[name=password]"
        submitSelector: "button[type=submit]"
        username: display
        password: a test page password
        minimumInterval: 5s
      dismiss:
        - selector: "button.dismiss"
          whenTextMatches: "Got it"
vnc:
  enabled: true
  listen: 127.0.0.1:5900
audio:
  enabled: false
time:
  enabled: false
`

// awkwardPage is a page that does the two things a real dashboard does and a
// test page usually does not: it covers itself with an announcement nobody
// asked for, and it wants to be signed in.
//
// It reports what has happened to it in its own title, because the title is
// something the daemon already reports for every tab, so the test can watch it
// without any special machinery. The title becomes "dismissed-signedin" only
// when both rules have actually worked — the announcement was clicked away and
// the form was submitted with the right credentials.
//
// The form deliberately sets its value through the property that React and
// friends intercept, so a login that only assigned .value would leave the
// field looking full and submit nothing. That is the failure this page exists
// to catch.
// The screen the smoke configuration asks for. The screenshot has to come
// back at exactly this size.
const (
	screenWidth  = 1280
	screenHeight = 720
)

const awkwardPage = `data:text/html,` +
	`<title>waiting-anon</title>` +
	`<style>.overlay{position:fixed;inset:0;background:rgba(0,0,0,0.6);` +
	`display:flex;align-items:center;justify-content:center}</style>` +
	`<div class="overlay" id="announcement">` +
	`<div><h2>What is new</h2><button class="dismiss">Got it</button></div></div>` +
	`<form id="signin">` +
	`<input name="username"><input type="password" name="password">` +
	`<button type="submit">Sign in</button></form>` +
	`<script>` +
	`var dismissed=false,signedIn=false;` +
	`function update(){document.title=(dismissed?"dismissed":"waiting")+"-"+(signedIn?"signedin":"anon")}` +
	`document.querySelector("button.dismiss").onclick=function(){` +
	`document.getElementById("announcement").remove();dismissed=true;update()};` +
	`document.getElementById("signin").onsubmit=function(event){event.preventDefault();` +
	`var user=document.querySelector("input[name=username]").value;` +
	`var pass=document.querySelector("input[name=password]").value;` +
	`if(user==="display"){if(pass==="a test page password"){signedIn=true;` +
	`document.getElementById("signin").remove()}}update()};` +
	`update();` +
	`</script>`

// --- the plumbing -----------------------------------------------------------

func step(format string, arguments ...interface{}) {
	fmt.Printf("==> "+format+"\n", arguments...)
}

func waitFor(what string, limit time.Duration, attempt func() error) error {
	deadline := time.Now().Add(limit)
	var last error
	for time.Now().Before(deadline) {
		last = attempt()
		if last == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("waited %s for %s: %w", limit, what, last)
}

// withLogs attaches the container's log to a failure, because a smoke test
// that says only "it did not work" costs somebody a rerun to find out why.
func withLogs(err error) error {
	output, logErr := exec.Command("docker", "logs", "--tail", "60", containerName).CombinedOutput()
	if logErr != nil {
		return err
	}
	return fmt.Errorf("%w\n\n--- the last of the container's log ---\n%s", err, output)
}

type client struct {
	http *http.Client
}

func newClient() (*client, error) {
	jar, err := cookieJar()
	if err != nil {
		return nil, err
	}
	return &client{http: &http.Client{Jar: jar, Timeout: 30 * time.Second}}, nil
}

func post(self *client, address string, body interface{}, into interface{}) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	response, err := self.http.Post(address, "application/json", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("POST %s: %s: %s", address, response.Status, payload)
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(payload, into)
}

func get(self *client, address string, into interface{}) error {
	payload, err := fetch(self, address)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, into)
}

func fetch(self *client, address string) ([]byte, error) {
	response, err := self.http.Get(address)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s: %s", address, response.Status, payload)
	}
	return payload, nil
}

// pngSize reads the dimensions out of a PNG header, which is enough to prove
// the picture is of the screen that was configured rather than a placeholder.
func pngSize(data []byte) (int, int, error) {
	const header = "\x89PNG\r\n\x1a\n"
	if len(data) < 24 || string(data[:8]) != header {
		return 0, 0, fmt.Errorf("that is not a PNG")
	}
	width := binary.BigEndian.Uint32(data[16:20])
	height := binary.BigEndian.Uint32(data[20:24])
	return int(width), int(height), nil
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// averageBrightness decodes a PNG and returns the mean channel value. It is
// how the smoke test tells a dark screen from a flag that claims one.
func averageBrightness(data []byte) (float64, error) {
	picture, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, fmt.Errorf("cannot read the screenshot: %w", err)
	}
	bounds := picture.Bounds()
	var total float64
	var count float64
	// Every eighth pixel in each direction: enough of a sample for an average
	// over a whole screen, and a great deal quicker.
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 8 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 8 {
			red, green, blue, _ := picture.At(x, y).RGBA()
			total += float64(red>>8) + float64(green>>8) + float64(blue>>8)
			count += 3
		}
	}
	if count == 0 {
		return 0, fmt.Errorf("the screenshot has no pixels")
	}
	return total / count, nil
}
