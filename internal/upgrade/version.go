package upgrade

import (
	"strconv"
	"strings"
)

// developmentVersion is what a binary built with a plain "go build" reports.
// See internal/version.
const developmentVersion = "0.0.0-dev"

// Newer reports whether candidate is a later release than current.
//
// Compared as three numbers rather than as text, because text says 0.10.0 is
// older than 0.9.0 and would offer somebody a downgrade as an upgrade.
//
// A development build is never out of date. It has no place on the ladder:
// it may be ahead of every release or behind all of them, and the person
// running one is building from source and does not need a web page telling
// them to install a tag.
func Newer(current, candidate string) bool {
	if isDevelopment(current) {
		return false
	}

	currentMajor, currentMinor, currentPatch, ok := parse(current)
	if !ok {
		return false
	}
	candidateMajor, candidateMinor, candidatePatch, ok := parse(candidate)
	if !ok {
		return false
	}

	if candidateMajor != currentMajor {
		return candidateMajor > currentMajor
	}
	if candidateMinor != currentMinor {
		return candidateMinor > currentMinor
	}
	return candidatePatch > currentPatch
}

// isDevelopment reports whether this is a build made outside the release
// workflow. Anything that is not three plain numbers counts, so a build marked
// dirty or carrying a pre-release suffix is treated the same way.
func isDevelopment(version string) bool {
	if version == "" || version == developmentVersion {
		return true
	}
	_, _, _, ok := parse(version)
	return !ok
}

// parse reads "1.2.3", with or without a leading v, and refuses anything else
// -- including the pre-release and build suffixes semantic versioning allows,
// which this project does not publish and which have ordering rules of their
// own that would be wrong to guess at.
func parse(version string) (int, int, int, bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(version), "v")

	pieces := strings.Split(trimmed, ".")
	if len(pieces) != 3 {
		return 0, 0, 0, false
	}

	numbers := make([]int, 3)
	for index, piece := range pieces {
		if piece == "" {
			return 0, 0, 0, false
		}
		number, err := strconv.Atoi(piece)
		if err != nil || number < 0 {
			return 0, 0, 0, false
		}
		numbers[index] = number
	}
	return numbers[0], numbers[1], numbers[2], true
}
