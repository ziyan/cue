package display

import (
	"fmt"

	"github.com/jezek/xgb/xproto"
)

// FitWindowsToScreen resizes every mapped top-level window to fill the screen,
// and reports how many it moved.
//
// This is the second job of a window manager the daemon cannot do without, and
// it was missing. Chromium sizes its window when it starts and then leaves it
// alone: there is no window manager here to resize anything, and Chromium does
// not follow a RandR screen change by itself. So a screen that grows -- an
// external display plugged into a laptop, a monitor that negotiates a larger
// mode than it did at boot -- ends up with the page in the top-left corner and
// black around two sides of it, and a screen that shrinks has the page clipped.
// Both were seen on a device: 1024x768 of browser sitting in a 1280x1024
// screen, with the rest black.
//
// Restarting the browser would also fix it and is what the daemon used to do
// for changes it could not apply. It is the wrong tool here: the screen goes
// black for several seconds, any page that was signed in has to sign in again,
// and this happens at exactly the moment somebody is standing at the display
// plugging a cable in and watching it.
func (self *Display) FitWindowsToScreen() (int, error) {
	screen := self.Screen()
	if screen.Width == 0 || screen.Height == 0 {
		return 0, nil
	}

	tree, err := xproto.QueryTree(self.connection, self.root).Reply()
	if err != nil {
		return 0, fmt.Errorf("display: cannot list the windows: %w", err)
	}

	fitted := 0
	for _, window := range tree.Children {
		attributes, err := xproto.GetWindowAttributes(self.connection, window).Reply()
		if err != nil || attributes.MapState != xproto.MapStateViewable {
			continue
		}
		// The same exclusion as focusing: override-redirect windows are the
		// menus and tooltips an application places itself, and stretching one
		// of those over the screen would be worse than leaving it alone.
		if attributes.OverrideRedirect {
			continue
		}

		geometry, err := xproto.GetGeometry(self.connection, xproto.Drawable(window)).Reply()
		if err != nil {
			continue
		}
		if geometry.X == 0 && geometry.Y == 0 &&
			int(geometry.Width) == screen.Width && int(geometry.Height) == screen.Height {
			continue
		}

		values := []uint32{0, 0, uint32(screen.Width), uint32(screen.Height)}
		if err := xproto.ConfigureWindowChecked(self.connection, window,
			xproto.ConfigWindowX|xproto.ConfigWindowY|
				xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
			values).Check(); err != nil {
			log.Debugf("cannot resize a window to the screen: %s", err)
			continue
		}
		fitted++
	}
	return fitted, nil
}
