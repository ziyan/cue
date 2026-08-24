package daemon

import (
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// displayRestartNeeded decides whether a configuration change can be applied
// to a running X server or needs a new one. Getting it wrong in one direction
// blanks the screen for no reason; in the other, a setting is accepted through
// the interface and silently does nothing, which is how switching the server
// from xorg to xvfb appeared to work and did not.
func TestWhatNeedsTheXServerRestartedAndWhatDoesNot(t *testing.T) {
	running := config.Default().Display

	cases := map[string]struct {
		change func(*config.Display)
		needed bool
	}{
		"the server itself": {
			func(display *config.Display) { display.Server = config.ServerXvfb }, true,
		},
		"the display number": {
			func(display *config.Display) { display.Number = 3 }, true,
		},
		"taking the server's cursor away, which is a command line flag": {
			func(display *config.Display) { display.Cursor = config.CursorHidden }, true,
		},
		"showing a cursor the server already has, which the daemon does itself": {
			func(display *config.Display) { display.Cursor = config.CursorAlways }, false,
		},
		"an output's mode, which RandR can change": {
			func(display *config.Display) { display.Outputs[0].Mode = "1280x720" }, false,
		},
		"the framebuffer on a real server, which RandR can change": {
			func(display *config.Display) { display.Framebuffer = "1920x1080" }, false,
		},
		"nothing at all": {
			func(display *config.Display) {}, false,
		},
	}

	for what, expected := range cases {
		updated := config.Default()
		expected.change(&updated.Display)

		daemon := &Daemon{}
		if got := daemon.displayRestartWouldBeNeeded(running, updated); got != expected.needed {
			t.Errorf("changing %s: restart=%v, want %v", what, got, expected.needed)
		}
	}
}

func TestTheVirtualServersSizeIsFixedWhenItStarts(t *testing.T) {
	// Xvfb takes its screen size on the command line and RandR cannot change
	// it, so unlike a real server this one does need restarting for it.
	running := config.Default().Display
	running.Server = config.ServerXvfb
	running.Framebuffer = "1280x720"

	updated := config.Default()
	updated.Display.Server = config.ServerXvfb
	updated.Display.Framebuffer = "1920x1080"

	daemon := &Daemon{}
	if !daemon.displayRestartWouldBeNeeded(running, updated) {
		t.Error("resizing a virtual screen was accepted without a restart, so it would have done nothing")
	}
}
