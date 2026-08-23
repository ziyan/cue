// Command release builds the binaries a release publishes, and the file of
// checksums beside them.
//
// It is a Go program rather than a shell script for the same reason the rest
// of this project is: there is one language here, and a build step that only
// runs in continuous integration is exactly the kind of thing that quietly
// stops working.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// targets are the machines a display runs on: an x86 mini PC or compute
// stick, and an ARM single-board computer.
var targets = []struct {
	operatingSystem string
	architecture    string
}{
	{"linux", "amd64"},
	{"linux", "arm64"},
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: release <version> <directory>")
		os.Exit(2)
	}
	version := strings.TrimPrefix(os.Args[1], "v")
	directory := os.Args[2]

	if err := run(version, directory); err != nil {
		fmt.Fprintf(os.Stderr, "release: %s\n", err)
		os.Exit(1)
	}
}

func run(version, directory string) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}

	commit, err := currentCommit()
	if err != nil {
		return err
	}

	var sums []string
	for _, target := range targets {
		name := fmt.Sprintf("cue-%s-%s", target.operatingSystem, target.architecture)
		path := filepath.Join(directory, name)

		fmt.Printf("building %s\n", name)
		build := exec.Command("go", "build", "-mod=vendor",
			"-ldflags", fmt.Sprintf(`-s -w -extldflags "-static" `+
				`-X github.com/ziyan/cue/internal/version.version=%s `+
				`-X github.com/ziyan/cue/internal/version.commit=%s`, version, commit),
			"-o", path, ".")
		build.Env = append(os.Environ(),
			"CGO_ENABLED=0",
			"GOOS="+target.operatingSystem,
			"GOARCH="+target.architecture)
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			return fmt.Errorf("building %s: %w", name, err)
		}

		sum, err := checksum(path)
		if err != nil {
			return err
		}
		sums = append(sums, fmt.Sprintf("%s  %s", sum, name))
	}

	content := strings.Join(sums, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Print(content)
	return nil
}

func currentCommit() (string, error) {
	output, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown", nil
	}
	return strings.TrimSpace(string(output)), nil
}

func checksum(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}
