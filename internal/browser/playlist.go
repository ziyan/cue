package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/cdp"
	"github.com/ziyan/cue/internal/util/deferutil"
)

// afterReady runs once each time Chromium comes up. It opens the tabs and
// starts the two loops that keep the screen doing the right thing.
func (self *Browser) afterReady(ctx context.Context) {
	if err := self.openTabs(ctx); err != nil {
		log.Errorf("cannot set up the playlist: %s", err)
		return
	}

	self.mutex.Lock()
	self.ready = true
	self.mutex.Unlock()

	go func() {
		defer deferutil.Recover()
		self.rotate(ctx)
	}()
	go func() {
		defer deferutil.Recover()
		self.enforceRules(ctx)
	}()
}

// openTabs makes the browser's tabs match the playlist: one tab per enabled
// item, in order, with everything else closed.
//
// It runs on every browser start and whenever the configuration changes, so it
// is written as a reconciliation rather than as a sequence of steps: work out
// what should be open, compare it with what is, and change the difference.
func (self *Browser) openTabs(ctx context.Context) error {
	session, err := self.browser(ctx)
	if err != nil {
		return err
	}

	items := self.enabledItems()
	if len(items) == 0 {
		// A device that has been set up but has nothing to show still needs
		// something on the screen, and a black rectangle tells whoever is
		// standing in front of it nothing at all. The daemon's own holding
		// page says where to point a browser to configure it.
		items = []config.Item{{Identifier: holdingIdentifier, URL: self.holdingPageURL()}}
	}

	existing, err := self.client.Pages(ctx)
	if err != nil {
		return err
	}

	// Chromium always has one tab open at start-up. Reuse it for the first
	// item rather than opening a second and closing the first, which makes
	// the screen flash.
	reusable := make([]cdp.Target, 0, len(existing))
	reusable = append(reusable, existing...)

	tabs := map[string]string{}
	for index, item := range items {
		var identifier string
		if index < len(reusable) {
			identifier = reusable[index].Identifier
			if err := self.navigateTab(ctx, identifier, item.URL); err != nil {
				log.Warningf("cannot point a tab at %s: %s", item.URL, err)
				continue
			}
		} else {
			identifier, err = session.CreateTarget(ctx, item.URL)
			if err != nil {
				log.Warningf("cannot open a tab for %s: %s", item.URL, err)
				continue
			}
		}
		tabs[item.Identifier] = identifier
	}

	// Anything left over is a tab from a playlist that has since been
	// shortened, or a window a page opened for itself.
	for index := len(items); index < len(reusable); index++ {
		if err := session.CloseTarget(ctx, reusable[index].Identifier); err != nil {
			log.Debugf("cannot close a spare tab: %s", err)
		}
	}

	self.mutex.Lock()
	self.tabs = tabs
	self.mutex.Unlock()

	if len(items) > 0 {
		if err := self.show(ctx, items[0].Identifier); err != nil {
			return err
		}
	}
	log.Noticef("showing %d page(s)", len(tabs))
	return nil
}

// rotate moves to the next item when the current one's time is up. The wait is
// recomputed each time round rather than driven by a fixed ticker, because an
// item can set its own duration and the configuration can change underneath.
func (self *Browser) rotate(ctx context.Context) {
	for {
		wait := self.currentDuration()
		if wait <= 0 {
			// A single page, or a playlist with rotation switched off. Wake
			// occasionally anyway so that a change to the configuration takes
			// effect without a restart.
			wait = 5 * time.Second
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
				continue
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		if err := self.showNext(ctx); err != nil {
			log.Warningf("cannot move to the next page: %s", err)
		}
	}
}

// currentDuration is how long the item now on screen should stay there.
func (self *Browser) currentDuration() time.Duration {
	items := self.enabledItems()
	if len(items) < 2 {
		return 0
	}

	self.mutex.Lock()
	current := self.current
	since := self.currentSince
	self.mutex.Unlock()

	duration := self.configuration.Playlist.Interval.Duration()
	for _, item := range items {
		if item.Identifier == current && item.Duration > 0 {
			duration = item.Duration.Duration()
			break
		}
	}
	if duration <= 0 {
		return 0
	}

	remaining := duration - time.Since(since)
	if remaining < 0 {
		remaining = 0
	}
	return remaining
}

// showNext moves to the item after the one on screen, wrapping round.
func (self *Browser) showNext(ctx context.Context) error {
	items := self.enabledItems()
	if len(items) == 0 {
		return nil
	}

	self.mutex.Lock()
	current := self.current
	self.mutex.Unlock()

	next := 0
	for index, item := range items {
		if item.Identifier == current {
			next = (index + 1) % len(items)
			break
		}
	}
	return self.show(ctx, items[next].Identifier)
}

// Show puts one playlist item on the screen immediately, which is what the
// web interface's "show this now" does.
func (self *Browser) Show(ctx context.Context, identifier string) error {
	return self.show(ctx, identifier)
}

func (self *Browser) show(ctx context.Context, identifier string) error {
	self.mutex.Lock()
	target, found := self.tabs[identifier]
	self.mutex.Unlock()
	if !found {
		return fmt.Errorf("browser: nothing is showing the item %q", identifier)
	}

	session, err := self.browser(ctx)
	if err != nil {
		return err
	}
	if err := session.ActivateTarget(ctx, target); err != nil {
		return err
	}

	self.mutex.Lock()
	self.current = identifier
	self.currentSince = time.Now()
	self.mutex.Unlock()

	item, found := self.itemFor(identifier)
	if found && item.Reload {
		// Some dashboards stop refreshing themselves after a few hours and
		// show numbers from this morning. Reloading on the way in is the
		// blunt fix, and it is opt-in because a page that is expensive to
		// load should not be reloaded every thirty seconds.
		if tab, err := self.session(ctx, target); err == nil {
			if err := tab.Reload(ctx, false); err != nil {
				log.Debugf("cannot reload %s: %s", item.URL, err)
			}
		}
	}

	log.Debugf("showing %s", describeItem(item, identifier))
	return nil
}

// Navigate points the tab that is currently on screen at a different address,
// without changing the playlist. Used by the web interface for a quick look at
// something.
func (self *Browser) Navigate(ctx context.Context, address string) error {
	self.mutex.Lock()
	target, found := self.tabs[self.current]
	self.mutex.Unlock()
	if !found {
		return fmt.Errorf("browser: there is no tab on the screen to point somewhere else")
	}
	return self.navigateTab(ctx, target, address)
}

func (self *Browser) navigateTab(ctx context.Context, target, address string) error {
	session, err := self.session(ctx, target)
	if err != nil {
		return err
	}
	return session.Navigate(ctx, address)
}

// Screenshot returns a PNG of what is on the screen.
func (self *Browser) Screenshot(ctx context.Context) ([]byte, error) {
	self.mutex.Lock()
	target, found := self.tabs[self.current]
	self.mutex.Unlock()
	if !found {
		return nil, fmt.Errorf("browser: nothing is on the screen yet")
	}
	session, err := self.session(ctx, target)
	if err != nil {
		return nil, err
	}
	return session.CaptureScreenshot(ctx)
}

// enabledItems is the playlist without the items an operator has switched off.
func (self *Browser) enabledItems() []config.Item {
	items := make([]config.Item, 0, len(self.configuration.Playlist.Items))
	for _, item := range self.configuration.Playlist.Items {
		if !item.Disabled {
			items = append(items, item)
		}
	}
	return items
}

func (self *Browser) itemFor(identifier string) (config.Item, bool) {
	for _, item := range self.configuration.Playlist.Items {
		if item.Identifier == identifier {
			return item, true
		}
	}
	return config.Item{}, false
}

func describeItem(item config.Item, identifier string) string {
	switch {
	case item.Title != "":
		return item.Title
	case item.URL != "":
		return item.URL
	default:
		return identifier
	}
}

// holdingIdentifier names the tab shown when there is no playlist at all.
const holdingIdentifier = "holding"

// holdingPageURL is the daemon's own page, served on the loopback interface,
// which tells whoever is looking at the screen where to configure it.
func (self *Browser) holdingPageURL() string {
	port := "8080"
	if _, listenPort, err := splitPort(self.configuration.Web.Listen); err == nil && listenPort != "" {
		port = listenPort
	}
	return "http://127.0.0.1:" + port + "/welcome"
}

func splitPort(address string) (string, string, error) {
	for index := len(address) - 1; index >= 0; index-- {
		if address[index] == ':' {
			return address[:index], address[index+1:], nil
		}
	}
	return "", "", fmt.Errorf("browser: %q has no port", address)
}
