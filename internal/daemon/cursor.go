package daemon

import (
	"context"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/display"
	"github.com/ziyan/cue/internal/util/deferutil"
)

// cursorPollInterval is how often the pointer is asked where it is.
//
// Fast enough that the pointer appears under somebody's hand rather than after
// it, and slow enough to be nothing: this is one round trip to a local X
// server, several times a second, next to a browser rendering video.
const cursorPollInterval = 100 * time.Millisecond

// watchPointer shows the mouse pointer while it is moving and hides it again
// once it stops.
//
// A screen on a wall should not have an arrow parked in the middle of it, and
// a screen with a mouse or a touchscreen is impossible to use without one —
// there is no way to see where you are. The X server cannot be told to do this
// (-nocursor is all or nothing, decided at start-up), so the daemon does it:
// it watches where the pointer is and puts an empty cursor on the root window
// when nothing has moved for a while.
//
// It polls rather than subscribing to motion, because subscribing means
// selecting PointerMotion on the root window, and an X client that does that
// takes the events — a second client and a browser that stops seeing the
// mouse is a much worse bug than a poll every tenth of a second.
func (self *Daemon) watchPointer(ctx context.Context) {
	defer deferutil.Recover()

	var connection *display.Display
	defer func() {
		if connection != nil {
			connection.Close()
		}
	}()

	var lastX, lastY int
	var movedAt time.Time
	var showing bool
	first := true

	ticker := time.NewTicker(cursorPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		configuration := self.store.Current()
		if configuration.Display.Cursor != config.CursorAuto {
			// Somebody has changed their mind. Anything already hidden is put
			// back, so that turning this off does not leave the pointer
			// invisible until the next restart.
			if connection != nil {
				if configuration.Display.Cursor == config.CursorAlways && !showing {
					_ = connection.ShowPointer()
					showing = true
				}
				connection.Close()
				connection = nil
			}
			continue
		}

		if connection == nil {
			openContext, cancel := context.WithTimeout(ctx, 5*time.Second)
			opened, err := display.Open(openContext, configuration.Display.Number, self.xserver.Cookie())
			cancel()
			if err != nil {
				// The X server is restarting, or has not started. Nothing to
				// say: the supervisor is already saying it.
				continue
			}
			connection = opened
			first = true
		}

		x, y, err := connection.Pointer()
		if err != nil {
			connection.Close()
			connection = nil
			continue
		}

		if first {
			// The pointer has not moved yet as far as this is concerned, so
			// the screen starts clean.
			lastX, lastY, first = x, y, false
			if err := connection.HidePointer(); err != nil {
				connection.Close()
				connection = nil
				continue
			}
			showing = false
			continue
		}

		if x != lastX || y != lastY {
			lastX, lastY = x, y
			movedAt = time.Now()
			if !showing {
				if err := connection.ShowPointer(); err == nil {
					showing = true
				}
			}
			continue
		}

		idle := configuration.Display.CursorIdleTimeout.Duration()
		if idle <= 0 {
			idle = 3 * time.Second
		}
		if showing && time.Since(movedAt) >= idle {
			if err := connection.HidePointer(); err == nil {
				showing = false
			}
		}
	}
}
