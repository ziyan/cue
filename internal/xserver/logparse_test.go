package xserver

import (
	"strings"
	"testing"
	"time"
)

// Taken from a real device: the X server's timestamps are the kernel's
// monotonic clock, and the only line that pairs one with a wall clock is the
// one where it names its own log file.
const realLog = `[3885935.672] 
X.Org X Server 1.21.1.16
X Protocol Version 11, Revision 0
[3885935.672] Current Operating System: Linux carbon 6.12.94+deb13-amd64
[3885935.672] 	Before reporting problems, check http://wiki.x.org
	to make sure that you have the latest version.
[3885935.680] (++) Log file: "/run/cue/xorg.log", Time: Mon Aug 24 08:50:15 2026
[3885935.700] (==) Using system config directory "/usr/share/X11/xorg.conf.d"
[3885936.700] (WW) VGA arbiter: cannot open kernel arbiter, no multi-card support
[3885937.200] (EE) open /dev/fb0: No such file or directory
`

func TestTheMonotonicStampsBecomeRealTimes(t *testing.T) {
	// "[3885935.672]" is seconds since the machine booted: forty-five days
	// after a boot nobody remembers, and comparable with nothing. The server
	// prints the wall clock once, beside a monotonic reading, and everything
	// else follows from that.
	entries := ParseLog(realLog)
	if len(entries) == 0 {
		t.Fatal("nothing was parsed")
	}

	anchor := time.Date(2026, time.August, 24, 8, 50, 15, 0, time.Local)
	var checked int
	for _, entry := range entries {
		if entry.Monotonic == 0 {
			continue
		}
		if entry.At.IsZero() {
			t.Fatalf("%q kept its monotonic stamp and got no real time", entry.Text)
		}
		// The anchor line is at 3885935.680; a line 1.02 seconds later must
		// land 1.02 seconds after the wall time it was paired with.
		expected := anchor.Add(time.Duration((entry.Monotonic - 3885935.680) * float64(time.Second)))
		if difference := entry.At.Sub(expected); difference > time.Millisecond || difference < -time.Millisecond {
			t.Errorf("%q is at %s, want %s", entry.Text, entry.At, expected)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no stamped lines were checked, so this proved nothing")
	}
}

func TestSeverityComesOutOfTheMiddleOfTheText(t *testing.T) {
	entries := ParseLog(realLog)

	found := map[string]string{}
	for _, entry := range entries {
		if entry.Severity != "" {
			found[entry.Severity] = entry.Text
		}
	}
	for _, want := range []string{"error", "warning", "default", "command-line"} {
		if _, ok := found[want]; !ok {
			t.Errorf("no %s line was recognised; found %v", want, found)
		}
	}
	// And the marker is taken off, rather than left in the message.
	if text := found["error"]; strings.Contains(text, "(EE)") {
		t.Errorf("the marker is still in the text: %q", text)
	}
	if text := found["error"]; !strings.Contains(text, "/dev/fb0") {
		t.Errorf("the message was lost with the marker: %q", text)
	}
}

func TestAContinuationJoinsTheLineItContinues(t *testing.T) {
	// "to make sure that you have the latest version." is a fragment on its
	// own. The line above is what gives it meaning.
	entries := ParseLog(realLog)
	for _, entry := range entries {
		if strings.Contains(entry.Text, "Before reporting problems") {
			if !strings.Contains(entry.Text, "latest version") {
				t.Errorf("the continuation was dropped or left alone: %q", entry.Text)
			}
			return
		}
	}
	t.Fatal("the line with a continuation was not found at all")
}

func TestALogWithNoAnchorStillParses(t *testing.T) {
	// A log truncated past its header, which is what a tail of the last two
	// hundred lines usually is. There is nothing to convert against, so the
	// monotonic readings are reported as they are rather than as a wall time
	// invented from nothing.
	entries := ParseLog("[100.5] (EE) something went wrong\n[101.0] (II) and then this\n")
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	for _, entry := range entries {
		if !entry.At.IsZero() {
			t.Errorf("%q was given a real time with nothing to anchor it to: %s", entry.Text, entry.At)
		}
		if entry.Monotonic == 0 {
			t.Errorf("%q lost its monotonic stamp too", entry.Text)
		}
	}
	if entries[0].Severity != "error" {
		t.Errorf("severity was not read without an anchor: %q", entries[0].Severity)
	}
}

func TestAnEmptyLogIsNotAnEntry(t *testing.T) {
	if entries := ParseLog("\n\n   \n"); len(entries) != 0 {
		t.Errorf("blank lines became %d entries", len(entries))
	}
}
