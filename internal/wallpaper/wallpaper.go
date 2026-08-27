// Package wallpaper draws what the screen shows when the browser is not
// showing anything: at start-up, in the seconds before Chromium has painted,
// and again if it goes away.
//
// Without it that time is whatever the X server leaves behind — black on most
// drivers, and on some the grey stipple pattern from 1987, which looks exactly
// like a machine that has failed to boot. On a wall, in front of people, the
// difference between "starting" and "broken" is worth a picture.
package wallpaper

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"github.com/ziyan/cue/internal/util/picture"
)

// logo is this project's mark, rendered once at a size no screen will need to
// enlarge and embedded so that the daemon has no file to find at run time.
//
//go:embed logo.png
var logoPNG []byte

// background is the colour behind it: a very dark grey rather than black, so
// that a screen showing the wallpaper is visibly *on* and not merely dark.
var background = color.RGBA{R: 0x11, G: 0x12, B: 0x14, A: 0xff}

// logoFraction is how much of the shorter side of the screen the mark takes.
// Small enough to look deliberate, large enough to read across a room. The
// mark is landscape and has a transparent margin of its own, so what is drawn
// is smaller than this again.
const logoFraction = 5

// Draw makes a picture the size of the screen with the mark in the middle.
func Draw(width, height int) image.Image {
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)

	mark, err := png.Decode(bytes.NewReader(logoPNG))
	if err != nil {
		// The mark is compiled in, so this cannot happen unless the build is
		// wrong; a plain background is still better than nothing on a screen.
		return canvas
	}

	shorter := height
	if width < height {
		shorter = width
	}
	size := shorter / logoFraction
	if size < 1 {
		return canvas
	}
	if size < mark.Bounds().Dx() {
		mark = picture.Shrink(mark, size)
	}

	at := image.Point{
		X: (width - mark.Bounds().Dx()) / 2,
		Y: (height - mark.Bounds().Dy()) / 2,
	}
	// Over, not Src: the mark has transparent corners and has to sit on the
	// background rather than punch a hole in it.
	draw.Draw(canvas, image.Rectangle{Min: at, Max: at.Add(mark.Bounds().Size())},
		mark, mark.Bounds().Min, draw.Over)
	return canvas
}

// Mark is this project's logo on its own, for anything that wants to show it
// small: the menu somebody opens at the screen, for one.
//
// It returns nil rather than an error if the mark cannot be decoded. It is
// compiled in, so that cannot happen unless the build is broken, and a missing
// picture is not a reason to fail whatever wanted it.
func Mark() image.Image {
	mark, err := png.Decode(bytes.NewReader(logoPNG))
	if err != nil {
		return nil
	}
	return mark
}
