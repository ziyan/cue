package browser

import (
	"context"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
)

func TestNothingIsClosedWhenTheDaemonDoesNotKnowWhichTabsAreItsOwn(t *testing.T) {
	// self.tabs is empty until openTabs has run and finished. With it empty,
	// every tab looks unexpected — including the full-screen window on the
	// wall, and closing the last window takes the browser with it. This is
	// the one case where doing nothing is the only safe thing.
	browser := newTestBrowser(t, nil)

	browser.mutex.Lock()
	browser.tabs = nil
	browser.unexpectedTabs = map[string]bool{"anything": true}
	browser.mutex.Unlock()

	// It must return before it asks the browser anything at all: there is no
	// browser here, so a call that got that far would report an error, and
	// getting no further than the guard is the property being checked.
	browser.closeUnexpectedTabs(context.Background())

	browser.mutex.Lock()
	defer browser.mutex.Unlock()
	if len(browser.unexpectedTabs) != 1 {
		t.Error("the guard did not return early: it went on and rewrote what it had seen")
	}
}

func TestAskingForTheBrowserBeforeItStartedIsAnErrorAndNotAPanic(t *testing.T) {
	// The client does not exist until the browser has started and said which
	// port it is on. Everything that drives the browser runs on a timer and
	// can arrive before that — or after a browser has gone — and a panic
	// there is recovered by whichever goroutine caught it, which means a
	// rotation loop that quietly stops rotating.
	browser := newTestBrowser(t, nil)

	if _, err := browser.browser(context.Background()); err == nil {
		t.Fatal("attaching to a browser that has not started returned no error")
	}
}

// While a device is being set up over the air, its screen must show the page
// carrying the code to scan -- even if it has a playlist.
//
// A device in that state has no network, so whatever the playlist points at
// would not load anyway; and the setup network's passphrase exists only on
// that page, so a screen showing anything else is a device nobody can set up.
func TestTheScreenIsGivenToSetupEvenWhenThereIsAPlaylist(t *testing.T) {
	browser := newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Playlist.Items = []config.Item{
			{Identifier: "dashboard", URL: "http://dashboard.example.com/"},
			{Identifier: "second", URL: "http://second.example.com/"},
		}
	})

	if got := browser.plannedItems(); len(got) != 2 {
		t.Fatalf("without setup running the screen shows %d page(s), want the 2 in the playlist", len(got))
	}

	browser.SetupInProgress = func() bool { return true }

	planned := browser.plannedItems()
	if len(planned) != 1 || planned[0].Identifier != holdingIdentifier {
		t.Fatalf("while being set up the screen shows %+v, want only the holding page", planned)
	}
}

// A video item is shown by pointing the browser at the daemon's own player
// page, which is what knows how to fill a screen with one video.
func TestAVideoItemIsShownThroughThePlayerPage(t *testing.T) {
	browser := newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Web.Listen = "0.0.0.0:9090"
		configuration.Playlist.Items = []config.Item{
			{Identifier: "promo", Media: &config.ItemMedia{File: "0123456789abcdef0123456789abcdef"}},
			{Identifier: "dashboard", URL: "http://dashboard.example.com/"},
		}
	})

	planned := browser.plannedItems()
	if len(planned) != 2 {
		t.Fatalf("the screen shows %d item(s), want 2", len(planned))
	}
	if want := "http://127.0.0.1:9090/play/promo"; planned[0].URL != want {
		t.Errorf("the video item points at %q, want %q", planned[0].URL, want)
	}
	if planned[1].URL != "http://dashboard.example.com/" {
		t.Errorf("the page item was rewritten to %q", planned[1].URL)
	}
}

// A video stays on screen until it ends, and the page playing it says when.
// Rotating it away on the ordinary interval would cut a long video off part
// way and leave a short one frozen on its last frame.
func TestAVideoIsNotRotatedAwayOnTheClock(t *testing.T) {
	browser := newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Playlist.Interval = config.Duration(30 * time.Second)
		configuration.Playlist.Items = []config.Item{
			{Identifier: "promo", Media: &config.ItemMedia{File: "0123456789abcdef0123456789abcdef"}},
			{Identifier: "dashboard", URL: "http://dashboard.example.com/"},
		}
	})

	browser.mutex.Lock()
	browser.current = "promo"
	browser.currentSince = time.Now()
	browser.mutex.Unlock()
	if got := browser.currentDuration(); got != 0 {
		t.Errorf("a video item is given %s on screen; it should stay until it ends", got)
	}

	browser.mutex.Lock()
	browser.current = "dashboard"
	browser.currentSince = time.Now()
	browser.mutex.Unlock()
	if got := browser.currentDuration(); got <= 0 {
		t.Errorf("a page item is given %s on screen, so the playlist would never rotate", got)
	}
}

// An operator who sets a time on a video item means it: it wins over waiting
// for the end, which is how somebody clips a long video short.
func TestAVideoWithATimeSetKeepsThatTime(t *testing.T) {
	browser := newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Playlist.Items = []config.Item{
			{Identifier: "promo", Duration: config.Duration(10 * time.Second),
				Media: &config.ItemMedia{File: "0123456789abcdef0123456789abcdef"}},
			{Identifier: "dashboard", URL: "http://dashboard.example.com/"},
		}
	})

	browser.mutex.Lock()
	browser.current = "promo"
	browser.currentSince = time.Now()
	browser.mutex.Unlock()
	if got := browser.currentDuration(); got <= 0 || got > 10*time.Second {
		t.Errorf("a video given ten seconds is given %s", got)
	}
}

// A picture has no end of its own, so it rotates on the ordinary clock like
// every other item that is not a video. Waiting for an end that never comes
// would hold the screen for ever.
func TestAPictureRotatesOnTheOrdinaryClock(t *testing.T) {
	browser := newTestBrowser(t, func(configuration *config.Configuration) {
		configuration.Playlist.Interval = config.Duration(30 * time.Second)
		configuration.Playlist.Items = []config.Item{
			{Identifier: "poster", Media: &config.ItemMedia{
				File: "0123456789abcdef0123456789abcdef", Kind: "picture"}},
			{Identifier: "dashboard", URL: "http://dashboard.example.com/"},
		}
	})

	browser.mutex.Lock()
	browser.current = "poster"
	browser.currentSince = time.Now()
	browser.mutex.Unlock()

	if got := browser.currentDuration(); got <= 0 {
		t.Errorf("a picture is given %s on screen, so it would never move on", got)
	}
}

// The menu is opened by the control on whatever page the screen is showing,
// so it is not one of the tabs this daemon opened and the sweep would close
// it -- taking the menu away from somebody in the middle of using it.
func TestTheOnScreenMenuIsNotSweptUpAsAStrayWindow(t *testing.T) {
	browser := newTestBrowser(t, nil)
	browser.OwnMenu = "http://127.0.0.1:8080/menu"

	for _, one := range []struct {
		address string
		ours    bool
	}{
		{"http://127.0.0.1:8080/menu", true},
		{"http://127.0.0.1:8080/menu?from=screen", true},
		{"https://example.com/", false},
		{"http://127.0.0.1:8080/", false},
		{"http://127.0.0.1:9999/menu", false},
	} {
		if got := browser.isOwnMenu(one.address); got != one.ours {
			t.Errorf("isOwnMenu(%q) = %v, want %v", one.address, got, one.ours)
		}
	}
}

// A daemon that has not been told its own address must not decide that every
// window is the menu. strings.HasPrefix of an empty prefix is true of
// everything, so without the guard this sweep would close nothing at all --
// and the tab it exists to close is a login popup that keeps a dashboard off
// the screen.
func TestAnUnsetMenuAddressDoesNotMatchEveryWindow(t *testing.T) {
	browser := newTestBrowser(t, nil)
	browser.OwnMenu = ""

	for _, address := range []string{"https://example.com/", "http://127.0.0.1:8080/menu", ""} {
		if browser.isOwnMenu(address) {
			t.Errorf("with no menu address set, %q was taken for the menu", address)
		}
	}
}

// A hold stops the screen changing, and every hold is asked for by a page --
// so every hold depends on that page living long enough to give it back.
//
// It did not. The menu released the playlist and closed its own tab in the
// same breath, which cancelled the request still in flight: the hold was never
// returned and the display stopped rotating until somebody restarted the
// daemon. A wall display frozen until somebody walks to it is the worst thing
// this program can do, and it must not be one lost request away.
func TestAHoldNobodyRenewsIsLetGo(t *testing.T) {
	browser := newTestBrowser(t, nil)

	browser.Hold()
	if !browser.held() {
		t.Fatal("asking for a hold did not hold the playlist")
	}

	// As if whoever asked has been gone for longer than the limit.
	browser.mutex.Lock()
	browser.heldSince = time.Now().Add(-longestHold - time.Second)
	browser.mutex.Unlock()

	if browser.held() {
		t.Error("a hold nobody renewed still holds the screen, so it stays frozen")
	}
	browser.mutex.Lock()
	remaining := browser.holds
	browser.mutex.Unlock()
	if remaining != 0 {
		t.Errorf("the abandoned hold is still counted: %d", remaining)
	}
}

// And a hold that is being renewed must go on holding, or the menu would be
// rotated out from under somebody reading it -- which is what holds are for.
func TestARenewedHoldKeepsHolding(t *testing.T) {
	browser := newTestBrowser(t, nil)

	browser.Hold()
	browser.mutex.Lock()
	browser.heldSince = time.Now().Add(-longestHold + 5*time.Second)
	browser.mutex.Unlock()

	browser.KeepHolding()
	if !browser.held() {
		t.Error("renewing the hold did not keep the screen still")
	}
}

// Renewing something nobody is holding must not start holding it.
func TestRenewingNothingHoldsNothing(t *testing.T) {
	browser := newTestBrowser(t, nil)
	browser.KeepHolding()
	if browser.held() {
		t.Error("renewing a hold that was never asked for holds the screen")
	}
}
