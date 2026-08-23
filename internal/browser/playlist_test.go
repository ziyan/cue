package browser

import (
	"context"
	"testing"
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
