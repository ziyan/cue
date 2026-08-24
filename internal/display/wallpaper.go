package display

import (
	"fmt"
	"image"

	"github.com/jezek/xgb/xproto"
)

// SetRootBackground paints a picture onto the root window and leaves it there.
//
// It is what the screen shows before the browser has drawn anything, and what
// it falls back to if the browser goes away. Without it that is whatever the X
// server leaves behind, which on most drivers is black and on some is the
// stipple pattern from 1987.
//
// The picture is sent in horizontal strips for the same reason the screen is
// read in them: a single request carrying a whole 4K frame is thirty-three
// megabytes and the X protocol will not take a quarter of that.
func (self *Display) SetRootBackground(picture image.Image) error {
	bounds := picture.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return fmt.Errorf("display: a background needs a size")
	}

	depth, err := self.rootDepth()
	if err != nil {
		return err
	}

	pixmap, err := xproto.NewPixmapId(self.connection)
	if err != nil {
		return fmt.Errorf("display: cannot make a background: %w", err)
	}
	if err := xproto.CreatePixmapChecked(self.connection, depth, pixmap,
		xproto.Drawable(self.root), uint16(width), uint16(height)).Check(); err != nil {
		return fmt.Errorf("display: cannot make a background of %dx%d: %w", width, height, err)
	}
	// Freed at the end: once it is the window's background the server has its
	// own reference, and holding one here for the life of the daemon would
	// keep a whole screen of pixels twice over.
	defer xproto.FreePixmap(self.connection, pixmap)

	context, err := xproto.NewGcontextId(self.connection)
	if err != nil {
		return fmt.Errorf("display: cannot make a background: %w", err)
	}
	if err := xproto.CreateGCChecked(self.connection, context, xproto.Drawable(pixmap),
		0, nil).Check(); err != nil {
		return fmt.Errorf("display: cannot prepare a background: %w", err)
	}
	defer xproto.FreeGC(self.connection, context)

	// Four bytes a pixel, blue first, which is what every server this runs on
	// wants and what Capture reads back.
	const bytesPerPixel = 4
	// Two megabytes a strip, the same bound Capture reads with.
	rowsPerStrip := (2 << 20) / (width * bytesPerPixel)
	if rowsPerStrip < 1 {
		rowsPerStrip = 1
	}

	data := make([]byte, width*rowsPerStrip*bytesPerPixel)
	for top := 0; top < height; top += rowsPerStrip {
		rows := rowsPerStrip
		if top+rows > height {
			rows = height - top
		}

		offset := 0
		for y := 0; y < rows; y++ {
			for x := 0; x < width; x++ {
				red, green, blue, _ := picture.At(bounds.Min.X+x, bounds.Min.Y+top+y).RGBA()
				data[offset+0] = byte(blue >> 8)
				data[offset+1] = byte(green >> 8)
				data[offset+2] = byte(red >> 8)
				data[offset+3] = 0
				offset += bytesPerPixel
			}
		}

		if err := xproto.PutImageChecked(self.connection, xproto.ImageFormatZPixmap,
			xproto.Drawable(pixmap), context,
			uint16(width), uint16(rows), 0, int16(top), 0, depth,
			data[:offset]).Check(); err != nil {
			return fmt.Errorf("display: cannot draw the background: %w", err)
		}
	}

	if err := xproto.ChangeWindowAttributesChecked(self.connection, self.root,
		xproto.CwBackPixmap, []uint32{uint32(pixmap)}).Check(); err != nil {
		return fmt.Errorf("display: cannot set the background: %w", err)
	}
	// Setting the background does not repaint what is already there.
	if err := xproto.ClearAreaChecked(self.connection, false, self.root, 0, 0, 0, 0).Check(); err != nil {
		return fmt.Errorf("display: cannot repaint the background: %w", err)
	}
	return nil
}

// rootDepth is how many bits a pixel is on this screen.
func (self *Display) rootDepth() (byte, error) {
	geometry, err := xproto.GetGeometry(self.connection, xproto.Drawable(self.root)).Reply()
	if err != nil {
		return 0, fmt.Errorf("display: cannot ask about the root window: %w", err)
	}
	return geometry.Depth, nil
}
