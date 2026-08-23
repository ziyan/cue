// Command checksecrets fails if a tracked file contains something that must
// never be published: a real credential, a private network address, or a
// private key.
//
// This project is open source, and the feature that motivated its login
// support arrived with a working username and password for somebody's device.
// That pair belongs in that device's own /etc/cue/cue.yaml and nowhere else.
//
// Every pattern here matches a shape rather than a literal, so that this
// program does not itself become the place a secret is recorded. Run it with
// "make check-secrets"; it is part of "make lint-ci", so continuous
// integration refuses a change that carries one.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// rule is one thing that must not appear in a tracked file.
type rule struct {
	name string

	// pattern matches the shape of the thing.
	pattern *regexp.Regexp

	// allowed matches a line the pattern found that is fine after all: a
	// placeholder, a documented example, a comment explaining the rule.
	allowed *regexp.Regexp

	// explanation is printed once if the rule fires, and says what to do.
	explanation string
}

var rules = []rule{
	{
		name: "a private network address",
		// 127.0.0.1 and 0.0.0.0 are deliberately not matched: binding to them
		// is a normal thing for this daemon to do.
		pattern:     regexp.MustCompile(`(^|[^0-9.])(10\.[0-9]{1,3}|192\.168|172\.(1[6-9]|2[0-9]|3[01]))\.[0-9]{1,3}\.[0-9]{1,3}`),
		explanation: "use example.com, a placeholder, or 127.0.0.1 instead of somebody's real network",
	},
	{
		name:    "what looks like a real credential",
		pattern: regexp.MustCompile(`(?i)(password|passwd|secret|token|apikey|api_key)["']?\s*[:=]\s*["'][^"']+["']`),
		// Two kinds of value are allowed. The first is an outright
		// placeholder: an empty string, a row of asterisks, a format verb, a
		// template variable. The second is a value that announces itself as
		// invented — anything containing the word test, example, fake, dummy,
		// sample or placeholder as a word of its own. Test fixtures and
		// documentation need *some* string, and requiring them to say so is
		// better than exempting whole files: a real credential in a test is
		// exactly the leak this exists to catch.
		allowed: regexp.MustCompile(`(?i)["'](` +
			`|\*+|changeme|redacted|hunter2|\.\.\.|\$\{[^}]+\}|<[^>]+>|%[svq]` +
			`|[^"']*\b(test|example|fake|dummy|sample|placeholder|invalid)\b[^"']*` +
			`)["']`),
		explanation: "a credential belongs in the device's own configuration file, never in the repository; " +
			"a value used in a test or an example must say so — put the word test, example or placeholder in it",
	},
	{
		name:        "a private key",
		pattern:     regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		explanation: "keys are generated on the device at first run and are never committed",
	},
	{
		name:        "an AWS access key",
		pattern:     regexp.MustCompile(`(^|[^A-Z0-9])AKIA[A-Z0-9]{16}([^A-Z0-9]|$)`),
		explanation: "rotate it, then remove it",
	},
}

// skipped files are tracked but not scanned: vendored code belongs to somebody
// else, lock files are full of hashes that look like anything, and this
// program describes the patterns it looks for.
func skipped(filename string) bool {
	switch {
	case strings.HasPrefix(filename, "vendor/"):
		return true
	case strings.HasPrefix(filename, "web/node_modules/"):
		return true
	case filename == "web/package-lock.json":
		return true
	case filename == "tools/checksecrets/main.go":
		return true
	}
	return false
}

func main() {
	filenames, err := trackedFiles()
	if err != nil {
		fmt.Fprintf(os.Stderr, "checksecrets: %s\n", err)
		os.Exit(2)
	}

	found := 0
	scanned := 0
	for _, filename := range filenames {
		if skipped(filename) {
			continue
		}
		content, err := os.ReadFile(filename)
		if err != nil {
			// A tracked file that is not on disk is a deleted file that has
			// not been committed yet, which is not this program's problem.
			continue
		}
		if isBinary(content) {
			// A compiled program has no business being in the repository, and
			// one was committed by a "go build" that wrote into the working
			// directory followed by "git add -A". It is caught here rather
			// than only in .gitignore, because an ignore rule protects
			// against the names somebody thought of.
			fmt.Fprintf(os.Stderr, "%s: a compiled binary is committed (%d bytes)\n"+
				"    build into build/ instead; the Makefile does\n", filename, len(content))
			found++
			continue
		}
		if bytes.IndexByte(content, 0) >= 0 {
			// Something with NUL bytes in it: an image, a compressed file.
			// Not text, so not worth scanning for credentials.
			continue
		}

		scanned++
		found += scan(filename, string(content))
	}

	if found > 0 {
		fmt.Fprintf(os.Stderr, "\ncheck-secrets: %d problem(s) found in %d files\n", found, scanned)
		os.Exit(1)
	}
	fmt.Printf("check-secrets: clean (%d files)\n", scanned)
}

// isBinary reports whether a file is a compiled program rather than text.
// The four bytes at the start of an ELF file are the reliable test; a NUL
// anywhere is the loose one, and it also catches the compressed and image
// files that legitimately live in a repository, so it is only used to stop
// scanning them for credentials.
func isBinary(content []byte) bool {
	return bytes.HasPrefix(content, []byte("\x7fELF"))
}

func scan(filename, content string) int {
	found := 0
	for number, line := range strings.Split(content, "\n") {
		for _, current := range rules {
			if !current.pattern.MatchString(line) {
				continue
			}
			if current.allowed != nil && current.allowed.MatchString(line) {
				continue
			}
			fmt.Fprintf(os.Stderr, "%s:%d: %s\n    %s\n    %s\n",
				filename, number+1, current.name, strings.TrimSpace(line), current.explanation)
			found++
		}
	}
	return found
}

func trackedFiles() ([]string, error) {
	command := exec.Command("git", "ls-files")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var filenames []string
	for _, line := range strings.Split(string(output), "\n") {
		if line != "" {
			filenames = append(filenames, line)
		}
	}
	return filenames, nil
}
