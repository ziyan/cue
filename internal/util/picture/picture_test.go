package picture

import (
	"image"
	"image/color"
	"testing"
)

func solid(width, height int, shade color.RGBA) *image.RGBA {
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.Set(x, y, shade)
		}
	}
	return picture
}

func TestShrinkKeepsTheShape(t *testing.T) {
	// A 4K screen down to the width the card uses, keeping its proportions:
	// a picture that arrives the wrong shape is worse than a large one.
	small := Shrink(solid(2560, 1440, color.RGBA{0, 0, 0, 255}), 960)

	if got := small.Bounds().Dx(); got != 960 {
		t.Errorf("width is %d, want 960", got)
	}
	if got, want := small.Bounds().Dy(), 540; got != want {
		t.Errorf("height is %d, want %d — 2560x1440 is 16:9", got, want)
	}
}

func TestShrinkAveragesRatherThanSamples(t *testing.T) {
	// These are photographs from cameras. Nearest-neighbour at half size is a
	// mess of aliasing, and the point of the small picture is that it still
	// shows what the screen shows.
	//
	// A one-pixel checkerboard has no correct single sample: whichever pixel
	// is picked is either black or white, and the average is grey.
	source := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			shade := color.RGBA{0, 0, 0, 255}
			if (x+y)%2 == 0 {
				shade = color.RGBA{255, 255, 255, 255}
			}
			source.Set(x, y, shade)
		}
	}

	small := Shrink(source, 8)
	red, _, _, _ := small.At(4, 4).RGBA()
	if value := red >> 8; value < 100 || value > 155 {
		t.Errorf("a checkerboard averaged to %d, want about 128 — it is being sampled, not averaged", value)
	}
}

func TestAPictureAlreadySmallEnoughIsUntouched(t *testing.T) {
	source := solid(320, 240, color.RGBA{1, 2, 3, 255})
	if got := Shrink(source, 960); got != image.Image(source) {
		t.Error("a picture smaller than the target was copied rather than returned as it was")
	}
}

func TestShrinkDoesNotDivideByZero(t *testing.T) {
	// A screen one pixel tall is not a real screen, but a capture that failed
	// half way could produce one, and this must not take the daemon down.
	for _, size := range []image.Point{{X: 1, Y: 1}, {X: 4000, Y: 1}, {X: 1, Y: 4000}} {
		small := Shrink(solid(size.X, size.Y, color.RGBA{0, 0, 0, 255}), 960)
		if small.Bounds().Dx() < 1 || small.Bounds().Dy() < 1 {
			t.Errorf("%v shrank to %v", size, small.Bounds())
		}
	}
}
