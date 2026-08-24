package display

import (
	"fmt"

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
// So the server keeps its cursor and this hides it: an empty one is put on the
// root window, and taken off again when the pointer moves. Nothing here needs
// an X extension, which matters because the one built for this — XFIXES — is
// not in the vendored protocol bindings, and vendoring a protocol to hide a
// mouse pointer is the wrong trade.

// hiddenCursor makes a cursor with nothing in it.
//
// A cursor is two 1-bit pixmaps, the shape and its mask. A mask of all zeroes
// means every pixel is transparent, so the cursor is there — the pointer still
// moves, still clicks, still reports where it is — and draws nothing.
func (self *Display) hiddenCursor() (xproto.Cursor, error) {
	shape, err := xproto.NewPixmapId(self.connection)
	if err != nil {
		return 0, fmt.Errorf("display: cannot make a cursor: %w", err)
	}
	// Depth 1: a bitmap, which is what a cursor is made of.
	if err := xproto.CreatePixmapChecked(self.connection, 1, shape,
		xproto.Drawable(self.root), 1, 1).Check(); err != nil {
		return 0, fmt.Errorf("display: cannot make a cursor shape: %w", err)
	}
	defer xproto.FreePixmap(self.connection, shape)

	// A newly created pixmap has undefined contents, so it is cleared: an
	// uninitialised bit here is a single stray dot on the screen, which is
	// worse than the arrow it replaces.
	context, err := xproto.NewGcontextId(self.connection)
	if err != nil {
		return 0, fmt.Errorf("display: cannot make a cursor: %w", err)
	}
	if err := xproto.CreateGCChecked(self.connection, context, xproto.Drawable(shape),
		xproto.GcForeground, []uint32{0}).Check(); err != nil {
		return 0, fmt.Errorf("display: cannot prepare a cursor: %w", err)
	}
	defer xproto.FreeGC(self.connection, context)
	if err := xproto.PolyFillRectangleChecked(self.connection, xproto.Drawable(shape), context,
		[]xproto.Rectangle{{X: 0, Y: 0, Width: 1, Height: 1}}).Check(); err != nil {
		return 0, fmt.Errorf("display: cannot clear a cursor: %w", err)
	}

	cursor, err := xproto.NewCursorId(self.connection)
	if err != nil {
		return 0, fmt.Errorf("display: cannot make a cursor: %w", err)
	}
	// The same empty bitmap as both shape and mask: nothing is drawn, and
	// nothing is drawn through.
	if err := xproto.CreateCursorChecked(self.connection, cursor, shape, shape,
		0, 0, 0, 0, 0, 0, 0, 0).Check(); err != nil {
		return 0, fmt.Errorf("display: cannot make a cursor: %w", err)
	}
	return cursor, nil
}

// ShowPointer puts the server's own cursor back on the root window.
func (self *Display) ShowPointer() error {
	if err := xproto.ChangeWindowAttributesChecked(self.connection, self.root,
		xproto.CwCursor, []uint32{uint32(xproto.CursorNone)}).Check(); err != nil {
		return fmt.Errorf("display: cannot show the pointer: %w", err)
	}
	return nil
}

// HidePointer draws nothing where the pointer is.
func (self *Display) HidePointer() error {
	if self.hidden == 0 {
		cursor, err := self.hiddenCursor()
		if err != nil {
			return err
		}
		self.hidden = cursor
	}
	if err := xproto.ChangeWindowAttributesChecked(self.connection, self.root,
		xproto.CwCursor, []uint32{uint32(self.hidden)}).Check(); err != nil {
		return fmt.Errorf("display: cannot hide the pointer: %w", err)
	}
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
