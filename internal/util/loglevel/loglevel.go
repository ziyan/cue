// Package loglevel applies the configured logging level.
//
// It is here rather than in cmd because the level is a configuration setting
// like any other, and the daemon has to be able to apply it when the file
// changes. cmd imports the daemon, so the daemon cannot reach back into cmd.
package loglevel

import (
	"strings"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("loglevel")

// Set applies a log level by name, ignoring an empty or unparseable value so
// that a typo cannot silence the daemon.
func Set(level string) {
	if level == "" {
		return
	}
	parsed, err := logging.LogLevel(strings.ToUpper(level))
	if err != nil {
		log.Warningf("ignoring unknown log level %q", level)
		return
	}
	logging.SetLevel(parsed, "")
}

// Current is the level in force, as a name.
func Current() string {
	return logging.GetLevel("").String()
}
