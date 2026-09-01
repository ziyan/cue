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

func TestAWrittenPositionIsHonoured(t *testing.T) {
	// The failure this pins down: the wildcard entry in the default
	// configuration says "0x0", and an earlier version laid every output out
	// left to right anyway. On a laptop with an external screen that gave a
	// drawing surface twice as wide and one browser window spanning both,
	// which is not what anybody means by a kiosk.
	settings := &config.Output{Name: "*", Position: "0x0"}

	first, firstY, advance := positionFor(settings, 1920, 0)
	if first != 0 || firstY != 0 || advance != 0 {
		t.Errorf("the first output went to %d,%d advancing %d; want 0,0 advancing 0", first, firstY, advance)
	}

	// The second output, with the same wildcard entry, must land in the same
	// place — that is what mirroring is.
	second, secondY, advance := positionFor(settings, 1920, 0)
	if second != 0 || secondY != 0 || advance != 0 {
		t.Errorf("the second output went to %d,%d; want 0,0, mirroring the first", second, secondY)
	}
}

func TestAnOutputWithNoPositionIsLaidOutLeftToRight(t *testing.T) {
	settings := &config.Output{Name: "*"}

	first, _, advance := positionFor(settings, 1920, 0)
	if first != 0 || advance != 1920 {
		t.Errorf("the first output went to %d advancing %d; want 0 advancing 1920", first, advance)
	}

	second, _, advance := positionFor(settings, 2560, first+advance)
	if second != 1920 || advance != 2560 {
		t.Errorf("the second output went to %d advancing %d; want 1920 advancing 2560", second, advance)
	}
}

func TestAnExplicitPositionPlacesAnOutputWhereItSays(t *testing.T) {
	settings := &config.Output{Name: "HDMI-1", Position: "1920x0"}
	positionX, positionY, advance := positionFor(settings, 1920, 0)
	if positionX != 1920 || positionY != 0 {
		t.Errorf("the output went to %d,%d, want 1920,0", positionX, positionY)
	}
	if advance != 0 {
		t.Errorf("an output placed by hand advanced the automatic layout by %d, want 0", advance)
	}
}

func TestAPositionThatIsNotOneFallsBackToTheAutomaticLayout(t *testing.T) {
	// A typo must not put the screen somewhere nobody can see it.
	settings := &config.Output{Name: "*", Position: "nonsense"}
	positionX, _, advance := positionFor(settings, 1920, 640)
	if positionX != 640 || advance != 1920 {
		t.Errorf("a bad position gave %d advancing %d; want the automatic layout", positionX, advance)
	}
}

// Mirroring is every screen on one mode at one origin, so the whole of the
// decision is which mode they can all manage. The case that prompted this: a
// laptop panel with a single 2560x1440 mode and a 4K television. Laid out side
// by side they made a 6400x2160 desktop with the page stretched across both
// and half of it on each, which is not what plugging a second screen into a
// display daemon is for.
func TestMirroringPicksTheLargestSizeEveryScreenHas(t *testing.T) {
	panel := []mode{{width: 2560, height: 1440, rate: 60}}
	television := []mode{
		{width: 3840, height: 2160, rate: 30},
		{width: 2560, height: 1440, rate: 60},
		{width: 1920, height: 1080, rate: 60},
	}

	width, height, found, reason := largestSharedSize([][]mode{panel, television})
	if !found {
		t.Fatalf("no shared size was found, and they share 2560x1440: %s", reason)
	}
	if width != 2560 || height != 1440 {
		t.Errorf("mirroring at %dx%d; 2560x1440 is the largest they both have", width, height)
	}
}

func TestMirroringPrefersTheLargestSharedSizeOverASmallerOne(t *testing.T) {
	one := []mode{{width: 1920, height: 1080}, {width: 1280, height: 720}}
	two := []mode{{width: 1920, height: 1080}, {width: 1280, height: 720}, {width: 3840, height: 2160}}

	width, height, found, _ := largestSharedSize([][]mode{one, two})
	if !found || width != 1920 || height != 1080 {
		t.Errorf("mirroring at %dx%d; 1920x1080 is the largest shared size", width, height)
	}
}

// Side by side is the fallback rather than blanking one of them, and it is
// said out loud: a screen that quietly shows nothing is the failure worth
// going out of the way to avoid.
func TestScreensWithNothingInCommonAreNotMirrored(t *testing.T) {
	one := []mode{{width: 2560, height: 1440}}
	two := []mode{{width: 1920, height: 1080}}

	_, _, found, reason := largestSharedSize([][]mode{one, two})
	if found {
		t.Error("two screens with no shared mode were mirrored anyway")
	}
	if reason == "" {
		t.Error("nothing was said about why they are side by side")
	}
}

// One screen keeps its own preferred mode. Without this, a single 4K screen
// whose mode list happens to be led by something smaller would be talked onto
// the wrong one by a mirroring rule with nothing to mirror.
func TestOneScreenIsNotMirrored(t *testing.T) {
	if _, _, found, _ := largestSharedSize([][]mode{{{width: 3840, height: 2160}}}); found {
		t.Error("a single screen was treated as a mirror")
	}
	if _, _, found, _ := largestSharedSize(nil); found {
		t.Error("no screens at all were treated as a mirror")
	}
}

// The rate is not part of agreeing on a size, but it is part of driving one:
// a television that offers 2560x1440 at 30 and at 60 should be given 60.
func TestMirroringTakesTheFastestModeOfTheAgreedSize(t *testing.T) {
	modes := map[randr.Mode]mode{
		1: {identifier: 1, width: 2560, height: 1440, rate: 30},
		2: {identifier: 2, width: 2560, height: 1440, rate: 60},
		3: {identifier: 3, width: 1920, height: 1080, rate: 60},
	}
	information := testOutput([]randr.Mode{1, 2, 3}, 0)

	chosen, found := modeOfSize(information, modes, 2560, 1440)
	if !found {
		t.Fatal("2560x1440 was not found on an output that has it twice")
	}
	if chosen.rate != 60 {
		t.Errorf("chose %gHz; 60 is the faster of the two", chosen.rate)
	}
}
