package display

import (
	"fmt"

	"github.com/jezek/xgb/xproto"
)

// FocusTopWindow gives the keyboard to the largest window on the screen, and
// raises it.
//
// This is the one job of a window manager that this daemon cannot do without.
// There is no window manager in the image — there is deliberately nothing in
// it but the five programs that have to be there — and without one, nothing
// ever gives a window the input focus. A browser that has never been focused
// decides its window is not really being looked at: it paints once and then
// stops, so the screen holds the first frame for ever while the browser
// carries on working perfectly and answering questions about a page nobody
// can see.
//
// Everything else a window manager would do is unwanted here: no decorations,
// no stacking, no moving. So instead of running one, the daemon does this.
func (self *Display) FocusTopWindow() error {
	tree, err := xproto.QueryTree(self.connection, self.root).Reply()
	if err != nil {
		return fmt.Errorf("display: cannot list the windows: %w", err)
	}

	best := xproto.Window(0)
	bestArea := 0

	for _, window := range tree.Children {
		attributes, err := xproto.GetWindowAttributes(self.connection, window).Reply()
		if err != nil || attributes.MapState != xproto.MapStateViewable {
			continue
		}
		// Windows the application asked the server not to manage are menus
		// and tooltips, not the page.
		if attributes.OverrideRedirect {
			continue
		}

		geometry, err := xproto.GetGeometry(self.connection, xproto.Drawable(window)).Reply()
		if err != nil {
			continue
		}
		area := int(geometry.Width) * int(geometry.Height)
		if area > bestArea {
			best, bestArea = window, area
		}
	}

	if best == 0 {
		return fmt.Errorf("display: there is no window on the screen to focus")
	}

	// Raise it, then give it the keyboard. The order matters only in that a
	// window nobody can see is not worth focusing.
	values := []uint32{xproto.StackModeAbove}
	if err := xproto.ConfigureWindowChecked(self.connection, best,
		xproto.ConfigWindowStackMode, values).Check(); err != nil {
		log.Debugf("cannot raise the window: %s", err)
	}

	if err := xproto.SetInputFocusChecked(self.connection,
		xproto.InputFocusPointerRoot, best, xproto.TimeCurrentTime).Check(); err != nil {
		return fmt.Errorf("display: cannot give the window the keyboard: %w", err)
	}

	log.Debugf("gave the keyboard to the largest window on the screen (%d pixels)", bestArea)
	return nil
}

// FocusedWindow reports which window currently has the keyboard, so that the
// daemon can tell whether it needs to do anything.
func (self *Display) FocusedWindow() (xproto.Window, error) {
	reply, err := xproto.GetInputFocus(self.connection).Reply()
	if err != nil {
		return 0, fmt.Errorf("display: cannot ask what has the keyboard: %w", err)
	}
	return reply.Focus, nil
}
