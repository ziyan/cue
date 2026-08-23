package browser

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// knownBinaries are where a real Chromium executable is found on the systems
// this runs on, in the order they are tried. The image ships the first.
var knownBinaries = []string{
	"/usr/lib/chromium/chromium",
	"/usr/lib/chromium-browser/chromium-browser",
	"/opt/google/chrome/chrome",
	"/usr/lib/chrome/chrome",
}

// resolveBinary finds the executable to start.
//
// It exists because of one specific trap. On Debian, /usr/bin/chromium is not
// the browser: it is a shell script that works out some flags and then runs
// /usr/lib/chromium/chromium. This image has no shell, so executing that
// script fails — and the failure surfaces as the browser exiting immediately
// with no message anybody can act on. So a wrapper script is detected by its
// first two bytes and stepped over.
func resolveBinary(configured string) (string, error) {
	candidate := configured
	if candidate == "" {
		candidate = "chromium"
	}

	if !strings.ContainsRune(candidate, os.PathSeparator) {
		found, err := exec.LookPath(candidate)
		if err == nil {
			candidate = found
		}
	}

	if isExecutableProgram(candidate) {
		return candidate, nil
	}

	// Either it was not found, or it was a script. Both are answered the same
	// way: look for the real executable next to where the script would be.
	if isScript(candidate) {
		if real := realBinaryNear(candidate); real != "" {
			log.Debugf("%s is a wrapper script and this image has no shell; using %s instead", candidate, real)
			return real, nil
		}
	}

	for _, known := range knownBinaries {
		if isExecutableProgram(known) {
			log.Debugf("using %s", known)
			return known, nil
		}
	}

	return "", fmt.Errorf("browser: cannot find a browser to run; %q is not an executable program and none of %s exist",
		configured, strings.Join(knownBinaries, ", "))
}

// isExecutableProgram reports whether a path is a file that can be executed
// directly — which a script cannot be here, there being no interpreter for it.
func isExecutableProgram(path string) bool {
	information, err := os.Stat(path)
	if err != nil || information.IsDir() || information.Mode().Perm()&0o111 == 0 {
		return false
	}
	return !isScript(path)
}

// isScript reports whether a file begins with the two characters that ask the
// kernel to run it through an interpreter.
func isScript(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()

	header := make([]byte, 2)
	count, err := file.Read(header)
	if err != nil || count < 2 {
		return false
	}
	return header[0] == '#' && header[1] == '!'
}

// realBinaryNear looks for the executable a wrapper script would have run:
// Debian puts it in /usr/lib/<name>/<name>.
func realBinaryNear(script string) string {
	name := filepath.Base(script)
	for _, directory := range []string{"/usr/lib", "/usr/libexec", "/opt"} {
		candidate := filepath.Join(directory, name, name)
		if isExecutableProgram(candidate) {
			return candidate
		}
	}
	return ""
}
