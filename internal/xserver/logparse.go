package xserver

import (
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Reading the X server's own log.
//
// It is the file to look at when a screen is black, and in its raw form it is
// hard to read for two reasons.
//
// Its timestamps are the kernel's monotonic clock — seconds since the machine
// booted — so a line reading "[3885935.672]" is forty-five days after a boot
// nobody remembers, and cannot be compared with anything else. Since that is
// seconds since boot, and the machine knows when it booted, every line can be
// given a real time.
//
// The server does also print a wall clock once, in the line naming its own log
// file, and anchoring to that is the obvious thing to do and is wrong. It
// writes that string in whatever zone its own process has, which is the
// container's — UTC — while the daemon has been told to think in the zone the
// screen is in. Anchoring to it put every line four hours out on the first
// device it was tried on, and the error is invisible: the timestamps look
// perfectly reasonable, they are simply not the times anything happened. The
// boot clock has no zone in it and cannot go wrong that way.
//
// And its severities are two characters in the middle of the text — (EE), (WW)
// — which is enough to grep for and not enough to read a page of.

// LogEntry is one line of the X server's log.
type LogEntry struct {
	// At is the wall-clock time, worked out from the server's own monotonic
	// stamps. Zero when the log gave nothing to anchor them to.
	At time.Time `json:"at,omitempty"`

	// Monotonic is the raw reading, in seconds since the machine booted. Kept
	// because it is what appears in the file, and somebody comparing this
	// with a kernel message needs it.
	Monotonic float64 `json:"monotonic,omitempty"`

	// Severity is "error", "warning", "notice", "info", "config", "probed",
	// "default", "command-line", or "" for a line the server did not mark.
	Severity string `json:"severity,omitempty"`

	// Text is the message with the marker and the timestamp removed.
	Text string `json:"text"`
}

// markers are the two-character codes the X server explains at the top of
// every log it writes.
var markers = map[string]string{
	"(EE)": "error",
	"(WW)": "warning",
	"(!!)": "notice",
	"(II)": "info",
	"(**)": "config",
	"(--)": "probed",
	"(==)": "default",
	"(++)": "command-line",
	"(NI)": "not-implemented",
	"(??)": "unknown",
}

// ParseLog turns the X server's log into entries with real timestamps.
//
// bootTime is when the machine started, which is what the server's stamps are
// measured from. A zero value means it could not be determined, and then the
// readings are reported as they are rather than converted into times that
// would be confidently wrong.
//
// Lines the server wrote as continuations — indented, with no stamp of their
// own — are joined to the line above, because on their own they are fragments
// and the thing they continue is what gives them meaning.
func ParseLog(content string, bootTime time.Time) []LogEntry {
	lines := strings.Split(content, "\n")
	anchored := !bootTime.IsZero()

	entries := make([]LogEntry, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		monotonic, rest, stamped := splitStamp(line)
		if !stamped {
			// A continuation of the line before it.
			if len(entries) > 0 {
				entries[len(entries)-1].Text += " " + strings.TrimSpace(line)
				continue
			}
			rest = line
		}

		entry := LogEntry{Text: strings.TrimSpace(rest)}
		if stamped {
			entry.Monotonic = monotonic
			if anchored {
				entry.At = bootTime.Add(time.Duration(monotonic * float64(time.Second)))
			}
		}
		entry.Severity, entry.Text = splitMarker(entry.Text)
		entries = append(entries, entry)
	}
	return entries
}

// BootTime is the instant the X server's timestamps are measured from.
//
// The server stamps with CLOCK_MONOTONIC, so that is the clock read here.
// /proc/uptime looks like the obvious source and is the wrong one: on a modern
// kernel its first field is CLOCK_BOOTTIME, which keeps counting while the
// machine is suspended, and CLOCK_MONOTONIC does not. On a laptop that has
// been closed and opened a few times the two are days apart — on the first one
// this was checked against, 1.39 days — and every converted timestamp is out
// by exactly the time the machine spent asleep, while looking perfectly
// plausible.
func BootTime() (time.Time, bool) {
	var reading unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &reading); err != nil {
		return time.Time{}, false
	}
	elapsed := time.Duration(reading.Sec)*time.Second + time.Duration(reading.Nsec)
	return time.Now().Add(-elapsed), true
}

// splitStamp takes the "[  1234.567]" off the front of a line.
func splitStamp(line string) (monotonic float64, rest string, ok bool) {
	if !strings.HasPrefix(line, "[") {
		return 0, line, false
	}
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return 0, line, false
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(line[1:end]), 64)
	if err != nil {
		return 0, line, false
	}
	return value, line[end+1:], true
}

// splitMarker takes the "(EE)" off the front of a message and names it.
func splitMarker(text string) (severity, rest string) {
	if len(text) < 4 {
		return "", text
	}
	name, found := markers[text[:4]]
	if !found {
		return "", text
	}
	return name, strings.TrimSpace(text[4:])
}
