package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The examples under deploy/examples are what somebody copies onto a device.
// A field renamed here without renaming it there produces a file that looks
// authoritative and refuses to load, which is a bad first ten minutes.
func TestTheShippedExamplesLoad(t *testing.T) {
	pattern := filepath.Join("..", "..", "deploy", "examples", "*.yaml")
	filenames, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob: %s", err)
	}
	if len(filenames) == 0 {
		t.Fatalf("no examples were found at %s; if they moved, this test has to move too", pattern)
	}

	for _, filename := range filenames {
		content, err := os.ReadFile(filename)
		if err != nil {
			t.Errorf("%s: %s", filename, err)
			continue
		}
		if _, err := Parse(content); err != nil {
			t.Errorf("%s does not load:\n%s", filename, err)
		}
	}
}
