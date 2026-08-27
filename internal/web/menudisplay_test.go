package web

import (
	"strings"
	"testing"
)

// A monitor reports everything it will accept, which is commonly thirty or
// forty entries: the same handful of sizes at nine refresh rates each, plus
// sizes nobody has wanted since cathode ray tubes. Offering that to somebody
// standing at a screen with a mouse is offering them a haystack.
func TestOnlyTheSizesWorthOfferingAreOffered(t *testing.T) {
	// What a real monitor says, in the shape it says it.
	reported := []string{
		"1920x1080@60", "1920x1080@59.94", "1920x1080@50", "1920x1080@30",
		"1920x1080@29.97", "1920x1080@25", "1920x1080@24", "1920x1080@23.98",
		"1680x1050@59.88", "1600x900@60", "1280x1024@75", "1280x1024@60",
		"1280x720@60", "1280x720@59.94", "1280x720@50",
		"1024x768@75", "1024x768@70", "1024x768@60",
		"800x600@75", "800x600@72", "800x600@60", "800x600@56",
		"720x576@50", "720x480@60", "640x480@75", "640x480@73", "640x480@60",
	}

	offered := everydayModes(reported, "1920x1080@60")

	if len(offered) > 8 {
		t.Errorf("offered %d sizes: %v", len(offered), offered)
	}
	if offered[0] != "1920x1080@60" {
		t.Errorf("the monitor's own preference is not first: %v", offered)
	}

	// One entry per size, so nobody has to choose between eight ways of
	// saying 1920x1080.
	seen := map[string]bool{}
	for _, mode := range offered {
		size := sizeOf(mode)
		if seen[size] {
			t.Errorf("%s is offered more than once: %v", size, offered)
		}
		seen[size] = true
	}

	// Largest first, because that is the order somebody looks in.
	if !strings.HasPrefix(offered[1], "1680x1050") {
		t.Errorf("the sizes are not largest first: %v", offered)
	}
}

// A monitor that reports one mode is not a reason to show an empty list.
func TestAMonitorWithOneModeStillOffersIt(t *testing.T) {
	offered := everydayModes([]string{"1280x1024@60"}, "1280x1024@60")
	if len(offered) != 1 || offered[0] != "1280x1024@60" {
		t.Errorf("offered %v", offered)
	}
}

// And one that reports nothing useful must not produce a list of empty
// entries.
func TestAMonitorThatSaysNothingOffersNothing(t *testing.T) {
	if offered := everydayModes(nil, ""); len(offered) != 0 {
		t.Errorf("offered %v for a monitor that said nothing", offered)
	}
}
