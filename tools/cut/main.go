// Command cut turns whatever has piled up under "Unreleased" into a release:
// it works out the next version, dates the section, and leaves the file ready
// for the next change.
//
// It does not touch git. Deciding what the version is and writing it down is
// one thing; committing, tagging and pushing is another, and the workflow does
// that with the credentials for it. Keeping them apart means this can be run
// on a laptop to see what would happen, which is the first thing anybody wants
// from a release tool.
//
//	go run ./tools/cut             # print what would happen, change nothing
//	go run ./tools/cut --write     # rewrite CHANGELOG.md
//	go run ./tools/cut --write --major
//
// It is a Go program rather than a shell script for the reason the rest of
// this repository is: there is one language here, and a step that only runs in
// continuous integration is exactly the kind of thing that quietly stops
// working.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func main() {
	check := flag.String("check", "", "check a pull request description in this file and say nothing else")
	write := flag.Bool("write", false, "rewrite the changelog rather than saying what would change")
	major := flag.Bool("major", false, "bump the major version, whatever the entries say")
	filename := flag.String("file", "CHANGELOG.md", "the changelog to read")
	flag.Parse()

	if *check != "" {
		checkDescription(*check)
		return
	}

	result, err := cut(*filename, *major, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cut: %s\n", err)
		os.Exit(1)
	}

	if !result.Released {
		// Not an error. Most pushes change nothing anybody outside this
		// repository would notice, and a release workflow that failed on those
		// would be a wall of red nobody reads.
		fmt.Println("nothing under Unreleased, so there is nothing to release")
		emit("released", "false")
		return
	}

	if *write {
		if err := os.WriteFile(*filename, []byte(result.Changelog), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "cut: %s\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("%s -> %s (%s)\n", result.Previous, result.Version, result.Why)
	emit("released", "true")
	emit("version", result.Version)
	emit("tag", "v"+result.Version)
}

// emit writes a value the workflow can read, and also to the log, so that a
// run can be understood from its output alone.
func emit(key, value string) {
	if path := os.Getenv("GITHUB_OUTPUT"); path != "" {
		if file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			// The workflow reads its next steps out of this file, so a failure
			// here decides whether a release happens. Say so rather than
			// leaving a later step to fail for no visible reason.
			if _, err := fmt.Fprintf(file, "%s=%s\n", key, value); err != nil {
				fmt.Fprintf(os.Stderr, "cut: cannot write %s to the workflow output: %s\n", key, err)
			}
			if err := file.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "cut: cannot finish the workflow output: %s\n", err)
			}
		}
	}
	fmt.Printf("  %s=%s\n", key, value)
}

// Result is what cutting a release would do.
type Result struct {
	Released  bool
	Previous  string
	Version   string
	Why       string
	Changelog string
}

var (
	unreleasedHeading = regexp.MustCompile(`(?m)^## \[Unreleased\]\s*$`)
	versionHeading    = regexp.MustCompile(`(?m)^## \[(\d+)\.(\d+)\.(\d+)\]`)
	kindHeading       = regexp.MustCompile(`(?m)^### (.+?)\s*$`)
)

// cut works out what the next release is, and what the changelog looks like
// afterwards.
func cut(filename string, forceMajor bool, now time.Time) (Result, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return Result{}, err
	}
	text := string(content)

	where := unreleasedHeading.FindStringIndex(text)
	if where == nil {
		return Result{}, fmt.Errorf("%s has no \"## [Unreleased]\" heading", filename)
	}

	// Everything from the Unreleased heading to the next version heading is
	// what has been written by hand.
	rest := text[where[1]:]
	written := rest
	tail := ""
	if next := versionHeading.FindStringIndex(rest); next != nil {
		written = rest[:next[0]]
		tail = rest[next[0]:]
	}

	previous := latestVersion(text)

	// Entries reach a release by two roads and both are open: written into a
	// pull request description, where they are reviewed alongside the change
	// they describe, or written straight into the Unreleased section by
	// somebody committing to main. Taking only one would quietly drop work.
	collected, err := entriesFromPullRequests(tagOf(previous))
	if err != nil {
		return Result{}, err
	}

	body := merge(written, collected)
	if strings.TrimSpace(body) == "" {
		return Result{Released: false}, nil
	}

	version, why := nextVersion(previous, kindsIn(body), forceMajor)

	rewritten := text[:where[0]] +
		fmt.Sprintf("## [Unreleased]\n\n## [%s] - %s\n", version, now.Format("2006-01-02")) +
		body + tail

	return Result{
		Released:  true,
		Previous:  previous,
		Version:   version,
		Why:       why,
		Changelog: rewritten,
	}, nil
}

// tagOf is the tag a version was released under, or empty for a project with
// no releases yet.
func tagOf(version string) string {
	if version == "" || version == "0.0.0" {
		return ""
	}
	return "v" + version
}

// merge folds the entries collected from pull requests into what somebody
// wrote by hand, under the headings they belong to.
//
// What was written by hand comes first under each heading. Somebody who took
// the trouble to describe a change in the changelog itself has said more about
// it than a one-line pull request summary, and should read first.
func merge(written string, collected map[string][]string) string {
	if len(collected) == 0 {
		return written
	}

	existing := sectionsOf(written)
	var builder strings.Builder
	for _, kind := range kinds {
		lines := append([]string(nil), existing[kind]...)
		lines = append(lines, collected[kind]...)
		if len(lines) == 0 {
			continue
		}
		builder.WriteString("\n### " + kind + "\n\n")
		for _, line := range lines {
			builder.WriteString(line + "\n")
		}
	}
	return builder.String()
}

// sectionsOf splits a written Unreleased section into its headings, keeping
// each entry exactly as it was written -- indentation, wrapped lines and the
// blank lines between paragraphs included, because those are what make a
// changelog worth reading.
func sectionsOf(body string) map[string][]string {
	sections := map[string][]string{}
	kind := ""
	for _, line := range strings.Split(body, "\n") {
		if found := kindHeading.FindStringSubmatch(line); found != nil {
			kind = knownKind(found[1])
			continue
		}
		if kind == "" {
			continue
		}
		sections[kind] = append(sections[kind], line)
	}
	// Trailing blank lines belong to the gap between sections, not to the last
	// entry of one.
	for kind, lines := range sections {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		sections[kind] = lines
	}
	return sections
}

// latestVersion is the newest version already in the file, or 0.0.0 when this
// would be the first release.
func latestVersion(text string) string {
	if found := versionHeading.FindStringSubmatch(text); found != nil {
		return found[1] + "." + found[2] + "." + found[3]
	}
	return "0.0.0"
}

// kindsIn is the set of "### Added", "### Fixed" and so on under a section.
func kindsIn(body string) map[string]bool {
	kinds := map[string]bool{}
	for _, found := range kindHeading.FindAllStringSubmatch(body, -1) {
		kinds[strings.ToLower(strings.TrimSpace(found[1]))] = true
	}
	return kinds
}

// nextVersion decides the bump from what kind of entries there are.
//
// The rule is the plain reading of semantic versioning, and the mapping is
// from the Keep a Changelog headings this project already uses:
//
//   - Removed is a major bump. Something that was there is not any more, and
//     anybody relying on it is broken by upgrading. Nothing else in this list
//     can break somebody.
//   - Added is a minor bump: there is something new and everything old still
//     works.
//   - Everything else -- Fixed, Changed, Security, Deprecated -- is a patch.
//
// Before 1.0.0 this is softened one step, as is usual: a Removed becomes a
// minor rather than taking a project from 0.4 to 1.0, which would say
// something about stability that nobody meant.
func nextVersion(previous string, kinds map[string]bool, forceMajor bool) (string, string) {
	major, minor, patch := parse(previous)

	switch {
	case forceMajor:
		return fmt.Sprintf("%d.0.0", major+1), "asked for a major release"

	case kinds["removed"]:
		if major == 0 {
			return fmt.Sprintf("0.%d.0", minor+1),
				"something was removed, which before 1.0.0 is a minor bump"
		}
		return fmt.Sprintf("%d.0.0", major+1), "something was removed"

	case kinds["added"]:
		return fmt.Sprintf("%d.%d.0", major, minor+1), "something was added"

	default:
		return fmt.Sprintf("%d.%d.%d", major, minor, patch+1), "fixes and changes only"
	}
}

func parse(version string) (int, int, int) {
	pieces := strings.SplitN(version, ".", 3)
	for len(pieces) < 3 {
		pieces = append(pieces, "0")
	}
	major, _ := strconv.Atoi(pieces[0])
	minor, _ := strconv.Atoi(pieces[1])
	patch, _ := strconv.Atoi(pieces[2])
	return major, minor, patch
}

// checkDescription is the guard on a pull request: it fails when the changelog
// block is missing or is still the template.
//
// It exists because the alternative is finding out at release time, when the
// person who made the change has moved on and the entry has to be written by
// somebody guessing from a diff. The message says how to get past it both
// ways: write the entry, or say there is nothing to write.
func checkDescription(filename string) {
	body, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cut: %s\n", err)
		os.Exit(1)
	}

	entries := changelogBlockOf(string(body))
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr,
			"This pull request has no changelog entry.\n\n"+
				"Under \"## Changelog\" in the description, keep the heading that fits\n"+
				"(Added, Changed, Deprecated, Removed, Fixed, Security) and replace the\n"+
				"placeholder with what changed, written for whoever reads the release\n"+
				"notes.\n\n"+
				"If nobody outside this repository can observe the change, label the pull\n"+
				"request skip-changelog instead.")
		os.Exit(1)
	}

	for kind, lines := range entries {
		for _, line := range lines {
			fmt.Printf("%s: %s\n", kind, line)
		}
	}
}
