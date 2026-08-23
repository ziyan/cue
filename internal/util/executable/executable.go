// Package executable finds the program to run behind a name, stepping over
// the wrapper scripts a distribution puts in the way.
//
// This exists because of a trap that costs an hour every time it is met. On
// Debian, several of the programs this project supervises are not what their
// name suggests:
//
//	/usr/bin/chromium   a shell script that runs /usr/lib/chromium/chromium
//	/usr/bin/Xorg       a shell script that runs /usr/lib/xorg/Xorg
//
// The image has no shell, so executing either of them fails — and the failure
// surfaces as the program exiting immediately with nothing in its output but a
// complaint from a shell nobody knew was involved. So a wrapper is detected by
// its first two bytes and the real executable is found instead.
package executable

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Resolve returns a path that can actually be executed.
//
// It tries, in order: the name as given (looked up on PATH if it has no
// separator in it), the real executable a wrapper of that name would have run,
// and then each of the fallbacks. A path that is a script is never returned.
func Resolve(name string, fallbacks ...string) (string, error) {
	candidate := name
	if candidate != "" && !strings.ContainsRune(candidate, os.PathSeparator) {
		if found, err := exec.LookPath(candidate); err == nil {
			candidate = found
		}
	}

	if IsExecutableProgram(candidate) {
		return candidate, nil
	}

	if IsScript(candidate) {
		if real := behindWrapper(candidate); real != "" {
			return real, nil
		}
	}

	for _, fallback := range fallbacks {
		if IsExecutableProgram(fallback) {
			return fallback, nil
		}
		if IsScript(fallback) {
			if real := behindWrapper(fallback); real != "" {
				return real, nil
			}
		}
	}

	if len(fallbacks) == 0 {
		return "", fmt.Errorf("executable: %q is not a program that can be run", name)
	}
	return "", fmt.Errorf("executable: %q is not a program that can be run, and none of %s exist either",
		name, strings.Join(fallbacks, ", "))
}

// IsExecutableProgram reports whether a path is a file that can be executed
// directly — which a script cannot be here, there being no interpreter for it.
func IsExecutableProgram(path string) bool {
	if path == "" {
		return false
	}
	information, err := os.Stat(path)
	if err != nil || information.IsDir() || information.Mode().Perm()&0o111 == 0 {
		return false
	}
	return !IsScript(path)
}

// IsScript reports whether a file begins with the two characters that ask the
// kernel to run it through an interpreter.
func IsScript(path string) bool {
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

// behindWrapper looks for the executable a wrapper script would have run.
// Debian's convention is /usr/lib/<name>/<name>, which covers both the cases
// this project meets.
func behindWrapper(script string) string {
	name := filepath.Base(script)
	for _, directory := range []string{"/usr/lib", "/usr/libexec", "/usr/local/lib", "/opt"} {
		candidate := filepath.Join(directory, name, name)
		if IsExecutableProgram(candidate) {
			return candidate
		}
	}
	return ""
}
