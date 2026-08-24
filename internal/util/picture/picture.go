// Package picture holds the small amount of image work this daemon does for
// itself: making a picture smaller, without a dependency and without asking
// the browser, which cannot be asked without disturbing the screen.
package picture

import (
	"image"
	"image/color"
)

// Shrink scales a picture down to fit inside a width, by averaging.
//
// It exists so that making the picture smaller costs the screen nothing. The
// obvious way — asking Chromium for a scaled capture — re-lays the page out at
// the clipped size while it takes the picture, and that is visible on the wall:
// the dashboard jumps to another size and back, every few seconds, for as long
// as anybody has the interface open. The screen is the product here, and
// nothing shown *about* it may disturb it.
//
// Transparency is carried through. The first version of this threw the alpha
// away and wrote every pixel opaque, which is invisible on a screenshot — the
// screen has no transparent pixels — and put a black box around the mark on
// the wallpaper, because the transparent margin of the logo averaged to opaque
// black.
//
// Averaging rather than sampling because these are photographs from cameras.
// Nearest-neighbour on a camera image at half size is a mess of aliasing, and
// the whole point of the smaller picture is that it still shows what the
// screen shows.
func Shrink(source image.Image, width int) image.Image {
	bounds := source.Bounds()
	if bounds.Dx() <= width || width <= 0 {
		return source
	}

	height := bounds.Dy() * width / bounds.Dx()
	if height < 1 {
		height = 1
	}
	target := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		// The band of source rows this output row stands for.
		top := bounds.Min.Y + y*bounds.Dy()/height
		bottom := bounds.Min.Y + (y+1)*bounds.Dy()/height
		if bottom <= top {
			bottom = top + 1
		}
		for x := 0; x < width; x++ {
			left := bounds.Min.X + x*bounds.Dx()/width
			right := bounds.Min.X + (x+1)*bounds.Dx()/width
			if right <= left {
				right = left + 1
			}

			var red, green, blue, alpha, count uint64
			for sourceY := top; sourceY < bottom; sourceY++ {
				for sourceX := left; sourceX < right; sourceX++ {
					r, g, b, a := source.At(sourceX, sourceY).RGBA()
					// RGBA returns 16 bits per channel, already multiplied by
					// the alpha — which is what makes averaging them and the
					// alpha separately the right thing to do, and what makes
					// the result storable in a color.RGBA, which is premultiplied
					// too.
					red += uint64(r >> 8)
					green += uint64(g >> 8)
					blue += uint64(b >> 8)
					alpha += uint64(a >> 8)
					count++
				}
			}
			if count == 0 {
				continue
			}
			target.Set(x, y, color.RGBA{
				R: uint8(red / count),
				G: uint8(green / count),
				B: uint8(blue / count),
				A: uint8(alpha / count),
			})
		}
	}
	return target
}
