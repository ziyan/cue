// Package devices works out which groups a process needs to be in to use the
// machine's hardware.
//
// This exists because of a mismatch that is invisible until something is
// slow. The browser runs as an unprivileged account — it has to, because
// Chromium refuses to enable its own sandbox as root — but the graphics
// device is owned by root and readable only by a group:
//
//	crw-rw---- 1 root video   226,   0 /dev/dri/card0
//	crw-rw---- 1 root render  226, 128 /dev/dri/renderD128
//
// Inside a container those groups have whatever numbers the *host* gave them,
// and the host's numbers are not the container's: "render" is 992 on one
// machine and 993 on another, and a group of that name may not exist in the
// image at all. Writing a number into the image would be wrong on somebody
// else's machine.
//
// So the numbers are taken from the device files themselves, at run time, by
// the daemon — which is root and can see them. The browser is then started in
// those groups and can open the hardware. Without it, Chromium falls back to
// rendering in software, which on a screen showing video is the difference
// between smooth and unwatchable, and which reports itself only as a line in a
// log nobody reads.
package devices

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// Groups returns the distinct group numbers owning the device files at or
// under the given paths. A path that does not exist is skipped: a machine
// with no sound card is an ordinary machine.
func Groups(paths ...string) []uint32 {
	seen := map[uint32]bool{}

	for _, path := range paths {
		information, err := os.Stat(path)
		if err != nil {
			continue
		}

		if !information.IsDir() {
			if group, ok := groupOf(information); ok {
				seen[group] = true
			}
			continue
		}

		// One level is enough: /dev/dri, /dev/snd and /dev/input all hold
		// their device nodes directly, and walking deeper only reaches the
		// by-path and by-id directories of symbolic links.
		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			nested, err := entry.Info()
			if err != nil {
				continue
			}
			if group, ok := groupOf(nested); ok {
				seen[group] = true
			}
		}
	}

	groups := make([]uint32, 0, len(seen))
	for group := range seen {
		groups = append(groups, group)
	}
	// Sorted so that the same machine produces the same list every time,
	// which makes a log line about it comparable between restarts.
	sort.Slice(groups, func(first, second int) bool { return groups[first] < groups[second] })
	return groups
}

// groupOf reads the owning group of a file. It reports false rather than zero
// for anything it cannot read, because group zero is root and adding a process
// to it would be the opposite of what this package is for.
func groupOf(information fs.FileInfo) (uint32, bool) {
	status, ok := information.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	if status.Gid == 0 {
		return 0, false
	}
	return status.Gid, true
}

// Describe is a one-line summary for the log, so that a device rendering in
// software can be diagnosed from its log rather than by standing next to it.
func Describe(paths []string) string {
	groups := Groups(paths...)
	if len(groups) == 0 {
		return "no hardware groups were found; is /dev/dri passed through?"
	}

	text := "using hardware groups"
	for _, group := range groups {
		text += " " + itoa(group)
	}
	return text
}

func itoa(value uint32) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 10)
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for left, right := 0, len(digits)-1; left < right; left, right = left+1, right-1 {
		digits[left], digits[right] = digits[right], digits[left]
	}
	return string(digits)
}

// Hardware are the directories whose device files an unprivileged process
// needs to be in the groups of: the graphics device, the sound cards, and the
// keyboards and touchscreens.
var Hardware = []string{
	filepath.Clean("/dev/dri"),
	filepath.Clean("/dev/snd"),
	filepath.Clean("/dev/input"),
}
