// Command changelog prints the CHANGELOG.md section for one version, which
// becomes the body of the GitHub release.
//
// It fails when there is no section, on purpose: a release with no changelog
// entry is one nobody can tell anything about afterwards, and finding that out
// before the build takes ten minutes off the discovery.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: changelog <version> [file]")
		os.Exit(2)
	}
	version := strings.TrimPrefix(os.Args[1], "v")

	filename := "CHANGELOG.md"
	if len(os.Args) > 2 {
		filename = os.Args[2]
	}

	section, err := extract(filename, version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "changelog: %s\n", err)
		os.Exit(1)
	}
	fmt.Print(section)
}

// extract returns everything under the heading for one version, up to the
// next version heading. The format is Keep a Changelog:
//
//	## [0.2.0] - 2026-09-01
func extract(filename, version string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	var lines []string
	inside := false

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "## ") {
			if inside {
				break
			}
			inside = headingIsFor(line, version)
			continue
		}
		if inside {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	section := strings.TrimSpace(strings.Join(lines, "\n"))
	if section == "" {
		return "", fmt.Errorf("%s has no entry for %s; add one before releasing", filename, version)
	}
	return section + "\n", nil
}

// headingIsFor reports whether a heading names this version, accepting the
// several ways people write it: "## [0.2.0] - 2026-09-01", "## 0.2.0",
// "## v0.2.0".
func headingIsFor(heading, version string) bool {
	trimmed := strings.TrimSpace(strings.TrimPrefix(heading, "##"))
	trimmed = strings.TrimLeft(trimmed, "[")
	for _, candidate := range []string{version, "v" + version} {
		if strings.HasPrefix(trimmed, candidate) {
			// Guard against 0.2.0 matching a heading for 0.2.10.
			rest := strings.TrimPrefix(trimmed, candidate)
			if rest == "" || strings.HasPrefix(rest, "]") || strings.HasPrefix(rest, " ") {
				return true
			}
		}
	}
	return false
}
