package display

import (
	"context"
	"fmt"
	"image"
	"image/color"

	"github.com/jezek/xgb/xproto"
)

// Capture reads the pixels the X server is actually showing, straight from the
// root window.
//
// This is ground truth, and it is the only thing in this project that is. The
// browser's own screenshot is what the browser believes it drew, which is not
// the same thing: a window that was never sized to the screen, a compositor
// that stopped painting, a second program covering the page — none of those
// show up in a picture the browser takes of itself. This shows them.
//
// The image is read in horizontal strips because a single request for a whole
// screen exceeds what the X protocol will return in one reply: a 4K screen is
// thirty-three megabytes and the limit is a quarter of that.
func (self *Display) Capture(ctx context.Context) (image.Image, error) {
	setup := xproto.Setup(self.connection).DefaultScreen(self.connection)
	width, height := int(setup.WidthInPixels), int(setup.HeightInPixels)
	if width == 0 || height == 0 {
		return nil, fmt.Errorf("display: the screen has no size")
	}

	picture := image.NewRGBA(image.Rect(0, 0, width, height))

	// Four bytes a pixel, and a comfortable margin under the protocol's own
	// limit so that this works on a server with a small maximum request size.
	const bytesPerPixel = 4
	rows := (2 << 20) / (width * bytesPerPixel)
	if rows < 1 {
		rows = 1
	}

	for top := 0; top < height; top += rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		strip := rows
		if top+strip > height {
			strip = height - top
		}

		reply, err := xproto.GetImage(self.connection, xproto.ImageFormatZPixmap,
			xproto.Drawable(self.root), 0, int16(top), uint16(width), uint16(strip),
			0xffffffff).Reply()
		if err != nil {
			return nil, fmt.Errorf("display: cannot read the screen at row %d: %w", top, err)
		}

		copyStrip(picture, reply.Data, width, strip, top, int(reply.Depth))
	}

	return picture, nil
}

// copyStrip turns one band of the server's pixels into the image. The server
// sends them in its own order, which on every machine this runs on is blue,
// green, red, then a byte that means nothing.
func copyStrip(picture *image.RGBA, data []byte, width, rows, top, depth int) {
	const bytesPerPixel = 4

	for row := 0; row < rows; row++ {
		for column := 0; column < width; column++ {
			offset := (row*width + column) * bytesPerPixel
			if offset+2 >= len(data) {
				return
			}
			picture.SetRGBA(column, top+row, color.RGBA{
				R: data[offset+2],
				G: data[offset+1],
				B: data[offset],
				A: 0xff,
			})
		}
	}
}
