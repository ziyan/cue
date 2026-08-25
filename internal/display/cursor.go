package display

import (
	"fmt"

	"github.com/jezek/xgb/xfixes"
	"github.com/jezek/xgb/xproto"
)

// The pointer on a screen nobody is standing at.
//
// A wall display wants no cursor: an arrow parked in the middle of a dashboard
// is the sort of thing people photograph and send to you. But a screen with a
// mouse or a touchscreen attached wants one the moment somebody touches it,
// and "-nocursor" on the X server's command line is not a setting that can be
// changed afterwards — it means the server has no cursor at all, ever.
//
// So the server keeps its cursor and this hides it, through XFIXES, which is
// the extension built for exactly this and the only thing that does it.
//
// The first attempt put an empty cursor on the root window instead, on the
// reasoning that adding a protocol to hide a mouse pointer was not worth it.
// That reasoning was wrong twice over. It does not work: a cursor is a
// per-window attribute, the browser sets its own on its own window, and the
// browser's window covers the screen — so the root window's cursor is the one
// thing nobody ever sees. And it cost nothing anyway: xfixes is another
// package of the X bindings this already depends on, not another dependency.
//
// XFIXES hides the cursor for the whole screen regardless of which window it
// is over, which is the behaviour wanted and the behaviour no amount of
// per-window fiddling can produce.

// available reports whether the server has XFIXES, which every X server this
// runs on does, but Xvfb in a test might not.
func (self *Display) cursorControlAvailable() bool {
	if self.xfixesChecked {
		return self.xfixesAvailable
	}
	self.xfixesChecked = true

	if err := xfixes.Init(self.connection); err != nil {
		return false
	}
	// The version has to be negotiated before any other request, or the
	// server answers every one of them with a protocol error.
	if _, err := xfixes.QueryVersion(self.connection, 4, 0).Reply(); err != nil {
		return false
	}
	self.xfixesAvailable = true
	return true
}

// ShowPointer draws the cursor again.
func (self *Display) ShowPointer() error {
	if !self.cursorControlAvailable() {
		return fmt.Errorf("display: this X server has no XFIXES, so the pointer cannot be shown or hidden")
	}
	if !self.pointerHidden {
		return nil
	}
	if err := xfixes.ShowCursorChecked(self.connection, self.root).Check(); err != nil {
		return fmt.Errorf("display: cannot show the pointer: %w", err)
	}
	self.pointerHidden = false
	return nil
}

// HidePointer draws nothing where the pointer is, over every window.
//
// Hiding and showing are counted by the server: two hides need two shows. So
// this keeps track and never asks twice, which is what makes it safe to call
// from a loop that does not know what it asked for last time.
func (self *Display) HidePointer() error {
	if !self.cursorControlAvailable() {
		return fmt.Errorf("display: this X server has no XFIXES, so the pointer cannot be shown or hidden")
	}
	if self.pointerHidden {
		return nil
	}
	if err := xfixes.HideCursorChecked(self.connection, self.root).Check(); err != nil {
		return fmt.Errorf("display: cannot hide the pointer: %w", err)
	}
	self.pointerHidden = true
	return nil
}

// Pointer is where the pointer is now, in screen coordinates.
func (self *Display) Pointer() (x, y int, err error) {
	reply, err := xproto.QueryPointer(self.connection, self.root).Reply()
	if err != nil {
		return 0, 0, fmt.Errorf("display: cannot ask where the pointer is: %w", err)
	}
	return int(reply.RootX), int(reply.RootY), nil
}
