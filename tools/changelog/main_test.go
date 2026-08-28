package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const example = `# Changelog

## [Unreleased]

### Added

- Something not released yet.

## [0.2.10] - 2026-10-01

### Fixed

- The later one, which shares a prefix with the earlier one.

## [0.2.0] - 2026-09-01

### Added

- The first thing.
- The second thing.

## [0.1.0] - 2026-08-01

### Added

- The beginning.
`

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %s", err)
	}
	return path
}

func TestOneVersionsSectionIsTakenAndNotTheNext(t *testing.T) {
	path := write(t, example)

	section, err := extract(path, "0.2.0")
	if err != nil {
		t.Fatalf("extract: %s", err)
	}
	if !strings.Contains(section, "The first thing") || !strings.Contains(section, "The second thing") {
		t.Errorf("the section is missing its own entries:\n%s", section)
	}
	if strings.Contains(section, "The beginning") {
		t.Errorf("the section ran into the version below it:\n%s", section)
	}
	if strings.Contains(section, "The later one") {
		t.Errorf("the section ran into the version above it:\n%s", section)
	}
}

func TestAVersionIsNotConfusedWithOneThatSharesItsPrefix(t *testing.T) {
	// 0.2.0 must not match the heading for 0.2.10, which is the kind of thing
	// that is noticed only when the release notes are wrong.
	path := write(t, example)

	section, err := extract(path, "0.2.10")
	if err != nil {
		t.Fatalf("extract: %s", err)
	}
	if !strings.Contains(section, "The later one") {
		t.Errorf("0.2.10 gave the wrong section:\n%s", section)
	}
}

func TestAReleaseWithNoEntryIsRefused(t *testing.T) {
	// A release nobody can tell anything about afterwards is worse than one
	// that does not happen, and finding out now takes ten seconds rather than
	// ten minutes of building.
	path := write(t, example)

	if _, err := extract(path, "0.3.0"); err == nil {
		t.Fatal("a version with no changelog entry was accepted")
	} else if !strings.Contains(err.Error(), "0.3.0") {
		t.Errorf("the error does not say which version is missing: %s", err)
	}
}

func TestTheSeveralWaysPeopleWriteAHeadingAreAccepted(t *testing.T) {
	for _, heading := range []string{
		"## [0.2.0] - 2026-09-01",
		"## 0.2.0 - 2026-09-01",
		"## v0.2.0",
		"## [0.2.0]",
	} {
		if !headingIsFor(heading, "0.2.0") {
			t.Errorf("%q was not recognised as the heading for 0.2.0", heading)
		}
	}

	for _, heading := range []string{
		"## [0.2.10] - 2026-10-01",
		"## [Unreleased]",
		"## 0.20.0",
	} {
		if headingIsFor(heading, "0.2.0") {
			t.Errorf("%q was wrongly taken as the heading for 0.2.0", heading)
		}
	}
}

// The project's own changelog has to work with this, since a release depends
// on it.
// The heading must be there. Its contents must not be checked for being
// non-empty, and that is the point of this comment.
//
// Cutting a release moves everything out of Unreleased into a section named
// for the version, which leaves Unreleased empty until the next change lands.
// An empty Unreleased is therefore the correct state of this file on exactly
// one commit: the release commit. This test used to call extract(), which
// refuses an empty section because releasing nothing is a mistake -- so it
// failed on every release commit, on a repository where the release commit is
// written by the release itself. Both of the first two releases went out with
// a red tick beside them for that reason and no other.
//
// What extract() does with a section that has contents is checked on written
// examples above, where an empty Unreleased cannot come along and break it.
func TestThisProjectsChangelogHasSomewhereForTheNextChangeToGo(t *testing.T) {
	path := filepath.Join("..", "..", "CHANGELOG.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("this project's own changelog: %s", err)
	}
	if !strings.Contains(string(content), "## [Unreleased]") {
		t.Error("there is no Unreleased heading; the next change has nowhere to go")
	}
}
