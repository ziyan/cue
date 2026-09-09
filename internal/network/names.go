package network

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Interface names, and why they are checked here as well as in the
// configuration.
//
// A name is joined into several file paths -- wpa_supplicant's control socket,
// the configuration written for it, the scan file, the socket this process
// binds to answer on. A name containing a separator puts those somewhere else
// entirely, and "somewhere else" for a file this program writes as root is not
// a small thing.
//
// config.Validate already refuses such a name when the file is read, and that
// is the door most of them come through. This is the other kind of check: at
// the point of use, where the value is about to become a path, so that the
// reasoning is local and does not depend on how the name arrived. A name that
// reached here another way -- a future caller, a test, something read from the
// system -- is checked the same.

// interfaceNamePattern is what the kernel will accept: up to fifteen
// characters, letters, digits, hyphen and underscore. Nothing that works is
// being forbidden.
var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,15}$`)

// checkedInterfaceName returns the name if it is one, and an error otherwise.
func checkedInterfaceName(name string) (string, error) {
	if !interfaceNamePattern.MatchString(name) {
		return "", fmt.Errorf("network: %q is not an interface name", name)
	}
	// Belt and braces, and the part a scanner can see: whatever the pattern
	// allowed, what goes into a path is the last element of it and nothing
	// that could climb out of a directory.
	cleaned := filepath.Base(filepath.Clean("/" + name))
	if cleaned != name || strings.ContainsAny(cleaned, `/\`) || cleaned == "." || cleaned == ".." {
		return "", fmt.Errorf("network: %q is not an interface name", name)
	}
	return cleaned, nil
}
