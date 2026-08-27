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
	onEveryPage := self.OnEveryPage
	self.mutex.Unlock()

	// The control that puts this device back into setup has to be on whatever
	// is on the screen, including pages this daemon did not write.
	if onEveryPage != "" {
		self.PutOnEveryPage(ctx, onEveryPage)
	}

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

	items := self.plannedItems()

	existing, err := self.client.Pages(ctx)
	if err != nil {
		return err
	}

	// Chromium always has one tab open at start-up, and in kiosk mode that
	// one is the full-screen window on the wall. Reusing it is not an
	// optimisation: a tab created instead of reused gets a window of its own,
	// which is not full screen and not in front, so the screen would go on
	// showing the browser's start page while the daemon drove something
	// nobody can see. The readiness check waits for this tab to exist for
	// exactly that reason; see probe.
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

	self.fillTheScreen(ctx, session, tabs)

	log.Noticef("showing %d page(s)", len(tabs))
	return nil
}

// fillTheScreen makes the browser's window the size of the screen.
//
// It has to be done here, over the protocol, and not with a command line flag.
// --kiosk and --start-fullscreen both work by asking the window manager to
// make the window full screen, and this image has no window manager: the flags
// are accepted, nothing happens, and Chromium keeps the 800x600 window it
// opened with. Everything then looks healthy — the browser is running, the
// page has loaded, its own screenshot is of the page — while the screen on the
// wall shows a corner of it, or whatever was painted there first.
//
// That is exactly what happened on the first real device this was put on, and
// nothing but a photograph of the screen would have shown it.
func (self *Browser) fillTheScreen(ctx context.Context, session *cdp.Session, tabs map[string]string) {
	self.mutex.Lock()
	width, height := self.screenWidth, self.screenHeight
	self.mutex.Unlock()

	if width <= 0 || height <= 0 {
		return
	}

	windows := map[int]bool{}
	for _, target := range tabs {
		window, bounds, err := session.WindowForTarget(ctx, target)
		if err != nil {
			log.Debugf("cannot find the window of a tab: %s", err)
			continue
		}
		if windows[window] {
			continue
		}
		windows[window] = true

		// Full screen is by definition the size of the screen, so there is
		// nothing to compare and nothing to do. Comparing sizes as well is
		// what made this flap: a full-screen window reports itself one pixel
		// short of the screen, so it was resized, which took it out of full
		// screen, so it was resized again, for ever.
		if bounds.WindowState == "fullscreen" {
			continue
		}

		// Two steps, and both are needed. Setting a size takes the window out
		// of full screen — which is how the address bar and the tab strip
		// appeared on a wall — so the size is set first and full screen is
		// asked for again afterwards. The protocol will not accept both in
		// one call: a state and a rectangle are mutually exclusive.
		wanted := cdp.WindowBounds{Left: 0, Top: 0, Width: width, Height: height}
		if err := session.SetWindowBounds(ctx, window, wanted); err != nil {
			log.Warningf("cannot make the browser window fill the screen: %s", err)
			continue
		}
		if err := session.SetWindowBounds(ctx, window, cdp.WindowBounds{WindowState: "fullscreen"}); err != nil {
			log.Warningf("cannot put the browser window back into full screen: %s", err)
		}
		log.Noticef("the browser window was %dx%d and is now %dx%d and full screen",
			bounds.Width, bounds.Height, width, height)
	}
}

// rotate moves to the next item when the current one's time is up. The wait is
// recomputed each time round rather than driven by a fixed ticker, because an
// item can set its own duration and the configuration can change underneath.
func (self *Browser) rotate(ctx context.Context) {
	for {
		if self.held() {
			// Somebody is looking at the menu. Come back and ask again rather
			// than working out a deadline that will be wrong by then.
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}

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
				self.closeUnexpectedTabs(ctx)
				continue
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		self.closeUnexpectedTabs(ctx)

		if err := self.showNext(ctx); err != nil {
			log.Warningf("cannot move to the next page: %s", err)
		}
	}
}

// Hold stops the playlist rotating, and Release starts it again.
//
// It is held while somebody at the screen has the menu open. Rotating out from
// under them would move the page the menu is drawn over, and a menu that
// disappears while somebody is reading it is worse than no menu.
//
// A count rather than a flag, because two things could hold it at once and the
// second releasing must not start the screen moving under the first.
func (self *Browser) Hold() {
	self.mutex.Lock()
	self.holds++
	self.mutex.Unlock()
}

// Release gives back one hold.
func (self *Browser) Release() {
	self.mutex.Lock()
	if self.holds > 0 {
		self.holds--
	}
	// The item has been on screen for however long the menu was open, and
	// moving on the instant it closes would be startling. The clock starts
	// again from now.
	self.currentSince = time.Now()
	self.mutex.Unlock()
}

// held reports whether the playlist is being kept still.
func (self *Browser) held() bool {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.holds > 0
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
		if item.Identifier != current {
			continue
		}
		if item.Media != nil && item.Media.Kind != "picture" && item.Duration <= 0 {
			// A video stays until it ends, and the page playing it says when
			// that is. Rotating it away on the ordinary interval would cut a
			// long video off part way and leave a short one frozen on its last
			// frame for the rest of the interval.
			//
			// A picture has no end of its own, so it rotates on the ordinary
			// clock like everything else that is not a video.
			return 0
		}
		if item.Duration > 0 {
			duration = item.Duration.Duration()
		}
		break
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

// closeUnexpectedTabs gets rid of windows the daemon did not open.
//
// A page that calls window.open gets a window of its own, and with no window
// manager it is stacked in front of the one on the wall. Spare tabs are dealt
// with when the playlist is applied, but that happens on a restart or a
// configuration change; a screen showing one page — which is the ordinary case
// — would otherwise carry on showing whatever popped up until somebody noticed
// and walked over.
//
// A window has to have been there for two consecutive checks before it is
// closed. A page that opens a window and closes it again is doing something
// legitimate often enough — a print dialogue, an authentication step that
// hands back to the opener — that killing it the instant it appears would
// break the page. Anything that is still there ten seconds later is not
// transient. Nothing is ever closed silently: what it was is logged, because
// the failure mode of this function is a dashboard whose popup login window
// keeps vanishing and no explanation anywhere.
func (self *Browser) closeUnexpectedTabs(ctx context.Context) {
	if !self.configuration.Browser.CloseUnexpectedTabs {
		return
	}

	self.mutex.Lock()
	ours := make(map[string]bool, len(self.tabs))
	for _, identifier := range self.tabs {
		ours[identifier] = true
	}
	seenBefore := self.unexpectedTabs
	client := self.client
	self.mutex.Unlock()

	// First, before anything is asked of the browser. If the daemon does not
	// know which tabs are its own then every tab is unexpected, and this would
	// close the window on the wall — and with the last window gone, the
	// browser with it. That is the state whenever openTabs has not run or did
	// not finish, which is exactly when this must do nothing at all.
	if len(ours) == 0 || client == nil {
		return
	}

	session, err := self.browser(ctx)
	if err != nil {
		return
	}
	pages, err := client.Pages(ctx)
	if err != nil {
		return
	}

	seenNow := make(map[string]bool)
	for _, page := range pages {
		if ours[page.Identifier] {
			continue
		}
		seenNow[page.Identifier] = true
		if !seenBefore[page.Identifier] {
			// First sighting. Give it one cycle to close itself.
			continue
		}
		log.Noticef("closing a window this daemon did not open: %s", describeTarget(page))
		if err := session.CloseTarget(ctx, page.Identifier); err != nil {
			log.Warningf("cannot close it: %s", err)
		}
	}

	self.mutex.Lock()
	self.unexpectedTabs = seenNow
	self.mutex.Unlock()
}

// describeTarget names a window in a way that is useful in a log line without
// being a page's whole URL, which can be several kilobytes of query string.
func describeTarget(target cdp.Target) string {
	address := target.URL
	const maximum = 200
	if len(address) > maximum {
		address = address[:maximum] + "…"
	}
	if target.Title != "" {
		return fmt.Sprintf("%q at %s", target.Title, address)
	}
	return address
}

// settingUp reports whether this device is being set up over the air, and so
// needs its screen for the code somebody has to scan.
//
// It is a function rather than a field because the answer changes while the
// daemon runs: setup starts when a device finds itself with no network and
// ends when it has one.
func (self *Browser) settingUp() bool {
	if self.SetupInProgress == nil {
		return false
	}
	return self.SetupInProgress()
}

// Refresh makes the tabs match the playlist again, now.
//
// The tabs are normally worked out when the browser starts and when the
// configuration changes, which covers everything the playlist depends on --
// except one thing. Setting a device up over the air takes the screen for the
// page carrying the code to scan, and that starts and stops on its own, with
// no configuration change to notice. Without this, a device that decided to
// offer itself for setup would go on showing its old playlist and nobody could
// set it up.
func (self *Browser) Refresh(ctx context.Context) {
	self.mutex.Lock()
	ready := self.ready
	self.mutex.Unlock()

	if !ready {
		// The browser is not up yet, and afterReady will open the tabs with
		// the current answer when it is.
		return
	}
	if err := self.openTabs(ctx); err != nil {
		log.Warningf("cannot bring the screen up to date: %s", err)
	}
}

// plannedItems is what should be on the screen: the playlist, or the daemon's
// own holding page when there is no playlist or when the device is being set
// up over the air.
//
// It is separate from openTabs so that the decision can be checked without a
// browser to drive.
func (self *Browser) plannedItems() []config.Item {
	items := self.enabledItems()
	if len(items) > 0 && !self.settingUp() {
		// A video item is shown by pointing the browser at the daemon's own
		// player page, which is what knows how to fill the screen with one
		// video and say when it has ended.
		for index := range items {
			if items[index].Media != nil {
				items[index].URL = self.playerURL(items[index].Identifier)
			}
		}
		return items
	}

	// A device that has been set up but has nothing to show still needs
	// something on the screen, and a black rectangle tells whoever is standing
	// in front of it nothing at all. The daemon's own holding page says where
	// to point a browser to configure it.
	//
	// It also takes the screen while the device is being set up over the air,
	// playlist or no playlist. The code somebody has to scan is on that page,
	// and that code is the only place the setup network's passphrase exists --
	// a screen showing a dashboard instead is a device nobody can set up. A
	// device in that state has no network, so whatever the playlist points at
	// would not load anyway.
	return []config.Item{{Identifier: holdingIdentifier, URL: self.holdingPageURL()}}
}

// PutOnEveryPage arranges for a script to run in every tab, now and in every
// page they visit afterwards.
//
// It is how the control that puts this device back into setup reaches pages
// this daemon did not write. Its own pages carry it already; a dashboard
// somebody else runs has to be given it.
//
// Both halves are needed. addScriptToEvaluateOnNewDocument covers every page
// loaded from now on, including one that reloads itself hours later, and does
// nothing for the page already on screen; evaluating it directly covers that
// one and nothing else.
func (self *Browser) PutOnEveryPage(ctx context.Context, script string) {
	session, err := self.browser(ctx)
	if err != nil {
		log.Debugf("cannot reach the browser to add the on-screen control: %s", err)
		return
	}

	pages, err := self.client.Pages(ctx)
	if err != nil {
		log.Debugf("cannot list the tabs to add the on-screen control: %s", err)
		return
	}

	for _, page := range pages {
		tab, err := self.client.Attach(ctx, page)
		if err != nil {
			continue
		}
		if err := tab.Call(ctx, "Page.addScriptToEvaluateOnNewDocument",
			map[string]interface{}{"source": script}, nil); err != nil {
			log.Debugf("cannot arm the on-screen control for %s: %s", page.URL, err)
		}
		if err := tab.Call(ctx, "Runtime.evaluate", map[string]interface{}{
			"expression": script, "returnByValue": true,
		}, nil); err != nil {
			log.Debugf("cannot add the on-screen control to %s: %s", page.URL, err)
		}
		tab.Close()
	}
	_ = session
}

// ShowNext moves to the item after the one on screen.
//
// It exists for a video: a video item stays on screen for exactly as long as
// its video, and only the page playing it knows when that is. Nothing else in
// the daemon knows how long a video runs, and finding out would mean reading
// the file's headers -- which would still be a guess about a file somebody may
// replace tomorrow.
func (self *Browser) ShowNext(ctx context.Context) error {
	return self.showNext(ctx)
}

// playerURL is the daemon's own page for playing one video item.
func (self *Browser) playerURL(identifier string) string {
	port := "8080"
	if _, listenPort, err := splitPort(self.configuration.Web.Listen); err == nil && listenPort != "" {
		port = listenPort
	}
	return "http://127.0.0.1:" + port + "/play/" + identifier
}
