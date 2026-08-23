// Package version reports what this build is, so that a bug report can name
// it. The values are set at link time by the Makefile and by the release
// workflow; a build made with a plain "go build" reports the development
// defaults rather than lying about being a release.
package version

import (
	"fmt"
	"runtime"
)

var (
	version = "0.0.0-dev"
	commit  = "unknown"
)

// Version is the release this binary was built from, or "0.0.0-dev".
func Version() string {
	return version
}

// Commit is the full git commit this binary was built from, with "-dirty"
// appended when the working tree had uncommitted changes.
func Commit() string {
	return commit
}

// String is the one-line description used by "cue version" and by the first
// log line the daemon writes.
func String() string {
	return fmt.Sprintf("%s (%s, %s, %s/%s)", version, commit, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
