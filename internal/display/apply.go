package display

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jezek/xgb/dpms"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"

	"github.com/ziyan/cue/internal/config"
)

// Apply makes the screen look the way the configuration says it should. It is
// safe to call repeatedly — it is called on a timer, which is what makes
// unplugging and replugging an HDMI cable work without anybody doing anything
// — and it reports whether it changed something, so that the caller can log
// the interesting case and stay quiet the rest of the time.
func (self *Display) Apply(settings *config.Display) (bool, error) {
	// The screen saver and power management go first, because they are the
	// difference between a screen that is black at 3am and one that is not,
	// and they are cheap.
	self.applyBlanking(settings)

	if !self.randrAvailable {
		// Xvfb: the size is fixed at start-up and there is nothing to
		// arrange. Not an error — the development configuration and the smoke
		// test both run here.
		return false, nil
	}

	resources, err := randr.GetScreenResourcesCurrent(self.connection, self.root).Reply()
	if err != nil {
		return false, fmt.Errorf("display: cannot read the screen resources: %w", err)
	}
	modes := indexModes(resources)

	if settings.Modeline != "" {
		if err := self.ensureCustomMode(settings, resources, modes); err != nil {
			log.Warningf("cannot add the configured modeline: %s", err)
		} else {
			// The mode list has changed, so read it again before using it.
			if reloaded, err := randr.GetScreenResourcesCurrent(self.connection, self.root).Reply(); err == nil {
				resources = reloaded
				modes = indexModes(resources)
			}
		}
	}

	plan, err := self.plan(settings, resources, modes)
	if err != nil {
		return false, err
	}
	if plan.unchanged {
		return false, nil
	}

	log.Noticef("applying the display layout: %s", plan)
	if err := self.execute(plan, resources); err != nil {
		return false, err
	}
	return true, nil
}

// placement is what one CRTC should be doing when Apply is finished.
type placement struct {
	outputName string
	crtc       randr.Crtc
	output     randr.Output
	mode       randr.Mode
	modeName   string
	x          int
	y          int
	width      int
	height     int
	rotation   uint16
	primary    bool
}

// layout is the whole plan: every CRTC that should be on, every CRTC that
// should be off, and the size the drawing surface has to be for all of it to
// fit.
type layout struct {
	placements   []placement
	disable      []randr.Crtc
	screenWidth  int
	screenHeight int
	primary      randr.Output
	unchanged    bool
}

func (self layout) String() string {
	parts := make([]string, 0, len(self.placements)+1)
	parts = append(parts, fmt.Sprintf("screen %dx%d", self.screenWidth, self.screenHeight))
	for _, placement := range self.placements {
		parts = append(parts, fmt.Sprintf("%s %s at %d,%d", placement.outputName, placement.modeName, placement.x, placement.y))
	}
	if len(self.disable) > 0 {
		parts = append(parts, fmt.Sprintf("%d output(s) off", len(self.disable)))
	}
	return strings.Join(parts, "; ")
}

// plan decides what the screen should look like without changing anything.
// Keeping the decision separate from the change is what makes it possible to
// answer "is this already right?" cheaply, which matters because this runs
// every few seconds.
func (self *Display) plan(settings *config.Display, resources *randr.GetScreenResourcesCurrentReply, modes map[randr.Mode]mode) (layout, error) {
	plan := layout{unchanged: true}

	// A CRTC can drive several outputs but this daemon never asks it to, so
	// they are handed out one at a time and the machine drives as many
	// screens as it has CRTCs.
	usedCrtcs := map[randr.Crtc]bool{}
	nextX := 0

	// Worked out before anything is placed, because it is a property of all
	// the screens together rather than of any one of them.
	mirrorWidth, mirrorHeight, mirroring := self.mirrorSize(settings, resources, modes)

	for _, identifier := range resources.Outputs {
		information, err := randr.GetOutputInfo(self.connection, identifier, resources.ConfigTimestamp).Reply()
		if err != nil {
			continue
		}
		name := string(information.Name)

		if information.Connection != randr.ConnectionConnected {
			// Nothing plugged in. If the server still has it switched on —
			// which happens when a cable is pulled out — turn it off, or the
			// drawing surface stays big enough for a screen that is not there
			// and the browser window is mostly off the edge of the world.
			if information.Crtc != 0 {
				plan.disable = append(plan.disable, information.Crtc)
				plan.unchanged = false
			}
			continue
		}

		outputSettings := settingsFor(settings, name)
		if outputSettings == nil || outputSettings.Mode == config.ModeOff {
			if information.Crtc != 0 {
				plan.disable = append(plan.disable, information.Crtc)
				plan.unchanged = false
			}
			continue
		}

		chosen, err := chooseMode(information, modes, outputSettings, settings)
		if err != nil {
			log.Warningf("%s: %s", name, err)
			continue
		}
		if mirroring {
			// Every screen shows the same thing, so every screen is put on
			// the same mode and in the same place. The mode is the largest
			// one they all have, not this one's favourite.
			matching, found := modeOfSize(information, modes, mirrorWidth, mirrorHeight)
			if !found {
				// Cannot happen: mirrorSize only returns a size every output
				// offered. Handled rather than asserted, because the wrong
				// answer here is a dark screen.
				log.Warningf("%s: cannot mirror at %dx%d after all", name, mirrorWidth, mirrorHeight)
			} else {
				chosen = matching
			}
		}

		crtc := information.Crtc
		if crtc == 0 || usedCrtcs[crtc] {
			crtc = firstFreeCrtc(information.Crtcs, usedCrtcs)
		}
		if crtc == 0 {
			log.Warningf("%s: this machine has no spare display controller for it", name)
			continue
		}
		usedCrtcs[crtc] = true

		rotation := rotationValue(outputSettings.Rotate)
		width, height := chosen.width, chosen.height
		if rotation == uint16(randr.RotationRotate90) || rotation == uint16(randr.RotationRotate270) {
			// A rotated output occupies a rectangle of the screen with its
			// sides swapped, and the screen has to be sized for that rather
			// than for the mode.
			width, height = height, width
		}

		positionX, positionY, advance := positionFor(outputSettings, width, nextX)
		if mirroring {
			positionX, positionY, advance = 0, 0, 0
		}
		nextX += advance

		placement := placement{
			outputName: name,
			crtc:       crtc,
			output:     identifier,
			mode:       chosen.identifier,
			modeName:   chosen.String(),
			x:          positionX,
			y:          positionY,
			width:      width,
			height:     height,
			rotation:   rotation,
			primary:    outputSettings.Primary,
		}
		plan.placements = append(plan.placements, placement)

		if placement.primary {
			plan.primary = identifier
		}

		if !self.alreadyPlaced(placement, resources) {
			plan.unchanged = false
		}
	}

	// The drawing surface has to cover every rectangle on it.
	for _, placement := range plan.placements {
		if right := placement.x + placement.width; right > plan.screenWidth {
			plan.screenWidth = right
		}
		if bottom := placement.y + placement.height; bottom > plan.screenHeight {
			plan.screenHeight = bottom
		}
	}
	if settings.Framebuffer != "" {
		if width, height, err := config.ParseSize(settings.Framebuffer); err == nil {
			plan.screenWidth, plan.screenHeight = width, height
		}
	}
	if plan.screenWidth == 0 || plan.screenHeight == 0 {
		// Every output is off or unplugged. Leave the surface as it is: a
		// zero-sized screen is not a thing the X server will accept, and the
		// browser is better off holding its window than being resized to
		// nothing and back when a cable is jiggled.
		current := self.Screen()
		plan.screenWidth, plan.screenHeight = current.Width, current.Height
	}

	current := self.Screen()
	if current.Width != plan.screenWidth || current.Height != plan.screenHeight {
		plan.unchanged = false
	}

	if plan.primary != 0 {
		if reply, err := randr.GetOutputPrimary(self.connection, self.root).Reply(); err == nil && reply.Output != plan.primary {
			plan.unchanged = false
		}
	}

	if len(plan.placements) == 0 && len(plan.disable) == 0 {
		plan.unchanged = true
	}

	return plan, nil
}

// alreadyPlaced reports whether a CRTC is already doing what the plan wants,
// which is the common case and the reason Apply is cheap enough to run on a
// short timer.
func (self *Display) alreadyPlaced(wanted placement, resources *randr.GetScreenResourcesCurrentReply) bool {
	current, err := randr.GetCrtcInfo(self.connection, wanted.crtc, resources.ConfigTimestamp).Reply()
	if err != nil {
		return false
	}
	if current.Mode != wanted.mode {
		return false
	}
	if int(current.X) != wanted.x || int(current.Y) != wanted.y {
		return false
	}
	if current.Rotation&0x0f != wanted.rotation {
		return false
	}
	if len(current.Outputs) != 1 || current.Outputs[0] != wanted.output {
		return false
	}
	return true
}

// execute applies a plan in the order the X protocol requires: turn off
// anything that is about to be in the way, resize the drawing surface, then
// turn things on. Doing it in any other order fails with a "the new
// configuration does not fit" error that says nothing useful.
func (self *Display) execute(plan layout, resources *randr.GetScreenResourcesCurrentReply) error {
	timestamp := resources.Timestamp
	configTimestamp := resources.ConfigTimestamp

	// Everything that is on gets turned off first. Shrinking the screen with
	// a CRTC still scanning out past the new edge is refused, and working out
	// exactly which ones are in the way is more code than turning them all
	// off for the fraction of a second this takes.
	for _, identifier := range resources.Crtcs {
		if _, err := randr.SetCrtcConfig(self.connection, identifier, timestamp, configTimestamp,
			0, 0, 0, uint16(randr.RotationRotate0), nil).Reply(); err != nil {
			log.Debugf("cannot switch off display controller %d: %s", identifier, err)
		}
	}

	millimetreWidth, millimetreHeight := physicalSize(plan.screenWidth, plan.screenHeight)
	if err := randr.SetScreenSizeChecked(self.connection, self.root,
		uint16(plan.screenWidth), uint16(plan.screenHeight), millimetreWidth, millimetreHeight).Check(); err != nil {
		return fmt.Errorf("display: cannot resize the screen to %dx%d: %w", plan.screenWidth, plan.screenHeight, err)
	}

	for _, placement := range plan.placements {
		reply, err := randr.SetCrtcConfig(self.connection, placement.crtc, timestamp, configTimestamp,
			int16(placement.x), int16(placement.y), placement.mode, placement.rotation,
			[]randr.Output{placement.output}).Reply()
		if err != nil {
			return fmt.Errorf("display: cannot set %s to %s: %w", placement.outputName, placement.modeName, err)
		}
		if reply.Status != randr.SetConfigSuccess {
			return fmt.Errorf("display: the X server refused %s at %s: status %d", placement.outputName, placement.modeName, reply.Status)
		}
		timestamp = reply.Timestamp
	}

	if plan.primary != 0 {
		if err := randr.SetOutputPrimaryChecked(self.connection, self.root, plan.primary).Check(); err != nil {
			log.Warningf("cannot set the primary output: %s", err)
		}
	}

	return nil
}

// chooseMode picks the mode for one output: the one the configuration names,
// the one the monitor prefers, or the largest one it offers.
func chooseMode(information *randr.GetOutputInfoReply, modes map[randr.Mode]mode, outputSettings *config.Output, settings *config.Display) (mode, error) {
	available := make([]mode, 0, len(information.Modes))
	for _, identifier := range information.Modes {
		if described, found := modes[identifier]; found {
			available = append(available, described)
		}
	}
	if len(available) == 0 {
		return mode{}, fmt.Errorf("the monitor offered no modes at all")
	}

	// A custom modeline is selected by name, because its size may be one the
	// monitor also advertises and the point of adding it was to use a
	// different set of timings.
	if settings.Modeline != "" && outputSettings.Mode == settings.ModeName {
		for _, candidate := range available {
			if candidate.name == settings.ModeName {
				return candidate, nil
			}
		}
		return mode{}, fmt.Errorf("the mode %q is not available on this output", settings.ModeName)
	}

	switch outputSettings.Mode {
	case "", config.ModePreferred:
		// NumPreferred says how many of the leading entries the monitor
		// prefers; the first of them is what a monitor means by "my native
		// resolution".
		if information.NumPreferred > 0 && len(available) > 0 {
			return available[0], nil
		}
		return largest(available), nil
	}

	width, height, err := config.ParseSize(outputSettings.Mode)
	if err != nil {
		return mode{}, fmt.Errorf("%q is not a mode this output has", outputSettings.Mode)
	}

	var best *mode
	for index := range available {
		candidate := available[index]
		if candidate.width != width || candidate.height != height {
			continue
		}
		if outputSettings.Rate > 0 {
			// Within half a hertz: a mode advertised as 60Hz is really
			// 59.94, and an operator writing 60 means that one.
			if candidate.rate < outputSettings.Rate-0.5 || candidate.rate > outputSettings.Rate+0.5 {
				continue
			}
			return candidate, nil
		}
		if best == nil || candidate.rate > best.rate {
			chosen := candidate
			best = &chosen
		}
	}
	if best != nil {
		return *best, nil
	}

	sizes := make([]string, 0, len(available))
	for _, candidate := range available {
		sizes = append(sizes, candidate.String())
	}
	return mode{}, fmt.Errorf("%q is not a mode this output has; it offers %s", outputSettings.Mode, strings.Join(sizes, ", "))
}

// positionFor decides where one output sits in the drawing surface, and how
// far the next unpositioned one should be pushed along.
//
// A written position is honoured, always. That sounds obvious and was got
// wrong: the wildcard entry in the default configuration says "0x0", and an
// earlier version laid every output out left to right anyway, so a laptop with
// an external screen ended up with a drawing surface twice as wide and one
// browser window spanning both. Every output at 0,0 means every output shows
// the same top-left corner of the surface — they are mirrored — which is what
// a display appliance almost always wants and what the system this replaces
// did.
//
// Only an entry with no position at all is laid out left to right, which is
// what somebody with two screens on a desk means by leaving it blank.
func positionFor(outputSettings *config.Output, width, nextX int) (int, int, int) {
	if outputSettings.Position != "" {
		if parsedX, parsedY, err := config.ParseSize(outputSettings.Position); err == nil {
			return parsedX, parsedY, 0
		}
	}
	return nextX, 0, width
}

func largest(modes []mode) mode {
	best := modes[0]
	for _, candidate := range modes[1:] {
		if candidate.width*candidate.height > best.width*best.height {
			best = candidate
		} else if candidate.width == best.width && candidate.height == best.height && candidate.rate > best.rate {
			best = candidate
		}
	}
	return best
}

func firstFreeCrtc(candidates []randr.Crtc, used map[randr.Crtc]bool) randr.Crtc {
	for _, candidate := range candidates {
		if !used[candidate] {
			return candidate
		}
	}
	return 0
}

// ensureCustomMode adds the configured modeline to every connected output, for
// the televisions that report timings they cannot actually display. It is
// idempotent: a mode of that name that already exists is reused.
func (self *Display) ensureCustomMode(settings *config.Display, resources *randr.GetScreenResourcesCurrentReply, modes map[randr.Mode]mode) error {
	information, err := parseModeline(settings.Modeline)
	if err != nil {
		return err
	}

	existing := randr.Mode(0)
	for identifier, described := range modes {
		if described.name == settings.ModeName {
			existing = identifier
			break
		}
	}

	if existing == 0 {
		information.NameLen = uint16(len(settings.ModeName))
		reply, err := randr.CreateMode(self.connection, self.root, information, settings.ModeName).Reply()
		if err != nil {
			return fmt.Errorf("display: cannot create the mode %q: %w", settings.ModeName, err)
		}
		existing = reply.Mode
		log.Noticef("added the mode %q from the configured modeline", settings.ModeName)
	}

	for _, identifier := range resources.Outputs {
		outputInformation, err := randr.GetOutputInfo(self.connection, identifier, resources.ConfigTimestamp).Reply()
		if err != nil || outputInformation.Connection != randr.ConnectionConnected {
			continue
		}
		already := false
		for _, candidate := range outputInformation.Modes {
			if candidate == existing {
				already = true
				break
			}
		}
		if already {
			continue
		}
		if err := randr.AddOutputModeChecked(self.connection, identifier, existing).Check(); err != nil {
			log.Warningf("cannot offer the mode %q on %s: %s", settings.ModeName, outputInformation.Name, err)
		}
	}
	return nil
}

// parseModeline reads the format every X tool has used for thirty years: a
// pixel clock in megahertz, then the horizontal timings, then the vertical
// ones, then the sync polarities.
func parseModeline(modeline string) (randr.ModeInfo, error) {
	fields := strings.Fields(modeline)
	if len(fields) < 9 {
		return randr.ModeInfo{}, fmt.Errorf("display: a modeline needs a pixel clock and eight numbers, got %d fields", len(fields))
	}

	clock, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return randr.ModeInfo{}, fmt.Errorf("display: %q is not a pixel clock", fields[0])
	}

	numbers := make([]int, 8)
	for index := 0; index < 8; index++ {
		numbers[index], err = strconv.Atoi(fields[index+1])
		if err != nil {
			return randr.ModeInfo{}, fmt.Errorf("display: %q is not a number", fields[index+1])
		}
	}

	information := randr.ModeInfo{
		DotClock:   uint32(clock * 1000000),
		Width:      uint16(numbers[0]),
		HsyncStart: uint16(numbers[1]),
		HsyncEnd:   uint16(numbers[2]),
		Htotal:     uint16(numbers[3]),
		Height:     uint16(numbers[4]),
		VsyncStart: uint16(numbers[5]),
		VsyncEnd:   uint16(numbers[6]),
		Vtotal:     uint16(numbers[7]),
	}

	const (
		hsyncPositive = 0x001
		hsyncNegative = 0x002
		vsyncPositive = 0x004
		vsyncNegative = 0x008
		interlace     = 0x010
		doubleScan    = 0x020
	)
	for _, flag := range fields[9:] {
		switch strings.ToLower(flag) {
		case "+hsync":
			information.ModeFlags |= hsyncPositive
		case "-hsync":
			information.ModeFlags |= hsyncNegative
		case "+vsync":
			information.ModeFlags |= vsyncPositive
		case "-vsync":
			information.ModeFlags |= vsyncNegative
		case "interlace":
			information.ModeFlags |= interlace
		case "doublescan":
			information.ModeFlags |= doubleScan
		}
	}
	return information, nil
}

// applyBlanking turns off everything that makes a screen go dark on its own.
// A kiosk that blanks because nobody has touched its keyboard for ten minutes
// is reported as broken hardware, and the report arrives days later.
func (self *Display) applyBlanking(settings *config.Display) {
	seconds := int16(settings.BlankAfter.Duration().Seconds())

	// The core screen saver. Zero means never.
	if err := xproto.SetScreenSaverChecked(self.connection, seconds, 0,
		xproto.BlankingNotPreferred, xproto.ExposuresNotAllowed).Check(); err != nil {
		log.Debugf("cannot set the screen saver: %s", err)
	}

	if !self.dpmsAvailable {
		return
	}
	if seconds <= 0 {
		if err := dpms.DisableChecked(self.connection).Check(); err != nil {
			log.Debugf("cannot disable power management: %s", err)
		}
		return
	}
	if err := dpms.EnableChecked(self.connection).Check(); err != nil {
		log.Debugf("cannot enable power management: %s", err)
		return
	}
	// Standby, suspend and off all at the configured time: a display has one
	// useful power state and it is "dark".
	if err := dpms.SetTimeoutsChecked(self.connection, uint16(seconds), uint16(seconds), uint16(seconds)).Check(); err != nil {
		log.Debugf("cannot set the power management timeouts: %s", err)
	}
}

// physicalSize invents a physical size for the drawing surface. X wants one so
// that applications can work out how large a point is; nothing here cares, and
// what matters is only that the number implies a sane density. 96 dots per
// inch is what every desktop assumes, and 25.4 millimetres is an inch.
func physicalSize(width, height int) (uint32, uint32) {
	const dotsPerInch = 96.0
	const millimetresPerInch = 25.4
	return uint32(float64(width) * millimetresPerInch / dotsPerInch),
		uint32(float64(height) * millimetresPerInch / dotsPerInch)
}

// mirrorSize is the largest size every screen that will be switched on can
// show, and whether there is one at all.
//
// Mirroring in RandR is not a mode of its own: it is every output on the same
// mode at the same origin. So the whole of the work is agreeing on a mode, and
// the only ones worth considering are the ones they all have -- a laptop panel
// that offers a single size decides it on its own, which is the usual case.
func (self *Display) mirrorSize(settings *config.Display, resources *randr.GetScreenResourcesCurrentReply, modes map[randr.Mode]mode) (int, int, bool) {
	if !settings.Mirror {
		return 0, 0, false
	}

	offered := make([][]mode, 0, len(resources.Outputs))

	for _, identifier := range resources.Outputs {
		information, err := randr.GetOutputInfo(self.connection, identifier, resources.ConfigTimestamp).Reply()
		if err != nil || information.Connection != randr.ConnectionConnected {
			continue
		}
		outputSettings := settingsFor(settings, string(information.Name))
		if outputSettings == nil || outputSettings.Mode == config.ModeOff {
			continue
		}

		available := make([]mode, 0, len(information.Modes))
		for _, candidate := range information.Modes {
			if described, found := modes[candidate]; found {
				available = append(available, described)
			}
		}
		if len(available) == 0 {
			continue
		}
		offered = append(offered, available)
	}

	width, height, found, reason := largestSharedSize(offered)
	if reason != "" {
		log.Warningf("%s", reason)
	}
	return width, height, found
}

// largestSharedSize is the decision itself, apart from the X server so that it
// can be tested. It returns the biggest size every screen offers, and a reason
// to log when there is none.
func largestSharedSize(offered [][]mode) (int, int, bool, string) {
	// One screen is not a mirror of anything, and it keeps its own preferred
	// mode rather than being talked into the largest size it can manage.
	if len(offered) < 2 {
		return 0, 0, false, ""
	}

	type size struct{ width, height int }
	shared := map[size]bool{}
	for _, described := range offered[0] {
		shared[size{described.width, described.height}] = true
	}
	for _, screen := range offered[1:] {
		has := map[size]bool{}
		for _, described := range screen {
			has[size{described.width, described.height}] = true
		}
		for known := range shared {
			if !has[known] {
				delete(shared, known)
			}
		}
	}

	if len(shared) == 0 {
		return 0, 0, false, "these screens have no mode in common, so they cannot be mirrored; " +
			"they are laid out side by side instead. Set display.mirror to false to stop this being said, " +
			"or give them a size they share"
	}

	best := size{}
	for candidate := range shared {
		if candidate.width*candidate.height > best.width*best.height {
			best = candidate
		}
	}
	return best.width, best.height, true, ""
}

// modeOfSize finds this output's mode of exactly that size, preferring the
// highest refresh rate when it has several.
func modeOfSize(information *randr.GetOutputInfoReply, modes map[randr.Mode]mode, width, height int) (mode, bool) {
	var best *mode
	for _, identifier := range information.Modes {
		described, found := modes[identifier]
		if !found || described.width != width || described.height != height {
			continue
		}
		if best == nil || described.rate > best.rate {
			candidate := described
			best = &candidate
		}
	}
	if best == nil {
		return mode{}, false
	}
	return *best, true
}
