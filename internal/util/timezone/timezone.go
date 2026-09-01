// Package timezone sets the process's idea of local time from the
// configuration.
//
// It lives here rather than in cmd because the timezone is a setting like any
// other and has to follow the file while the daemon runs: a screen that is
// moved, or one set up in the wrong zone, should read correctly without
// somebody having to restart the container to see it.
package timezone

import (
	"time"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("timezone")

// Apply points time.Local at the named zone, so that every log line and
// everything the interface renders agrees with what somebody standing in front
// of the screen would expect. An empty or unknown name is left alone rather
// than resetting the zone to UTC underneath a running screen.
//
// This writes a package-level variable in the standard library while other
// goroutines may be formatting times. The write is a single pointer and the
// worst outcome is a timestamp either side of the change being rendered in the
// old zone, which is what would happen anyway.
func Apply(name string) {
	if name == "" {
		return
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		log.Warningf("ignoring unknown timezone %q: %s", name, err)
		return
	}
	if time.Local == location {
		return
	}
	time.Local = location
}

// Current is the zone in force.
func Current() string {
	return time.Local.String()
}
