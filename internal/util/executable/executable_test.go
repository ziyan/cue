package executable

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %s", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write: %s", err)
	}
}

func TestAScriptIsRecognisedAndARealProgramIsNot(t *testing.T) {
	directory := t.TempDir()

	script := filepath.Join(directory, "wrapper")
	write(t, script, "#!/bin/sh\nexec /usr/lib/thing/thing \"$@\"\n", 0o755)
	if !IsScript(script) {
		t.Error("a file starting with #! was not recognised as a script")
	}
	if IsExecutableProgram(script) {
		t.Error("a script was reported as a program that can be run; there is no shell to run it")
	}

	program := filepath.Join(directory, "program")
	write(t, program, "\x7fELF\x02\x01\x01", 0o755)
	if IsScript(program) {
		t.Error("an ELF binary was reported as a script")
	}
	if !IsExecutableProgram(program) {
		t.Error("an ELF binary was not reported as a program that can be run")
	}
}

func TestAFileWithoutTheExecuteBitIsNotAProgram(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	write(t, path, "\x7fELF", 0o644)
	if IsExecutableProgram(path) {
		t.Error("a file with no execute permission was reported as a program")
	}
}

func TestResolveFallsBackWhenTheNameIsAScript(t *testing.T) {
	directory := t.TempDir()

	wrapper := filepath.Join(directory, "Xorg")
	write(t, wrapper, "#!/bin/sh\nexec /usr/lib/xorg/Xorg \"$@\"\n", 0o755)

	real := filepath.Join(directory, "lib", "Xorg")
	write(t, real, "\x7fELF", 0o755)

	resolved, err := Resolve(wrapper, real)
	if err != nil {
		t.Fatalf("resolve: %s", err)
	}
	if resolved != real {
		t.Errorf("resolved to %q, want the real executable %q", resolved, real)
	}
}

func TestResolvePrefersTheNameWhenItIsAlreadyAProgram(t *testing.T) {
	directory := t.TempDir()
	program := filepath.Join(directory, "chromium")
	write(t, program, "\x7fELF", 0o755)
	other := filepath.Join(directory, "other")
	write(t, other, "\x7fELF", 0o755)

	resolved, err := Resolve(program, other)
	if err != nil {
		t.Fatalf("resolve: %s", err)
	}
	if resolved != program {
		t.Errorf("resolved to %q, want %q", resolved, program)
	}
}

func TestResolveSaysWhatItLookedFor(t *testing.T) {
	_, err := Resolve("/nonexistent/thing", "/nonexistent/other")
	if err == nil {
		t.Fatal("resolving something that does not exist should fail")
	}
	// The message is what somebody reads at three in the morning.
	for _, expected := range []string{"/nonexistent/thing", "/nonexistent/other"} {
		if !contains(err.Error(), expected) {
			t.Errorf("the error does not mention %s: %s", expected, err)
		}
	}
}

func contains(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
