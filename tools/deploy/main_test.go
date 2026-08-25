package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTheContainerGetsTheConsoleItIsToldToDrawOn(t *testing.T) {
	// This is the pairing that was got wrong once and produced a black screen
	// with a message naming neither half: the X server is told to use console
	// N, and the container has to be given /dev/ttyN.
	arguments := containerArguments("cue:dev", "cue", 5, nil)
	joined := strings.Join(arguments, " ")

	if !strings.Contains(joined, "--device /dev/tty5:/dev/tty5") {
		t.Errorf("console 5 was not passed through:\n%s", joined)
	}
	if !strings.Contains(joined, "--device /dev/tty0:/dev/tty0") {
		t.Errorf("/dev/tty0 was not passed through; without it the X server cannot reach the console layer at all")
	}
}

func TestTheContainerCanManageTheMachinesNetwork(t *testing.T) {
	// The machine's interfaces live in the host's network namespace and
	// nowhere else, so managing them needs that namespace and the capability
	// to change what is in it.
	joined := strings.Join(containerArguments("cue:dev", "cue", 2, nil), " ")
	for _, expected := range []string{"--network host", "--cap-add NET_ADMIN"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("%q is missing:\n%s", expected, joined)
		}
	}
}

func TestTheGraphicsDeviceAndTheStateAreAlwaysThere(t *testing.T) {
	joined := strings.Join(containerArguments("cue:dev", "cue", 2, nil), " ")
	for _, expected := range []string{
		"--device /dev/dri:/dev/dri",
		"-v /etc/cue:/etc/cue",
		"-v /var/lib/cue:/var/lib/cue",
		"--network host",
		"--shm-size 1g",
		"--cap-add SYS_TTY_CONFIG",
		"--cap-add SYS_TIME",
		// Without this the browser will not start at all, because its own
		// sandbox cannot create the namespaces it needs.
		"--cap-add SYS_ADMIN",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("%q is missing:\n%s", expected, joined)
		}
	}
}

func TestTheConfigurationDirectoryIsMountedNotTheFile(t *testing.T) {
	// Mounting the file makes every save fail with "device or resource busy",
	// because a rename cannot replace a bind mount.
	joined := strings.Join(containerArguments("cue:dev", "cue", 2, nil), " ")
	if strings.Contains(joined, "cue.yaml") {
		t.Errorf("the configuration file is mounted individually:\n%s", joined)
	}
}

func TestOptionalDevicesAreOnlyPassedWhenTheyExist(t *testing.T) {
	// Naming a device that is not on the machine is an error from Docker, and
	// a screen nobody touches and nobody listens to is perfectly ordinary.
	without := strings.Join(containerArguments("cue:dev", "cue", 2, nil), " ")
	if strings.Contains(without, "/dev/snd") || strings.Contains(without, "/dev/input") {
		t.Errorf("a device that was not offered was passed anyway:\n%s", without)
	}

	with := strings.Join(containerArguments("cue:dev", "cue", 2, []string{"/dev/input", "/dev/snd"}), " ")
	for _, expected := range []string{"--device /dev/input:/dev/input", "--device /dev/snd:/dev/snd"} {
		if !strings.Contains(with, expected) {
			t.Errorf("%q is missing:\n%s", expected, with)
		}
	}
}

func TestTheImageAndTheCommandComeLast(t *testing.T) {
	// Docker takes its own flags before the image and the container's
	// arguments after it; getting that order wrong is a confusing failure.
	arguments := containerArguments("ghcr.io/ziyan/cue:latest", "cue", 2, []string{"/dev/snd"})
	if arguments[len(arguments)-1] != "run" {
		t.Errorf("the last argument is %q, want the daemon's subcommand", arguments[len(arguments)-1])
	}
	if arguments[len(arguments)-2] != "ghcr.io/ziyan/cue:latest" {
		t.Errorf("the image is at %q, want it immediately before the subcommand", arguments[len(arguments)-2])
	}
}

func TestTheLogIsBounded(t *testing.T) {
	// A screen is a machine nobody logs in to for a year at a time. Docker
	// keeps the whole log by default, so anything the daemon or one of its
	// programs repeats runs until the disk is full and the machine stops
	// doing anything at all — which is not a hypothetical: the device this
	// project replaces turned over 50 MB of browser log in a day.
	joined := strings.Join(containerArguments("cue:dev", "cue", 2, nil), " ")

	for _, wanted := range []string{"--log-opt max-size=", "--log-opt max-file="} {
		if !strings.Contains(joined, wanted) {
			t.Errorf("the container is started without %q, so its log grows forever:\n%s", wanted, joined)
		}
	}
}

func TestTheMachinesDeviceDatabaseIsMounted(t *testing.T) {
	// The X server does not go looking in /dev/input; it asks udev, and udev
	// answers out of /run/udev. Passing the input devices without the
	// database gets a screen with no keyboard and no mouse, and nothing
	// anywhere that looks like an error — the X server asked and the answer
	// was none.
	joined := strings.Join(containerArguments("cue:dev", "cue", 2, []string{"/dev/input"}), " ")

	if !strings.Contains(joined, "/run/udev:/run/udev:ro") {
		t.Errorf("the machine's udev database is not mounted, so the screen will have no input:\n%s", joined)
	}
	if !strings.Contains(joined, "/dev/input") {
		t.Errorf("the input devices are not passed through:\n%s", joined)
	}
}

// The image is sent before anything running on the machine is touched.
//
// This cost a screen seventy minutes of being dark. The order was: remove the
// running container, stop the display manager, then send eight hundred
// megabytes. The link between the two sites went away during the send, and the
// machine was left with no container, no display manager, and no image to make
// a new one from — every step that had run having succeeded.
//
// Read out of the source rather than by running a deployment, because what is
// being asserted is an order, there is no machine here to deploy to, and the
// alternative is discovering it the same way again.
func TestTheImageIsSentBeforeTheRunningDeploymentIsTouched(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read the deploy tool: %s", err)
	}
	text := string(source)

	sending := strings.Index(text, `step("sending %s"`)
	stopping := strings.Index(text, `step("stopping any previous deployment")`)
	stoppingDisplay := strings.Index(text, `step("stopping anything that holds the graphics device")`)

	for name, at := range map[string]int{
		"the send":               sending,
		"stopping the container": stopping,
		"stopping the display":   stoppingDisplay,
	} {
		if at < 0 {
			t.Fatalf("cannot find %s in the deploy tool; this test needs updating with it", name)
		}
	}

	if sending > stopping {
		t.Error("the running container is removed before the image is sent: a transfer that fails " +
			"leaves the machine with nothing to start")
	}
	if sending > stoppingDisplay {
		t.Error("the display manager is stopped before the image is sent: a transfer that fails " +
			"leaves the machine with no display at all")
	}
}

// A send into a link that has gone must fail, and fail quickly.
//
// This is the fault underneath the outage. StdoutPipe hands the read end of
// the pipe to this process, and starting ssh gives it a second copy; while
// this process kept its own, ssh exiting did not close the pipe, "docker save"
// never got a broken pipe, and it blocked on a full buffer with nothing at the
// other end. The send did not fail — it stopped, silently, for as long as
// anybody was willing to wait.
//
// Measured as well as asserted, because "returns an error eventually" is what
// it did before: it took over two minutes, against a host name that does not
// even resolve.
func TestASendIntoNothingFailsAndFailsQuickly(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("no docker here to save an image with")
	}

	started := time.Now()
	err := sendImage("root@a-host-that-does-not-resolve.invalid", "cue:dev")
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("sending an image to a host that does not exist reported success")
	}
	// Generous: the point is seconds rather than minutes.
	if elapsed > 30*time.Second {
		t.Errorf("the send took %s to fail; it is hanging rather than failing", elapsed.Round(time.Second))
	}
	t.Logf("failed in %s: %s", elapsed.Round(time.Millisecond), err)
}
