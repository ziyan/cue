package display

import (
	"context"
	"image"
	"image/color"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/util/executable"
)

// startVirtualScreen brings up an X server this test owns.
//
// The wallpaper is painted on the root window, and on a real device the
// browser covers the root window completely — which is the point of a kiosk,
// and which means there is no way to photograph the wallpaper on a machine
// that is working. A screen of its own, with nothing else on it, is the only
// place the pixels can actually be checked.
func startVirtualScreen(t *testing.T, number int, width, height int) {
	t.Helper()

	path, err := executable.Resolve("Xvfb", "/usr/lib/xorg/Xvfb")
	if err != nil {
		t.Skipf("no Xvfb here; the image has one, and make docker-test runs this there: %s", err)
	}
	if where, found := SomethingIsAnsweringOn(number); found {
		t.Skipf("display :%d is taken on this machine (%s)", number, where)
	}

	server := exec.Command(path, Name(number),
		"-screen", "0", itoa(width)+"x"+itoa(height)+"x24", "-nolisten", "tcp")
	if err := server.Start(); err != nil {
		t.Skipf("cannot start Xvfb: %s", err)
	}
	t.Cleanup(func() {
		_ = server.Process.Kill()
		_, _ = server.Process.Wait()
	})

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, found := SomethingIsAnsweringOn(number); found {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Skip("Xvfb did not start in time")
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func TestTheWallpaperReachesTheScreen(t *testing.T) {
	const number, width, height = 71, 320, 200
	startVirtualScreen(t, number, width, height)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Xvfb started without -auth accepts anyone, so there is no cookie.
	connection, err := Open(ctx, number, nil)
	if err != nil {
		t.Fatalf("cannot reach the test screen: %s", err)
	}
	defer connection.Close()

	// A picture with a different colour in each corner, so that a wallpaper
	// drawn upside down, mirrored, or offset by a strip fails rather than
	// passing because everything happens to be one colour.
	wanted := image.NewRGBA(image.Rect(0, 0, width, height))
	corners := map[image.Point]color.RGBA{
		{X: 4, Y: 4}:                  {R: 200, G: 30, B: 40, A: 255},
		{X: width - 5, Y: 4}:          {R: 30, G: 200, B: 40, A: 255},
		{X: 4, Y: height - 5}:         {R: 40, G: 30, B: 200, A: 255},
		{X: width - 5, Y: height - 5}: {R: 210, G: 200, B: 30, A: 255},
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var shade color.RGBA
			if x < width/2 && y < height/2 {
				shade = corners[image.Point{X: 4, Y: 4}]
			} else if x >= width/2 && y < height/2 {
				shade = corners[image.Point{X: width - 5, Y: 4}]
			} else if x < width/2 {
				shade = corners[image.Point{X: 4, Y: height - 5}]
			} else {
				shade = corners[image.Point{X: width - 5, Y: height - 5}]
			}
			wanted.Set(x, y, shade)
		}
	}

	if err := connection.SetRootBackground(wanted); err != nil {
		t.Fatalf("the wallpaper was refused: %s", err)
	}

	got, err := connection.Capture(ctx)
	if err != nil {
		t.Fatalf("cannot read the screen back: %s", err)
	}
	if got.Bounds().Dx() != width || got.Bounds().Dy() != height {
		t.Fatalf("read back a %v screen, want %dx%d", got.Bounds().Size(), width, height)
	}

	for at, want := range corners {
		red, green, blue, _ := got.At(at.X, at.Y).RGBA()
		gotColour := color.RGBA{R: byte(red >> 8), G: byte(green >> 8), B: byte(blue >> 8), A: 255}
		if gotColour != want {
			t.Errorf("at %v the screen is %v, want %v — the wallpaper is not reaching the root window as drawn",
				at, gotColour, want)
		}
	}
}

func TestManyDisplaysCanBeOpenedAtOnce(t *testing.T) {
	// This killed a display in the field.
	//
	// randr.Init and dpms.Init register their extension in package-level maps
	// inside xgb, and those maps are not guarded. Two goroutines opening a
	// display at the same moment write the same map and Go stops the whole
	// program with "concurrent map writes" — a fatal error, not a panic, so
	// nothing recovers and the daemon dies outright. Three things here open
	// connections, and one of them opens on a timer.
	//
	// Run under -race, which CI does, this fails on the unguarded version
	// even when the timing does not happen to line up.
	const number, width, height = 72, 160, 120
	startVirtualScreen(t, number, width, height)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const openers = 8
	failures := make(chan error, openers)
	var waiting sync.WaitGroup
	for index := 0; index < openers; index++ {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			connection, err := Open(ctx, number, nil)
			if err != nil {
				failures <- err
				return
			}
			// Ask it something, so the connection is actually used rather
			// than merely made.
			if _, _, err := connection.Pointer(); err != nil {
				failures <- err
			}
			connection.Close()
		}()
	}
	waiting.Wait()
	close(failures)

	for err := range failures {
		t.Errorf("opening a display concurrently failed: %s", err)
	}
}
