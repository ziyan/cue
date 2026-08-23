package supervise

import (
	"strings"
	"testing"
	"time"
)

var testRepetitions = []Repetition{{
	Contains:    "error connecting to system bus",
	Explanation: "There is no D-Bus system bus in this image and nothing needs one.",
}}

const busLine = `(EE) dbus-core: error connecting to system bus: FileNotFound`

func TestTheFirstOccurrenceIsLoggedWholeWithItsReason(t *testing.T) {
	counter := newRepetitionCounter()
	now := time.Unix(1700000000, 0)

	message, handled := counter.consider(testRepetitions, "xorg", busLine, now)
	if !handled {
		t.Fatal("the line was not recognised")
	}
	// Whatever else this does, it must never be the reason somebody could not
	// find out what their X server said.
	if !strings.Contains(message, busLine) {
		t.Errorf("the first occurrence lost the line itself: %s", message)
	}
	if !strings.Contains(message, "no D-Bus system bus") {
		t.Errorf("the first occurrence lost the explanation: %s", message)
	}
}

func TestTheOnesAfterItAreCountedRatherThanLogged(t *testing.T) {
	counter := newRepetitionCounter()
	now := time.Unix(1700000000, 0)
	counter.consider(testRepetitions, "xorg", busLine, now)

	// Ten seconds apart, which is what the X server actually does.
	for index := 1; index < 100; index++ {
		message, handled := counter.consider(testRepetitions, "xorg", busLine, now.Add(time.Duration(index)*10*time.Second))
		if !handled {
			t.Fatalf("occurrence %d was not recognised", index)
		}
		if message != "" {
			t.Fatalf("occurrence %d was logged after only %ds: %s", index, index*10, message)
		}
	}
}

func TestItSaysSoAgainOnceAnHour(t *testing.T) {
	counter := newRepetitionCounter()
	start := time.Unix(1700000000, 0)
	counter.consider(testRepetitions, "xorg", busLine, start)

	for index := 1; index <= 360; index++ {
		counter.consider(testRepetitions, "xorg", busLine, start.Add(time.Duration(index)*10*time.Second))
	}

	message, handled := counter.consider(testRepetitions, "xorg", busLine, start.Add(2*time.Hour))
	if !handled || message == "" {
		t.Fatal("nothing was reported after two hours of a condition that is still happening")
	}
	if !strings.Contains(message, "361 more times") {
		t.Errorf("the report did not say how many were suppressed: %s", message)
	}
}

func TestALineThatWasNotRecognisedIsNeverTouched(t *testing.T) {
	counter := newRepetitionCounter()
	now := time.Unix(1700000000, 0)

	for _, line := range []string{
		"(EE) Failed to load module \"modesetting\"",
		"(EE) no screens found",
		"",
		"error connecting to something else entirely",
	} {
		if _, handled := counter.consider(testRepetitions, "xorg", line, now); handled {
			t.Errorf("claimed a line it should not have: %q", line)
		}
	}
}

func TestAnEmptyPatternMatchesNothing(t *testing.T) {
	// An empty Contains would otherwise match every line, and a supervised
	// program would go silent because of a typo in a table.
	counter := newRepetitionCounter()
	if _, handled := counter.consider([]Repetition{{Explanation: "x"}}, "xorg", "anything at all", time.Unix(0, 0)); handled {
		t.Error("an empty pattern swallowed a line")
	}
}

func TestTwoConditionsAreCountedSeparately(t *testing.T) {
	repetitions := []Repetition{
		{Contains: "first", Explanation: "the first."},
		{Contains: "second", Explanation: "the second."},
	}
	counter := newRepetitionCounter()
	now := time.Unix(1700000000, 0)

	firstMessage, _ := counter.consider(repetitions, "xorg", "a first line", now)
	secondMessage, _ := counter.consider(repetitions, "xorg", "a second line", now)

	if firstMessage == "" || secondMessage == "" {
		t.Fatal("one condition swallowed the other's first occurrence")
	}
	if !strings.Contains(secondMessage, "the second.") {
		t.Errorf("the second condition was explained as the first: %s", secondMessage)
	}
}
