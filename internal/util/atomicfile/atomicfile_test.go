package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplacesAndLeavesNothingBehind(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "cue.yaml")

	if err := Write(filename, []byte("first"), 0o640); err != nil {
		t.Fatalf("write: %s", err)
	}
	if err := Write(filename, []byte("second"), 0o640); err != nil {
		t.Fatalf("rewrite: %s", err)
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read: %s", err)
	}
	if string(content) != "second" {
		t.Errorf("content is %q, want %q", content, "second")
	}

	info, err := os.Stat(filename)
	if err != nil {
		t.Fatalf("stat: %s", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode is %o, want 640", info.Mode().Perm())
	}

	// The temporary file must not survive: a directory that is rewritten on
	// every configuration change would otherwise fill up with them.
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read the directory: %s", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("the directory holds %v, want only cue.yaml", names)
	}
}

func TestWriteCreatesTheDirectory(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "etc", "cue", "cue.yaml")
	if err := Write(filename, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %s", err)
	}
	if _, err := os.Stat(filename); err != nil {
		t.Fatalf("stat: %s", err)
	}
}
