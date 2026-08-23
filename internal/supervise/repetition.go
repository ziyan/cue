package supervise

import (
	"strings"
	"sync"
	"time"
)

// Repetition describes a line a supervised program writes over and over for a
// condition that is understood and is not going to change.
//
// The X server is the reason this exists. With no D-Bus system bus to connect
// to — and there is none, because nothing in this image needs one — it reports
// the failure as an error and retries every ten seconds, for as long as the
// machine is switched on. On a display left running for a year that is three
// million lines, and on one carbon had been running for an hour it was 61% of
// everything in the log. The problem is not the disk, it is that an operator
// looking for the reason a screen went blank has to read past six of these per
// minute, and that a line marked (EE) reads as a fault when it is not one.
//
// Suppressing output is a thing to be careful with, so this does not hide
// anything: the first occurrence is logged in full together with an
// explanation of why it does not matter, and the ones after it are counted and
// reported on a slow timer. Nothing that has not been recognised in advance is
// ever touched.
type Repetition struct {
	// Contains is matched against the line as a substring.
	Contains string

	// Explanation says why the condition is expected and what, if anything,
	// is lost by it. It is logged once, with the first occurrence.
	Explanation string
}

// repetitionInterval is how often the count of a suppressed line is reported.
// Long enough that it is not itself noise; short enough that a condition which
// has stopped happening stops being reported.
const repetitionInterval = time.Hour

type repetitionCounter struct {
	mutex      sync.Mutex
	seen       map[string]int
	lastReport map[string]time.Time
}

func newRepetitionCounter() *repetitionCounter {
	return &repetitionCounter{seen: map[string]int{}, lastReport: map[string]time.Time{}}
}

// consider reports whether the line was recognised as a known repetition, and
// what should be logged in its place. An unrecognised line is never claimed.
//
//	handled false                    — log the line as usual
//	handled true, message non-empty  — log the message instead
//	handled true, message empty      — log nothing
func (self *repetitionCounter) consider(repetitions []Repetition, name, line string, now time.Time) (message string, handled bool) {
	for _, repetition := range repetitions {
		if repetition.Contains == "" || !strings.Contains(line, repetition.Contains) {
			continue
		}

		self.mutex.Lock()
		defer self.mutex.Unlock()

		count := self.seen[repetition.Contains]
		self.seen[repetition.Contains] = count + 1

		if count == 0 {
			// The first one is logged whole, with the reason, so that this
			// never turns into a program quietly hiding its own output.
			self.lastReport[repetition.Contains] = now
			return line + " — " + repetition.Explanation +
				" This is expected, and further occurrences are counted rather than logged.", true
		}

		since := now.Sub(self.lastReport[repetition.Contains])
		if since < repetitionInterval {
			return "", true
		}
		self.lastReport[repetition.Contains] = now
		return repetition.Explanation + " Still happening: " +
			plural(count, "more time", "more times") + " since the last report.", true
	}
	return "", false
}

func plural(count int, singular, many string) string {
	word := many
	if count == 1 {
		word = singular
	}
	return itoa(count) + " " + word
}

// itoa avoids importing strconv for one call in a package that has no other
// need of it.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
