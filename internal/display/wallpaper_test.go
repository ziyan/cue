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

	// -noreset for the same reason the daemon passes it to the real server:
	// without it an X server throws everything away when its last client
	// disconnects. The readiness check below connects and disconnects, which
	// is exactly that, and the test's own connection then arrives in the
	// middle of the reset and is dropped.
	server := exec.Command(path, Name(number),
		"-screen", "0", itoa(width)+"x"+itoa(height)+"x24", "-nolisten", "tcp", "-noreset")
	if err := server.Start(); err != nil {
		t.Skipf("cannot start Xvfb: %s", err)
	}
	t.Cleanup(func() {
		_ = server.Process.Kill()
		_, _ = server.Process.Wait()
	})

	// Waiting for the socket to accept a connection is not enough: it appears
	// before the server will finish a handshake on it, and a test that starts
	// then gets a broken pipe. So the wait is a real connection, retried.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		attempt, cancel := context.WithTimeout(context.Background(), time.Second)
		connection, err := Open(attempt, number, nil)
		cancel()
		if err == nil {
			connection.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Skip("Xvfb did not become usable in time")
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

			// Retried, because eight simultaneous connects overflow the X
			// server's accept queue and the kernel resets some of them. That
			// is the server's backlog, not the thing under test: what is
			// being tested is what happens *after* the connection is made,
			// when each goroutine registers the extensions in maps they all
			// share.
			var connection *Display
			var err error
			for attempt := 0; attempt < 5; attempt++ {
				connection, err = Open(ctx, number, nil)
				if err == nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
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

func TestThePointerCanBeHiddenAndShown(t *testing.T) {
	// The first version of this put an empty cursor on the root window, which
	// looked right and did nothing: a cursor is a per-window attribute, the
	// browser sets its own, and the browser's window covers the screen — so
	// the root window's cursor is the one nobody ever sees. XFIXES hides the
	// cursor for the whole screen whatever it is over, and this checks the
	// server actually accepts the calls rather than that a field was set.
	const number, width, height = 73, 200, 150
	startVirtualScreen(t, number, width, height)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	connection, err := Open(ctx, number, nil)
	if err != nil {
		t.Fatalf("cannot reach the test screen: %s", err)
	}
	defer connection.Close()

	if err := connection.HidePointer(); err != nil {
		t.Fatalf("hiding the pointer failed: %s", err)
	}
	if err := connection.ShowPointer(); err != nil {
		t.Fatalf("showing the pointer failed: %s", err)
	}

	// Hides and shows are counted by the server: two hides would need two
	// shows, and a loop that asked twice would leave the pointer invisible
	// for ever. Asking twice must be harmless.
	for i := 0; i < 3; i++ {
		if err := connection.HidePointer(); err != nil {
			t.Fatalf("hiding again failed: %s", err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := connection.ShowPointer(); err != nil {
			t.Fatalf("showing again failed: %s", err)
		}
	}
	if connection.pointerHidden {
		t.Error("the pointer is still recorded as hidden after being shown")
	}
}
