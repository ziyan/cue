package browser

import (
	"context"

	"github.com/ziyan/cue/internal/util/cdp"
)

// pageSession is a connection to one tab, with the handful of page-level
// operations this project needs hung off it. It exists because the operations
// are written in JavaScript and belong with the policy that uses them, not in
// the protocol client.
type pageSession struct {
	*cdp.Session
}

// browser returns the connection to Chromium itself, opening one if there is
// not one already. Tabs are created, closed and switched through it.
func (self *Browser) browser(ctx context.Context) (*cdp.Session, error) {
	self.mutex.Lock()
	existing := self.browserSession
	self.mutex.Unlock()
	if existing != nil {
		return existing, nil
	}

	session, err := self.client.AttachBrowser(ctx)
	if err != nil {
		return nil, err
	}

	self.mutex.Lock()
	// Another goroutine may have opened one while this was connecting; the
	// loser closes its own rather than leaking it.
	if self.browserSession != nil {
		self.mutex.Unlock()
		session.Close()
		return self.browserSession, nil
	}
	self.browserSession = session
	self.mutex.Unlock()
	return session, nil
}

// session returns the connection to one tab, opening one if there is not one
// already. The connections are kept because the rules are evaluated every few
// seconds and reattaching each time would be most of the work.
func (self *Browser) session(ctx context.Context, target string) (*pageSession, error) {
	self.mutex.Lock()
	existing := self.sessions[target]
	self.mutex.Unlock()
	if existing != nil {
		// A session whose connection has gone is indistinguishable from a
		// working one until something is sent on it, so a failed call is what
		// invalidates the cache; see forgetSession.
		return &pageSession{Session: existing}, nil
	}

	pages, err := self.client.Pages(ctx)
	if err != nil {
		return nil, err
	}
	var found *cdp.Target
	for index := range pages {
		if pages[index].Identifier == target {
			found = &pages[index]
			break
		}
	}
	if found == nil {
		return nil, errNoSuchTab{target: target}
	}

	session, err := self.client.Attach(ctx, *found)
	if err != nil {
		return nil, err
	}

	self.mutex.Lock()
	if previous := self.sessions[target]; previous != nil {
		self.mutex.Unlock()
		session.Close()
		return &pageSession{Session: previous}, nil
	}
	self.sessions[target] = session
	self.mutex.Unlock()
	return &pageSession{Session: session}, nil
}

// forgetSession drops a tab's connection so that the next use reattaches.
func (self *Browser) forgetSession(target string) {
	self.mutex.Lock()
	session := self.sessions[target]
	delete(self.sessions, target)
	self.mutex.Unlock()
	if session != nil {
		session.Close()
	}
}

// forgetSessions drops every connection, which is what has to happen when the
// browser is restarted: the target identifiers and the connections behind them
// all belong to a process that no longer exists.
func (self *Browser) forgetSessions() {
	self.mutex.Lock()
	sessions := self.sessions
	browserSession := self.browserSession
	self.sessions = map[string]*cdp.Session{}
	self.browserSession = nil
	self.tabs = map[string]string{}
	self.current = ""
	self.ready = false
	self.mutex.Unlock()

	for _, session := range sessions {
		session.Close()
	}
	if browserSession != nil {
		browserSession.Close()
	}
}

// errNoSuchTab is returned when a tab the daemon was tracking has gone, which
// happens when a page closes its own window or the browser restarted a
// renderer.
type errNoSuchTab struct {
	target string
}

func (self errNoSuchTab) Error() string {
	return "browser: the tab " + self.target + " is not open any more"
}
