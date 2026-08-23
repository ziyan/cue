package display

import (
	"testing"

	"github.com/jezek/xgb/randr"

	"github.com/ziyan/cue/internal/config"
)

// The mode list a monitor of this kind advertises: its native resolution
// first, marked preferred, then a set of smaller fallbacks.
func testModes() map[randr.Mode]mode {
	return map[randr.Mode]mode{
		1: {identifier: 1, name: "1920x1080", width: 1920, height: 1080, rate: 60},
		2: {identifier: 2, name: "1920x1080", width: 1920, height: 1080, rate: 50},
		3: {identifier: 3, name: "1280x720", width: 1280, height: 720, rate: 60},
		4: {identifier: 4, name: "3840x2160", width: 3840, height: 2160, rate: 30},
		9: {identifier: 9, name: "cue", width: 1920, height: 1080, rate: 60},
	}
}

func testOutput(modes []randr.Mode, preferred uint16) *randr.GetOutputInfoReply {
	return &randr.GetOutputInfoReply{
		Modes:        modes,
		NumModes:     uint16(len(modes)),
		NumPreferred: preferred,
		Name:         []byte("HDMI-1"),
		Connection:   randr.ConnectionConnected,
	}
}

func TestPreferredTakesTheMonitorsOwnChoice(t *testing.T) {
	// "Preferred" is the monitor saying what its panel actually is. Choosing
	// the largest instead would drive a 1080p television at 4K over a cable
	// that cannot carry it.
	output := testOutput([]randr.Mode{1, 2, 3}, 1)
	settings := &config.Output{Mode: config.ModePreferred}

	chosen, err := chooseMode(output, testModes(), settings, &config.Display{})
	if err != nil {
		t.Fatalf("choose: %s", err)
	}
	if chosen.identifier != 1 {
		t.Errorf("chose %s, want the first preferred mode", chosen)
	}
}

func TestPreferredFallsBackToTheLargestWhenTheMonitorSaysNothing(t *testing.T) {
	output := testOutput([]randr.Mode{3, 1, 4}, 0)
	settings := &config.Output{Mode: config.ModePreferred}

	chosen, err := chooseMode(output, testModes(), settings, &config.Display{})
	if err != nil {
		t.Fatalf("choose: %s", err)
	}
	if chosen.width != 3840 {
		t.Errorf("chose %s, want the largest of what was offered", chosen)
	}
}

func TestAnExplicitSizeTakesTheFastestOfThatSize(t *testing.T) {
	// A television that offers 1920x1080 at both 50 and 60 hertz should be
	// driven at 60 unless told otherwise; 50 is for broadcast.
	output := testOutput([]randr.Mode{2, 1, 3}, 0)
	settings := &config.Output{Mode: "1920x1080"}

	chosen, err := chooseMode(output, testModes(), settings, &config.Display{})
	if err != nil {
		t.Fatalf("choose: %s", err)
	}
	if chosen.rate != 60 {
		t.Errorf("chose %s, want the 60Hz mode", chosen)
	}
}

func TestAnExplicitRateIsHonoured(t *testing.T) {
	output := testOutput([]randr.Mode{1, 2}, 0)
	settings := &config.Output{Mode: "1920x1080", Rate: 50}

	chosen, err := chooseMode(output, testModes(), settings, &config.Display{})
	if err != nil {
		t.Fatalf("choose: %s", err)
	}
	if chosen.rate != 50 {
		t.Errorf("chose %s, want the 50Hz mode", chosen)
	}
}

func TestARateIsMatchedWithinHalfAHertz(t *testing.T) {
	// A mode advertised as 60Hz is really 59.94, and an operator writing 60
	// means that one.
	modes := testModes()
	modes[5] = mode{identifier: 5, name: "1920x1080", width: 1920, height: 1080, rate: 59.94}
	output := testOutput([]randr.Mode{5}, 0)
	settings := &config.Output{Mode: "1920x1080", Rate: 60}

	chosen, err := chooseMode(output, modes, settings, &config.Display{})
	if err != nil {
		t.Fatalf("choose: %s", err)
	}
	if chosen.identifier != 5 {
		t.Errorf("chose %s; 59.94 should match a request for 60", chosen)
	}
}

func TestAModeTheMonitorDoesNotHaveIsRefusedWithTheListItDoesHave(t *testing.T) {
	output := testOutput([]randr.Mode{1, 3}, 0)
	settings := &config.Output{Mode: "2560x1440"}

	_, err := chooseMode(output, testModes(), settings, &config.Display{})
	if err == nil {
		t.Fatal("a mode the monitor does not offer should be refused")
	}
	// The message has to list what is available, or the operator is left
	// guessing what to write instead.
	for _, expected := range []string{"1920x1080", "1280x720"} {
		if !contains(err.Error(), expected) {
			t.Errorf("the error does not mention %s: %s", expected, err)
		}
	}
}

func TestACustomModelineIsSelectedByName(t *testing.T) {
	// The point of a custom modeline is different timings at a size the
	// monitor also claims to support, so it has to be selected by name.
	output := testOutput([]randr.Mode{1, 9}, 1)
	display := &config.Display{
		Modeline: "173.00 1920 2048 2248 2576 1080 1083 1088 1120 -hsync +vsync",
		ModeName: "cue",
	}
	settings := &config.Output{Mode: "cue"}

	chosen, err := chooseMode(output, testModes(), settings, display)
	if err != nil {
		t.Fatalf("choose: %s", err)
	}
	if chosen.name != "cue" {
		t.Errorf("chose %s, want the mode named cue", chosen)
	}
}

func TestSettingsForPrefersAnExactNameOverTheWildcard(t *testing.T) {
	settings := &config.Display{Outputs: []config.Output{
		{Name: "*", Mode: config.ModePreferred},
		{Name: "HDMI-1", Mode: "1280x720"},
	}}

	if chosen := settingsFor(settings, "HDMI-1"); chosen == nil || chosen.Mode != "1280x720" {
		t.Errorf("HDMI-1 got %v, want its own entry", chosen)
	}
	if chosen := settingsFor(settings, "DP-2"); chosen == nil || chosen.Mode != config.ModePreferred {
		t.Errorf("DP-2 got %v, want the wildcard entry", chosen)
	}
}

func TestSettingsForReturnsNothingWhenNoEntryMatches(t *testing.T) {
	// An output nothing names is left alone rather than being switched on with
	// invented settings.
	settings := &config.Display{Outputs: []config.Output{{Name: "HDMI-1"}}}
	if chosen := settingsFor(settings, "DP-2"); chosen != nil {
		t.Errorf("DP-2 got %v, want nothing", chosen)
	}
}

func TestParseModelineReadsTheFormatEveryXToolUses(t *testing.T) {
	information, err := parseModeline("173.00 1920 2048 2248 2576 1080 1083 1088 1120 -hsync +vsync")
	if err != nil {
		t.Fatalf("parse: %s", err)
	}
	if information.Width != 1920 || information.Height != 1080 {
		t.Errorf("the size is %dx%d, want 1920x1080", information.Width, information.Height)
	}
	if information.DotClock != 173000000 {
		t.Errorf("the pixel clock is %d, want 173000000", information.DotClock)
	}
	if information.Htotal != 2576 || information.Vtotal != 1120 {
		t.Errorf("the totals are %d and %d, want 2576 and 1120", information.Htotal, information.Vtotal)
	}
	const hsyncNegative = 0x002
	const vsyncPositive = 0x004
	if information.ModeFlags&hsyncNegative == 0 || information.ModeFlags&vsyncPositive == 0 {
		t.Errorf("the sync polarities were not read: flags are %#x", information.ModeFlags)
	}
}

func TestParseModelineRefusesSomethingThatIsNotOne(t *testing.T) {
	for _, modeline := range []string{"", "1920x1080", "173.00 1920 2048"} {
		if _, err := parseModeline(modeline); err == nil {
			t.Errorf("%q was accepted as a modeline", modeline)
		}
	}
}

func TestRefreshRateIsWorkedOutFromTheTimings(t *testing.T) {
	// 148.5 MHz over 2200x1125 pixels is 60Hz, which is the standard 1080p60
	// timing and a useful check that the arithmetic is the right way up.
	rate := refreshRate(randr.ModeInfo{DotClock: 148500000, Htotal: 2200, Vtotal: 1125})
	if rate < 59.9 || rate > 60.1 {
		t.Errorf("the rate is %.2f, want 60", rate)
	}
}

func TestRefreshRateOfAModeWithNoTimingsIsZeroRatherThanInfinite(t *testing.T) {
	if rate := refreshRate(randr.ModeInfo{DotClock: 148500000}); rate != 0 {
		t.Errorf("the rate is %v, want 0", rate)
	}
}

func TestPhysicalSizeImpliesTheDensityEveryDesktopAssumes(t *testing.T) {
	width, height := physicalSize(1920, 1080)
	// 1920 pixels at 96 per inch is 20 inches, which is 508 millimetres.
	if width < 500 || width > 515 {
		t.Errorf("1920 pixels became %d millimetres, want about 508", width)
	}
	if height < 280 || height > 292 {
		t.Errorf("1080 pixels became %d millimetres, want about 286", height)
	}
}

func TestRotationNamesRoundTrip(t *testing.T) {
	for _, name := range []string{"normal", "left", "right", "inverted"} {
		if back := rotationName(rotationValue(name)); back != name {
			t.Errorf("%q became %q", name, back)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for index := 0; index+len(needle) <= len(haystack); index++ {
			if haystack[index:index+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
