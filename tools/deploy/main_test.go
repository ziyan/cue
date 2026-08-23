package main

import (
	"strings"
	"testing"
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
