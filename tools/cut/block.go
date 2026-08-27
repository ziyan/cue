package main

import (
	"regexp"
	"strings"
)

// Reading the changelog block out of a pull request description.
//
// The block is the "## Changelog" heading and everything under it until the
// next heading of the same level. Inside it are Keep a Changelog headings and
// bullets, which is the same shape the changelog file itself uses -- so what
// somebody writes in a description is what appears in the release, and there
// is nothing to translate.

var (
	changelogSection = regexp.MustCompile(`(?m)^##\s+Changelog\s*$`)
	nextSection      = regexp.MustCompile(`(?m)^##\s+\S`)
	kindLine         = regexp.MustCompile(`(?m)^###\s+(.+?)\s*$`)
	bulletLine       = regexp.MustCompile(`^\s*[-*]\s+(.*)$`)
	htmlComment      = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// kinds are the headings this project uses, in the order a release shows them.
var kinds = []string{"Added", "Changed", "Deprecated", "Removed", "Fixed", "Security"}

// changelogBlockOf returns the entries in a pull request description, keyed by
// the heading they were written under.
//
// Anything that is still the template -- the placeholder line, the row of
// alternatives, an HTML comment -- is not an entry, and is dropped rather than
// published. Somebody who left the template alone gets nothing, not a release
// note reading "TODO".
func changelogBlockOf(body string) map[string][]string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = htmlComment.ReplaceAllString(body, "")

	where := changelogSection.FindStringIndex(body)
	if where == nil {
		return nil
	}
	block := body[where[1]:]
	if next := nextSection.FindStringIndex(block); next != nil {
		block = block[:next[0]]
	}

	entries := map[string][]string{}
	kind := ""
	for _, line := range strings.Split(block, "\n") {
		if found := kindLine.FindStringSubmatch(line); found != nil {
			kind = knownKind(found[1])
			continue
		}
		if kind == "" {
			continue
		}

		if found := bulletLine.FindStringSubmatch(line); found != nil {
			text := strings.TrimSpace(found[1])
			if text == "" || looksLikeTemplate(text) {
				continue
			}
			entries[kind] = append(entries[kind], text)
			continue
		}

		// A line that is not a bullet and not blank continues the bullet above
		// it. Somebody writing an entry long enough to wrap would otherwise
		// have half of it silently dropped from the release notes, which is a
		// poor reward for taking the trouble.
		if strings.TrimSpace(line) == "" || len(entries[kind]) == 0 {
			continue
		}
		last := len(entries[kind]) - 1
		entries[kind][last] += " " + strings.TrimSpace(line)
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}

// knownKind maps a heading to one this project uses, and returns empty for
// anything else -- including the template's row of alternatives, which is not
// a choice anybody made.
func knownKind(heading string) string {
	heading = strings.TrimSpace(heading)
	for _, kind := range kinds {
		if strings.EqualFold(heading, kind) {
			return kind
		}
	}
	return ""
}

// looksLikeTemplate recognises the line the template puts there to be replaced.
func looksLikeTemplate(text string) bool {
	lowered := strings.ToLower(text)
	for _, marker := range []string{
		"todo",
		"replace with",
		"one-line summary",
		"one line summary",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}
