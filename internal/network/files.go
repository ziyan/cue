package network

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ziyan/cue/internal/config"
)

// Where this package keeps the files it writes.
//
// They are all wpa_supplicant configurations, and there is one per interface
// plus one apiece for the access point and for scanning. They used to sit
// loose in the state directory beside everything else the daemon keeps, which
// made that directory hard to read and gave no hint that these three kinds of
// file belong together.

// directoryName is the subdirectory of the state directory these live in.
const directoryName = "network"

// Directory is where this package's files go, created if it is not there.
func Directory(configuration *config.Configuration) string {
	directory := filepath.Join(configuration.Paths.State, directoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		log.Warningf("cannot create %s: %s", directory, err)
		// Falling back to the old place rather than failing. A daemon that
		// cannot make one directory should still be able to join a network.
		return configuration.Paths.State
	}
	return directory
}

// AdoptOldFiles moves the configurations an older version left loose in the
// state directory.
//
// This matters more than tidiness. wpa_supplicant owns the file for an
// interface and saves the networks it has been told to join into it -- names
// and passphrases both -- so leaving it behind is a device that forgets every
// wireless network it knew and cannot get back onto one without somebody
// standing in front of it.
func AdoptOldFiles(configuration *config.Configuration) {
	previous := configuration.Paths.State
	current := Directory(configuration)
	if previous == current {
		return
	}

	entries, err := os.ReadDir(previous)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "wpa_supplicant-") || !strings.HasSuffix(name, ".conf") {
			continue
		}
		to := filepath.Join(current, name)
		if _, err := os.Stat(to); err == nil {
			// Something is already there under the new name, and it is the one
			// this version has been using. The old one is stale.
			continue
		}
		if err := os.Rename(filepath.Join(previous, name), to); err != nil {
			log.Warningf("cannot move %s into %s: %s", name, current, err)
			continue
		}
		log.Noticef("moved %s into %s, where this version keeps it", name, current)
	}
}
