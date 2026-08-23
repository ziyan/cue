package config

import (
	"time"
)

// DefaultFilename is where the configuration lives unless --config says
// otherwise. It matches the path the container image mounts.
const DefaultFilename = "/etc/cue/cue.yaml"

// Default returns a configuration that would work on a device nobody has
// looked at yet: it drives whatever outputs are connected at whatever
// resolution they ask for, shows a page served by the daemon itself, and
// exposes the web interface. Every field here is the answer to "what should
// happen if the operator says nothing".
func Default() *Configuration {
	return &Configuration{
		Device: Device{
			Name: "cue",
		},
		Log: Log{
			Level: "INFO",
		},
		Paths: Paths{
			State:   "/var/lib/cue",
			Runtime: "/run/cue",
		},
		Display: Display{
			Server: ServerXorg,
			Number: 0,
			Cursor: false,
			Outputs: []Output{
				{Name: "*", Mode: ModePreferred, Position: "0x0", Rotate: "normal"},
			},
			ModeName:          "cue",
			BlankAfter:        0,
			ReconcileInterval: Duration(5 * time.Second),
		},
		Browser: Browser{
			Binary:         "chromium",
			User:           "cue",
			Sandbox:        true,
			EphemeralCache: true,
			DebuggingPort:  9222,
		},
		Playlist: Playlist{
			Interval: Duration(30 * time.Second),
			Items:    nil,
		},
		Watchdog: Watchdog{
			Enabled:                      true,
			Interval:                     Duration(15 * time.Second),
			Timeout:                      Duration(10 * time.Second),
			FailuresBeforeReload:         2,
			FailuresBeforeRecreate:       4,
			FailuresBeforeClearCache:     6,
			FailuresBeforeRestart:        8,
			FailuresBeforeRestartDisplay: 16,
		},
		VNC: VNC{
			Enabled:  true,
			Listen:   "127.0.0.1:5900",
			ViewOnly: false,
		},
		Web: Web{
			Listen:          ":8080",
			SessionLifetime: Duration(30 * 24 * time.Hour),
		},
		Audio: Audio{
			Enabled: true,
			Volume:  70,
		},
		Time: Time{
			Enabled: true,
			Servers: []string{"pool.ntp.org"},
		},
		Fleet: Fleet{
			Enabled: false,
			URL:     "https://cue.sh",
		},
	}
}

// The values Display.Server accepts.
const (
	// ServerXorg drives real graphics hardware. This is what a device uses.
	ServerXorg = "xorg"

	// ServerXvfb renders into memory with no hardware at all. This is what a
	// developer's machine and the smoke test use, and it is the reason the
	// whole daemon can be exercised in continuous integration.
	ServerXvfb = "xvfb"
)

// The values Output.Mode accepts beyond an explicit size.
const (
	ModePreferred = "preferred"
	ModeOff       = "off"
)

// The values Output.Rotate accepts.
var rotations = []string{"normal", "left", "right", "inverted"}
