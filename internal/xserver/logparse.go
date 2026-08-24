package xserver

import (
	"strconv"
	"strings"
	"time"
)

// Reading the X server's own log.
//
// It is the file to look at when a screen is black, and in its raw form it is
// hard to read for two reasons.
//
// Its timestamps are the kernel's monotonic clock — seconds since the machine
// booted — so a line reading "[3885935.672]" is forty-five days after a boot
// nobody remembers, and cannot be compared with anything else. The server does
// print the wall-clock time, once, in the line that names its own log file,
// and that pairs a monotonic reading with a real one. Everything else can be
// converted from that.
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
// Lines the server wrote as continuations — indented, with no stamp of their
// own — are joined to the line above, because on their own they are fragments
// and the thing they continue is what gives them meaning.
func ParseLog(content string) []LogEntry {
	lines := strings.Split(content, "\n")

	monotonicAt, wallAt, anchored := findAnchor(lines)

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
				entry.At = wallAt.Add(time.Duration((monotonic - monotonicAt) * float64(time.Second)))
			}
		}
		entry.Severity, entry.Text = splitMarker(entry.Text)
		entries = append(entries, entry)
	}
	return entries
}

// findAnchor looks for the one line that gives both clocks: the server names
// its own log file and prints the date while doing it.
//
//	[3885935.672] (++) Log file: "/run/cue/xorg.log", Time: Mon Aug 24 08:50:15 2026
func findAnchor(lines []string) (monotonic float64, wall time.Time, ok bool) {
	const marker = ", Time: "
	for _, line := range lines {
		index := strings.Index(line, marker)
		if index < 0 || !strings.Contains(line, "Log file:") {
			continue
		}
		stamp, _, stamped := splitStamp(line)
		if !stamped {
			continue
		}
		// The X server writes this with the C library's own format.
		when, err := time.ParseInLocation("Mon Jan  2 15:04:05 2006",
			strings.TrimSpace(line[index+len(marker):]), time.Local)
		if err != nil {
			continue
		}
		return stamp, when, true
	}
	return 0, time.Time{}, false
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
