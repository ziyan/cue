// Package drm reads what the kernel knows about the machine's display
// connectors, without an X server and without any privileges.
//
// The kernel exposes one directory per connector under /sys/class/drm, named
// after the card and the connector: card0-HDMI-A-1, card0-eDP-1, card0-DP-2.
// Each contains a "status" file saying connected or disconnected, a "modes"
// file listing what the attached monitor advertises, and an "edid" file with
// the monitor's own description of itself.
//
// This is how the daemon notices a cable being plugged in a few seconds after
// it happens, and how it can report something useful about the screen before
// the X server has started or when it will not start at all.
package drm

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Connector is one physical socket on the machine.
type Connector struct {
	// Name is the X server's name for it: HDMI-1, DP-2, eDP-1. The kernel
	// spells some of them differently and the translation is done here, so
	// that everything above this speaks one vocabulary.
	Name string `json:"name"`

	// KernelName is the directory name under /sys/class/drm, kept because it
	// is what appears in kernel log messages.
	KernelName string `json:"kernelName"`

	Connected bool `json:"connected"`

	// Enabled is the kernel's own view of whether anything is being scanned
	// out to this connector. It is false while X is driving it through a
	// different path, so it is reported rather than relied on.
	Enabled bool `json:"enabled"`

	// Modes are what the monitor advertises, largest first, as "1920x1080".
	Modes []string `json:"modes"`

	// Monitor is what the monitor says it is, read from its EDID. Empty when
	// nothing is plugged in or the monitor did not say.
	Monitor string `json:"monitor"`
}

const sysfsRoot = "/sys/class/drm"

// Connectors lists every display connector on the machine.
func Connectors() ([]Connector, error) {
	entries, err := os.ReadDir(sysfsRoot)
	if err != nil {
		return nil, fmt.Errorf("drm: cannot read %s: %w", sysfsRoot, err)
	}

	connectors := make([]Connector, 0, len(entries))
	for _, entry := range entries {
		directory := filepath.Join(sysfsRoot, entry.Name())
		status, err := readTrimmed(filepath.Join(directory, "status"))
		if err != nil {
			// Entries that are not connectors — the card itself, the render
			// node — have no status file.
			continue
		}

		connector := Connector{
			KernelName: entry.Name(),
			Name:       ConnectorName(entry.Name()),
			Connected:  status == "connected",
		}
		if enabled, err := readTrimmed(filepath.Join(directory, "enabled")); err == nil {
			connector.Enabled = enabled == "enabled"
		}
		if modes, err := readTrimmed(filepath.Join(directory, "modes")); err == nil && modes != "" {
			connector.Modes = strings.Fields(modes)
		}
		if edid, err := os.ReadFile(filepath.Join(directory, "edid")); err == nil && len(edid) >= 128 {
			connector.Monitor = MonitorName(edid)
		}
		connectors = append(connectors, connector)
	}

	sort.Slice(connectors, func(first, second int) bool {
		return connectors[first].Name < connectors[second].Name
	})
	return connectors, nil
}

// ConnectorName translates the kernel's name for a connector into the X
// server's. The kernel writes card0-HDMI-A-1 where X writes HDMI-1, and
// card0-DP-2 where X writes DP-2; the letter in the middle of the HDMI name is
// the physical connector type, which X does not use.
func ConnectorName(kernelName string) string {
	name := kernelName
	if index := strings.Index(name, "-"); index >= 0 && strings.HasPrefix(name, "card") {
		name = name[index+1:]
	}
	// HDMI-A-1 and HDMI-B-1 both become HDMI-1, which is what X calls them.
	for _, prefix := range []string{"HDMI-A-", "HDMI-B-"} {
		if strings.HasPrefix(name, prefix) {
			return "HDMI-" + strings.TrimPrefix(name, prefix)
		}
	}
	return name
}

// MonitorName pulls the monitor's name out of its EDID.
//
// EDID is a 128-byte block a monitor sends over the display cable. Four
// eighteen-byte descriptors start at offset 54; a descriptor whose first two
// bytes are zero is a text field, and the byte at offset 3 says which kind:
// 0xfc is the monitor's name. The text is at offset 5, padded with a newline
// and spaces.
func MonitorName(edid []byte) string {
	const descriptorStart = 54
	const descriptorLength = 18
	const nameTag = 0xfc

	for index := 0; index < 4; index++ {
		offset := descriptorStart + index*descriptorLength
		if offset+descriptorLength > len(edid) {
			break
		}
		descriptor := edid[offset : offset+descriptorLength]
		if descriptor[0] != 0 || descriptor[1] != 0 || descriptor[3] != nameTag {
			continue
		}
		name := string(descriptor[5:])
		if cut := strings.IndexByte(name, '\n'); cut >= 0 {
			name = name[:cut]
		}
		return strings.TrimSpace(name)
	}
	return ""
}

// Fingerprint is a short description of what is plugged in where. Comparing
// two of them is how the daemon notices that a cable has been moved without
// having to diff the whole structure.
func Fingerprint(connectors []Connector) string {
	parts := make([]string, 0, len(connectors))
	for _, connector := range connectors {
		state := "-"
		if connector.Connected {
			state = "+"
			if len(connector.Modes) > 0 {
				state += connector.Modes[0]
			}
		}
		parts = append(parts, connector.Name+state)
	}
	return strings.Join(parts, " ")
}

func readTrimmed(filename string) (string, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}
