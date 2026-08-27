package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func changelog(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

var day = time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

// Most pushes change nothing anybody outside the repository would notice. A
// release workflow that failed on those would be a wall of red nobody reads.
func TestNothingUnreleasedIsNotAFailure(t *testing.T) {
	path := changelog(t, "# Changelog\n\n## [Unreleased]\n\n## [1.2.3] - 2026-01-01\n\n### Added\n\n- Something.\n")

	result, err := cut(path, false, day)
	if err != nil {
		t.Fatalf("cut: %s", err)
	}
	if result.Released {
		t.Error("an empty Unreleased section was released")
	}
}

// What kind of entries there are decides the bump, and the mapping is the
// plain reading of semantic versioning.
func TestWhatKindOfChangeDecidesTheVersion(t *testing.T) {
	cases := []struct {
		name     string
		previous string
		entries  string
		want     string
	}{
		{"a fix is a patch", "1.4.2", "### Fixed\n\n- A thing.\n", "1.4.3"},
		{"a change is a patch", "1.4.2", "### Changed\n\n- A thing.\n", "1.4.3"},
		{"security is a patch", "1.4.2", "### Security\n\n- A thing.\n", "1.4.3"},
		{"something new is a minor", "1.4.2", "### Added\n\n- A thing.\n", "1.5.0"},
		{"new beats fixed", "1.4.2", "### Fixed\n\n- A.\n\n### Added\n\n- B.\n", "1.5.0"},
		{"a removal breaks somebody", "1.4.2", "### Removed\n\n- A thing.\n", "2.0.0"},
		{"a removal beats everything", "1.4.2", "### Added\n\n- A.\n\n### Removed\n\n- B.\n", "2.0.0"},
		// Before 1.0.0 a removal must not be the thing that declares a project
		// stable. That would say something about it that nobody meant.
		{"a removal before 1.0.0 is only a minor", "0.4.1", "### Removed\n\n- A thing.\n", "0.5.0"},
		{"the first release of all", "", "### Added\n\n- Everything.\n", "0.1.0"},
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			body := "# Changelog\n\n## [Unreleased]\n\n" + one.entries
			if one.previous != "" {
				body += "\n## [" + one.previous + "] - 2026-01-01\n\n### Added\n\n- Older.\n"
			}

			result, err := cut(changelog(t, body), false, day)
			if err != nil {
				t.Fatalf("cut: %s", err)
			}
			if !result.Released {
				t.Fatal("there were entries and nothing was released")
			}
			if result.Version != one.want {
				t.Errorf("%s -> %s, want %s (%s)", one.previous, result.Version, one.want, result.Why)
			}
		})
	}
}

func TestAMajorCanBeAskedForOutright(t *testing.T) {
	path := changelog(t, "# Changelog\n\n## [Unreleased]\n\n### Fixed\n\n- A thing.\n\n## [1.4.2] - 2026-01-01\n\n### Added\n\n- Older.\n")

	result, err := cut(path, true, day)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "2.0.0" {
		t.Errorf("asked for a major and got %s", result.Version)
	}
}

// The rewritten file has to be one the release workflow can read back: it
// looks up the section by version to write the release notes.
func TestTheRewrittenFileKeepsTheEntriesUnderTheirNewVersion(t *testing.T) {
	path := changelog(t, "# Changelog\n\n## [Unreleased]\n\n### Added\n\n- A new thing.\n\n## [1.4.2] - 2026-01-01\n\n### Added\n\n- Older.\n")

	result, err := cut(path, false, day)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result.Changelog, "## [1.5.0] - 2026-08-27") {
		t.Errorf("the new section is not dated:\n%s", result.Changelog)
	}
	if !strings.Contains(result.Changelog, "## [Unreleased]\n\n## [1.5.0]") {
		t.Errorf("there is no empty Unreleased left for the next change:\n%s", result.Changelog)
	}
	// The entries stay where they were, under the version that now names them.
	after := result.Changelog[strings.Index(result.Changelog, "## [1.5.0]"):]
	if !strings.Contains(after[:strings.Index(after, "## [1.4.2]")], "A new thing.") {
		t.Errorf("the entries did not end up under the new version:\n%s", result.Changelog)
	}
	// And what was there before is untouched.
	if !strings.Contains(result.Changelog, "## [1.4.2] - 2026-01-01") {
		t.Error("the previous release was disturbed")
	}
}

// A file with no Unreleased heading is a mistake worth stopping on, not
// something to guess about.
func TestAChangelogWithNoUnreleasedSectionIsRefused(t *testing.T) {
	path := changelog(t, "# Changelog\n\n## [1.0.0] - 2026-01-01\n\n### Added\n\n- A thing.\n")

	if _, err := cut(path, false, day); err == nil {
		t.Error("a changelog with no Unreleased heading was accepted")
	}
}

// Running it twice must not release twice: the second run sees an empty
// Unreleased section.
func TestCuttingTwiceReleasesOnce(t *testing.T) {
	path := changelog(t, "# Changelog\n\n## [Unreleased]\n\n### Added\n\n- A thing.\n\n## [1.0.0] - 2026-01-01\n\n### Added\n\n- Older.\n")

	first, err := cut(path, false, day)
	if err != nil || !first.Released {
		t.Fatalf("the first cut released %v (%v)", first.Released, err)
	}
	if err := os.WriteFile(path, []byte(first.Changelog), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := cut(path, false, day)
	if err != nil {
		t.Fatal(err)
	}
	if second.Released {
		t.Errorf("cutting twice released twice: %s then %s", first.Version, second.Version)
	}
}

// The template as it comes must not pass. Somebody who left it alone gets
// asked, not a release note reading "TODO".
func TestTheTemplateAsItComesIsNotAnEntry(t *testing.T) {
	template, err := os.ReadFile("../../.github/pull_request_template.md")
	if err != nil {
		t.Skip("no template here")
	}
	if entries := changelogBlockOf(string(template)); len(entries) != 0 {
		t.Errorf("the untouched template counts as a changelog entry: %v", entries)
	}
}

// A description somebody filled in properly.
func TestAFilledInDescriptionIsRead(t *testing.T) {
	body := `## What this changes

Somebody can now do the thing.

## Changelog

### Added

- The screen can show an uploaded video.
- And a picture.

## How it was checked

- Ran it.
`
	entries := changelogBlockOf(body)
	if len(entries["Added"]) != 2 {
		t.Fatalf("read %v", entries)
	}
	if entries["Added"][0] != "The screen can show an uploaded video." {
		t.Errorf("first entry is %q", entries["Added"][0])
	}
	// Nothing from outside the block leaks in.
	if len(entries) != 1 {
		t.Errorf("read headings that were not there: %v", entries)
	}
}

// The comment in the template explains the headings. It must not be mistaken
// for the entries.
func TestTheGuidanceInsideTheBlockIsNotAnEntry(t *testing.T) {
	body := "## Changelog\n\n<!--\n### Added — new behaviour\n- an example\n-->\n\n### Fixed\n\n- A real one.\n"

	entries := changelogBlockOf(body)
	if len(entries["Added"]) != 0 {
		t.Errorf("the guidance was read as entries: %v", entries)
	}
	if len(entries["Fixed"]) != 1 {
		t.Errorf("the real entry was missed: %v", entries)
	}
}

// Entries from pull requests join what was written by hand, under the right
// headings, and neither is lost.
func TestBothRoadsIntoTheChangelogAreOpen(t *testing.T) {
	written := "\n### Added\n\n- Written by hand, with a second line\n  that wraps.\n\n### Fixed\n\n- Also by hand.\n"
	collected := map[string][]string{
		"Added":    {"From a pull request. (#12)"},
		"Security": {"Something urgent. (#13)"},
	}

	merged := merge(written, collected)

	for _, wanted := range []string{
		"Written by hand, with a second line",
		"  that wraps.",
		"From a pull request. (#12)",
		"Also by hand.",
		"Something urgent. (#13)",
	} {
		if !strings.Contains(merged, wanted) {
			t.Errorf("%q was lost:\n%s", wanted, merged)
		}
	}

	// Hand-written first under a heading both share: somebody who wrote in the
	// changelog itself said more than a one-line summary.
	added := merged[strings.Index(merged, "### Added"):]
	if strings.Index(added, "Written by hand") > strings.Index(added, "From a pull request") {
		t.Errorf("the pull request entry came first:\n%s", merged)
	}
	// And the headings are in the order a release shows them.
	if strings.Index(merged, "### Added") > strings.Index(merged, "### Fixed") {
		t.Errorf("the headings are out of order:\n%s", merged)
	}
}

// Nothing from pull requests, and nothing written: nothing to release.
func TestNeitherRoadMeansNoRelease(t *testing.T) {
	if merged := merge("\n", nil); strings.TrimSpace(merged) != "" {
		t.Errorf("merged to %q", merged)
	}
}

// An entry long enough to wrap keeps all of itself. Losing half a sentence
// from the release notes is a poor reward for writing a careful one.
func TestAWrappedEntryKeepsItsTail(t *testing.T) {
	body := "## Changelog\n\n### Added\n\n" +
		"- A screen with no network shows a code a phone camera can read, and\n" +
		"  walks somebody through choosing a wireless network.\n" +
		"- A second entry, to be sure the first did not swallow it.\n"

	entries := changelogBlockOf(body)
	if len(entries["Added"]) != 2 {
		t.Fatalf("read %d entries: %v", len(entries["Added"]), entries["Added"])
	}
	if !strings.Contains(entries["Added"][0], "walks somebody through choosing") {
		t.Errorf("the wrapped tail was lost: %q", entries["Added"][0])
	}
	if !strings.HasPrefix(entries["Added"][1], "A second entry") {
		t.Errorf("the second entry is %q", entries["Added"][1])
	}
}
