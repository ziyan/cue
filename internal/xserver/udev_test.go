package xserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAMissingUdevDatabaseIsExplained(t *testing.T) {
	// The failure this catches looks like nothing at all: /dev/input full of
	// devices, the Device page listing every one of them, the X server's log
	// free of errors, and a screen that ignores the keyboard. Whatever else
	// this says, it has to name the mount.
	previous := udevDirectory
	udevDirectory = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { udevDirectory = previous })

	problem, ok := inputWillWork()
	if ok {
		t.Fatal("a container with no udev database was reported fine")
	}
	if !strings.Contains(problem, "/run/udev") {
		t.Errorf("the message does not name the thing to mount: %q", problem)
	}
	for _, wanted := range []string{"keyboard", "udev"} {
		if !strings.Contains(problem, wanted) {
			t.Errorf("the message does not mention %q, so it will not be understood: %q", wanted, problem)
		}
	}
}

func TestAnEmptyUdevDatabaseIsExplainedDifferently(t *testing.T) {
	// Mounted but empty is a different mistake from not mounted, and the
	// thing to do about it is different too.
	previous := udevDirectory
	udevDirectory = t.TempDir()
	t.Cleanup(func() { udevDirectory = previous })

	problem, ok := inputWillWork()
	if ok {
		t.Fatal("an empty udev database was reported fine")
	}
	if strings.Contains(problem, "-v /run/udev") {
		t.Errorf("an empty database was reported as a missing mount: %q", problem)
	}
}

func TestAPopulatedUdevDatabaseIsFine(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "c13:64"), []byte("I:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := udevDirectory
	udevDirectory = directory
	t.Cleanup(func() { udevDirectory = previous })

	if problem, ok := inputWillWork(); !ok {
		t.Errorf("a populated database was reported as a problem: %q", problem)
	}
}
