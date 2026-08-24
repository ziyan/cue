package wallpaper

import (
	"image"
	"testing"
)

func TestTheWallpaperIsTheSizeOfTheScreen(t *testing.T) {
	for _, size := range []image.Point{{X: 1280, Y: 720}, {X: 2560, Y: 1440}, {X: 1280, Y: 1024}} {
		drawn := Draw(size.X, size.Y)
		if got := drawn.Bounds().Size(); got != size {
			t.Errorf("a %v screen got a %v wallpaper", size, got)
		}
	}
}

func TestTheMarkIsInTheMiddleAndTheRestIsBackground(t *testing.T) {
	drawn := Draw(1280, 720)

	// The corners are the background: a mark that filled the screen, or one
	// drawn at the origin, would both pass a size check.
	for _, corner := range []image.Point{{X: 2, Y: 2}, {X: 1277, Y: 2}, {X: 2, Y: 717}, {X: 1277, Y: 717}} {
		red, green, blue, _ := drawn.At(corner.X, corner.Y).RGBA()
		if byte(red>>8) != background.R || byte(green>>8) != background.G || byte(blue>>8) != background.B {
			t.Errorf("the corner at %v is not the background: %d,%d,%d", corner, red>>8, green>>8, blue>>8)
		}
	}

	// And the middle is not: something was drawn there.
	red, green, blue, _ := drawn.At(640, 360).RGBA()
	if byte(red>>8) == background.R && byte(green>>8) == background.G && byte(blue>>8) == background.B {
		t.Error("the middle of the screen is bare background, so the mark was not drawn")
	}
}

func TestTheBackgroundIsNotBlack(t *testing.T) {
	// A screen showing black is indistinguishable from a screen that is off.
	if background.R == 0 && background.G == 0 && background.B == 0 {
		t.Error("the background is pure black, which looks like a screen nobody switched on")
	}
}

func TestAnAbsurdScreenDoesNotPanic(t *testing.T) {
	// A capture that went wrong, or a server that has not finished resizing,
	// must not take the daemon down.
	for _, size := range []image.Point{{X: 1, Y: 1}, {X: 8, Y: 4000}, {X: 4000, Y: 8}} {
		if drawn := Draw(size.X, size.Y); drawn.Bounds().Size() != size {
			t.Errorf("%v produced %v", size, drawn.Bounds().Size())
		}
	}
}
