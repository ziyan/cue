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
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
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
	if width < 1000 || height < 500 {
		return withLogs(fmt.Errorf("the screenshot is %dx%d, which is not the configured screen", width, height))
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

	return nil
}

// The playlist is two pages served by the daemon itself, so that the test
// needs no network beyond the container.
const smokeConfiguration = `
device:
  name: Smoke test
log:
  level: INFO
display:
  server: xvfb
  number: 0
  framebuffer: 1280x720
browser:
  user: cue
  sandbox: false
playlist:
  interval: 5s
  items:
    - url: "data:text/html,<title>First</title><body style='background:#123'>"
    - url: "data:text/html,<title>Second</title><body style='background:#321'>"
vnc:
  enabled: true
  listen: 127.0.0.1:5900
audio:
  enabled: false
time:
  enabled: false
`

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
