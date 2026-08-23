package browser

import (
	"context"
	"fmt"
	"time"
)

// ProbeResponsive asks the browser a trivial question and requires an answer
// within the context's deadline. It fails when the renderer is wedged, which
// is the common way a kiosk stops working: the process is running, the window
// is on the screen, and nothing in it will ever change again.
func (self *Browser) ProbeResponsive(ctx context.Context) error {
	session, err := self.currentSession(ctx)
	if err != nil {
		return err
	}
	var answer int
	if err := session.Evaluate(ctx, "1+1", false, &answer); err != nil {
		return err
	}
	if answer != 2 {
		return fmt.Errorf("browser: the page answered %d to 1+1", answer)
	}
	return nil
}

// ProbePainting requires the page to reach its next animation frame.
//
// This is the probe that catches what nothing else does. A renderer can go on
// answering JavaScript while its compositor has stopped, and the screen then
// shows a picture from hours ago with no other symptom at all. Asking for a
// promise that resolves on the next frame is a question only a page that is
// still painting can answer.
//
// A page in a background tab does not paint, by design, so this is only ever
// asked of the tab that is on the screen.
func (self *Browser) ProbePainting(ctx context.Context) error {
	session, err := self.currentSession(ctx)
	if err != nil {
		return err
	}

	// The timeout inside the page is shorter than the context's, so that a
	// page which is painting slowly reports itself rather than being cut off
	// by the protocol deadline, which reads the same as the browser being
	// gone.
	inner := time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline) - time.Second; remaining > inner {
			inner = remaining
		}
	}

	expression := fmt.Sprintf(`new Promise((resolve) => {
  const timer = setTimeout(() => resolve('no frame'), %d);
  requestAnimationFrame(() => { clearTimeout(timer); resolve('painted'); });
})`, inner.Milliseconds())

	var outcome string
	if err := session.Evaluate(ctx, expression, true, &outcome); err != nil {
		return err
	}
	if outcome != "painted" {
		return fmt.Errorf("browser: the page has not drawn a frame in %s", inner)
	}
	return nil
}

// ReloadCurrent fetches the page on the screen again. The first step of the
// watchdog's ladder.
func (self *Browser) ReloadCurrent(ctx context.Context) error {
	session, err := self.currentSession(ctx)
	if err != nil {
		return err
	}
	return session.Reload(ctx, false)
}

// RecreateCurrent closes the tab on the screen and opens it again. A tab whose
// renderer has crashed in a way a reload does not clear needs this.
func (self *Browser) RecreateCurrent(ctx context.Context) error {
	self.mutex.Lock()
	identifier := self.current
	target := self.tabs[identifier]
	self.mutex.Unlock()
	if target == "" {
		return fmt.Errorf("browser: there is no tab on the screen")
	}

	item, found := self.itemFor(identifier)
	address := self.holdingPageURL()
	if found {
		address = item.URL
	}

	session, err := self.browser(ctx)
	if err != nil {
		return err
	}

	created, err := session.CreateTarget(ctx, address)
	if err != nil {
		return err
	}
	// Close the old tab only after the new one exists, so the window is never
	// empty and the screen never goes black.
	if err := session.CloseTarget(ctx, target); err != nil {
		log.Warningf("cannot close the old tab: %s", err)
	}
	self.forgetSession(target)

	self.mutex.Lock()
	self.tabs[identifier] = created
	self.mutex.Unlock()

	return session.ActivateTarget(ctx, created)
}

// ClearCache empties the browser's HTTP cache and reloads. A corrupted cache
// is a fault that survives every restart and shows up as a page that will not
// load while everything else looks healthy.
func (self *Browser) ClearCache(ctx context.Context) error {
	session, err := self.currentSession(ctx)
	if err != nil {
		return err
	}
	if err := session.ClearCache(ctx); err != nil {
		return err
	}
	return session.Reload(ctx, true)
}

// currentSession is the connection to the tab that is on the screen.
func (self *Browser) currentSession(ctx context.Context) (*pageSession, error) {
	self.mutex.Lock()
	ready := self.ready
	target := self.tabs[self.current]
	self.mutex.Unlock()

	if !ready {
		return nil, fmt.Errorf("browser: the browser is not ready")
	}
	if target == "" {
		return nil, fmt.Errorf("browser: there is no tab on the screen")
	}

	session, err := self.session(ctx, target)
	if err != nil {
		// The tab has gone, or its connection has. Drop what is cached so
		// that the next attempt reattaches rather than failing the same way
		// forever.
		self.forgetSession(target)
		return nil, err
	}
	return session, nil
}
