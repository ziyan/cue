package web

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// The zones this machine actually has.
//
// Offered as a list rather than a text box because a timezone is the setting
// most likely to be typed wrongly and least likely to say so: "Europe/london"
// and "EST" and "GMT+1" are all things people write, none of them is a zone
// this daemon accepts, and the only symptom is a clock on the wall that is
// wrong by an hour.
//
// Read from the zoneinfo directory rather than compiled in, so the list is the
// one this build can actually load — a zone added to the database and not to a
// hard-coded list would be missing, and one removed would be offered and then
// refused.
var (
	timezonesOnce sync.Once
	timezoneNames []string
)

// zoneinfoDirectory is a variable so the tests can point it at one they built.
var zoneinfoDirectory = "/usr/share/zoneinfo"

// Timezones lists the zone names, sorted, with the continent-shaped ones
// first: those are what somebody is looking for, and the handful of legacy
// single-word zones at the top of an alphabetical list is noise.
func Timezones() []string {
	timezonesOnce.Do(func() {
		timezoneNames = readTimezones(zoneinfoDirectory)
	})
	return timezoneNames
}

func readTimezones(root string) []string {
	var names []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			// posix/ and right/ are the same zones again under different
			// leap-second rules, and offering each zone three times helps
			// nobody.
			switch entry.Name() {
			case "posix", "right":
				return filepath.SkipDir
			}
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		// Files that are not zones: the database's own metadata, and the
		// files with an extension.
		if strings.ContainsAny(name, ".") || !strings.Contains(name, "/") {
			return nil
		}
		names = append(names, name)
		return nil
	})

	sort.Strings(names)
	return names
}
