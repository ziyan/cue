package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestADefaultedPlatformArgumentIsRejected reintroduces the exact line that
// broke the first release and expects the check to say so. Without this the
// check could quietly stop matching and nothing would notice until another
// release failed halfway through.
func TestADefaultedPlatformArgumentIsRejected(t *testing.T) {
	dockerfile := filepath.Join(t.TempDir(), "Dockerfile")
	write(t, dockerfile, "FROM debian:trixie-slim\nARG TARGETARCH=amd64\n")

	err := checkPlatformArguments(dockerfile)
	if err == nil {
		t.Fatal("ARG TARGETARCH=amd64 was accepted; that is the bug this check exists for")
	}
	if !strings.Contains(err.Error(), "TARGETARCH") {
		t.Fatalf("the complaint does not name the argument: %s", err)
	}
}

// TestTheBareFormIsAccepted is the other half: the check must not object to
// the way the Dockerfile is supposed to be written, or it would be worked
// around rather than obeyed.
func TestTheBareFormIsAccepted(t *testing.T) {
	dockerfile := filepath.Join(t.TempDir(), "Dockerfile")
	write(t, dockerfile, "FROM debian:trixie-slim\nARG TARGETARCH\nARG VERSION=dev\n")

	if err := checkPlatformArguments(dockerfile); err != nil {
		t.Fatalf("the bare form was rejected: %s", err)
	}
}

// TestOurOwnDockerfilePasses points the check at the real thing, so that the
// two tests above cannot both pass while the image stays broken.
func TestOurOwnDockerfilePasses(t *testing.T) {
	dockerfile := filepath.Join("..", "..", "Dockerfile")
	if _, err := os.Stat(dockerfile); err != nil {
		t.Skipf("no Dockerfile to check: %s", err)
	}

	if err := checkPlatformArguments(dockerfile); err != nil {
		t.Fatalf("%s", err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
