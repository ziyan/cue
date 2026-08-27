package vncserver

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/executable"
)

// A server told to listen on the loopback must be reachable there and nowhere
// else — over IPv6 included.
//
// This connects to the running server instead of reading its command line,
// because the first version of this test read the command line, passed, and
// the device deployed on the strength of it was still answering on [::]:5900.
// x11vnc accepts flags it then does not act on, and the difference is not
// visible in the arguments.
//
// The control run is the other half. On a machine where x11vnc would not
// listen on IPv6 anyway, the check below passes while proving nothing, so the
// test first watches the fault happen with the flags taken out and gives up
// rather than reassure if it cannot.
//
// The port is 5900 and must stay 5900. On any other port -no6 alone closes the
// socket, so an earlier version of this test that picked a free port passed
// against a build that leaked on every device, twice. 5900 is x11vnc's default
// and the only port this fault appears on, which is also the port every device
// runs.
func TestTheLoopbackListenerIsNotReachableOverIPv6(t *testing.T) {
	x11vnc, err := executable.Resolve("x11vnc", "/usr/bin/x11vnc")
	if err != nil {
		t.Skipf("no x11vnc here; the image has one and make docker-test runs this there: %s", err)
	}
	if _, err := executable.Resolve("Xvfb", "/usr/lib/xorg/Xvfb"); err != nil {
		t.Skipf("no Xvfb here; the image has one: %s", err)
	}

	const displayNumber = 61
	const port = 5900
	if _, taken := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); taken == nil {
		t.Skipf("something is already on port %d here, and this test cannot use another one", port)
	}
	arguments := serverArguments(t, displayNumber, port)

	withoutTheFlags := []string{}
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "-no6":
			continue
		case "-rfbportv6":
			index++ // and its value
			continue
		}
		withoutTheFlags = append(withoutTheFlags, arguments[index])
	}
	if len(withoutTheFlags) == len(arguments) {
		t.Fatal("the arguments carry nothing that turns IPv6 off, so there is " +
			"nothing here to test")
	}

	if !reachesIPv6(t, x11vnc, withoutTheFlags, displayNumber, port) {
		t.Skip("this x11vnc does not listen on IPv6 even when allowed to, so " +
			"the fault this guards against cannot be shown on this machine")
	}
	if reachesIPv6(t, x11vnc, arguments, displayNumber, port) {
		t.Error("told to listen on the IPv4 loopback, x11vnc is reachable over " +
			"IPv6 as well; on a machine with a routable address that is the " +
			"screen answering the internet")
	}
}

// serverArguments is what the daemon would run, from the daemon's own code, so
// that changing what it passes changes what is tested.
func serverArguments(t *testing.T, displayNumber, port int) []string {
	t.Helper()
	runtime := t.TempDir()
	configuration := config.Default()
	configuration.Paths.Runtime = runtime
	configuration.Paths.State = t.TempDir()
	configuration.VNC.Listen = fmt.Sprintf("127.0.0.1:%d", port)
	configuration.Normalize()

	authority := filepath.Join(runtime, "Xauthority")
	if err := os.WriteFile(authority, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store := config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)
	return New(store, fmt.Sprintf(":%d", displayNumber), authority).Settings().CommandLine()
}

// reachesIPv6 starts an X server and an x11vnc over it and reports whether the
// IPv6 loopback answers. It fails the test if the server never comes up at
// all, because a server that is not running answers nowhere and would look
// like the good news this is trying to establish.
func reachesIPv6(t *testing.T, x11vnc string, arguments []string, displayNumber, port int) bool {
	t.Helper()

	xvfb, _ := executable.Resolve("Xvfb", "/usr/lib/xorg/Xvfb")
	screen := exec.Command(xvfb, fmt.Sprintf(":%d", displayNumber),
		"-screen", "0", "640x480x24", "-nolisten", "tcp", "-noreset")
	if err := screen.Start(); err != nil {
		t.Fatalf("cannot start Xvfb: %s", err)
	}
	defer func() {
		_ = screen.Process.Kill()
		_, _ = screen.Process.Wait()
	}()

	said, err := os.CreateTemp(t.TempDir(), "x11vnc-*.log")
	if err != nil {
		t.Fatal(err)
	}
	server := exec.Command(x11vnc, arguments...)
	server.Env = append(os.Environ(), fmt.Sprintf("DISPLAY=:%d", displayNumber))
	server.Stdout, server.Stderr = said, said
	if err := server.Start(); err != nil {
		t.Fatalf("cannot start x11vnc (%s %v): %s", x11vnc, arguments, err)
	}
	defer func() {
		_ = server.Process.Kill()
		_, _ = server.Process.Wait()
	}()

	answers := func(address string) bool {
		connection, err := net.DialTimeout("tcp", address, 2*time.Second)
		if err != nil {
			return false
		}
		connection.Close()
		return true
	}

	// IPv4 first: until it is up, its silence elsewhere means nothing.
	overIPv4 := fmt.Sprintf("127.0.0.1:%d", port)
	up := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if answers(overIPv4) {
			up = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !up {
		written, _ := os.ReadFile(said.Name())
		t.Fatalf("x11vnc never listened on %s with %v. It said:\n%s", overIPv4, arguments, written)
	}

	// It binds both sockets together, so by now either it is there or it is
	// not; a short look is enough and a long one only slows the test down.
	return answers(fmt.Sprintf("[::1]:%d", port))
}
