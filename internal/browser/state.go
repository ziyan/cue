package browser

import (
	"context"
	"time"

	"github.com/ziyan/cue/internal/config"
)

// State is what the web interface is told about the
// browser.
type State struct {
	Ready bool `json:"ready"`

	// Current is the playlist item on the screen.
	Current      string    `json:"current"`
	CurrentTitle string    `json:"currentTitle"`
	CurrentURL   string    `json:"currentUrl"`
	CurrentSince time.Time `json:"currentSince"`

	Tabs []TabState `json:"tabs"`
}

// TabState is one open page.
type TabState struct {
	Item      string `json:"item"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Active    bool   `json:"active"`
	Logins    int    `json:"logins"`
	Dismissed int    `json:"dismissed"`
}

// State reports what the browser is showing. It asks the browser rather than
// reporting what it last told it to show, because the difference between the
// two is precisely the interesting case: a page that has redirected itself to
// a login screen still has the tab the daemon opened.
func (self *Browser) State(ctx context.Context) State {
	self.mutex.Lock()
	state := State{
		Ready:        self.ready,
		Current:      self.current,
		CurrentSince: self.currentSince,
	}
	tabs := make(map[string]string, len(self.tabs))
	for identifier, target := range self.tabs {
		tabs[identifier] = target
	}
	logins := make(map[string]int, len(self.loginCount))
	for identifier, count := range self.loginCount {
		logins[identifier] = count
	}
	dismissed := make(map[string]int, len(self.dismissCount))
	for identifier, count := range self.dismissCount {
		dismissed[identifier] = count
	}
	self.mutex.Unlock()

	if !state.Ready {
		return state
	}

	pages, err := self.client.Pages(ctx)
	if err != nil {
		return state
	}
	byTarget := map[string]int{}
	for index, page := range pages {
		byTarget[page.Identifier] = index
	}

	for identifier, target := range tabs {
		tab := TabState{
			Item:      identifier,
			Active:    identifier == state.Current,
			Logins:    logins[identifier],
			Dismissed: dismissed[identifier],
		}
		if index, found := byTarget[target]; found {
			tab.Title = pages[index].Title
			tab.URL = pages[index].URL
		}
		if tab.Active {
			state.CurrentTitle = tab.Title
			state.CurrentURL = tab.URL
		}
		state.Tabs = append(state.Tabs, tab)
	}

	return state
}

// Reconfigure applies a changed configuration. The playlist, the rules and the
// rotation all change while Chromium keeps running, because restarting it
// blanks the screen for several seconds and an operator editing a playlist
// should not see that.
//
// It reports whether the change needs the browser restarted anyway, which is
// true only for the things that are fixed at start-up: the command line.
func (self *Browser) Reconfigure(ctx context.Context, configuration *config.Configuration) (bool, error) {
	self.mutex.Lock()
	previous := self.configuration
	self.configuration = configuration
	ready := self.ready
	self.mutex.Unlock()

	if restartNeeded(previous, configuration) {
		return true, nil
	}
	if !ready {
		return false, nil
	}
	return false, self.openTabs(ctx)
}

// restartNeeded reports whether a configuration change is one that can only be
// applied by starting Chromium again.
func restartNeeded(previous, updated *config.Configuration) bool {
	before, after := previous.Browser, updated.Browser
	switch {
	case before.Binary != after.Binary:
	case before.User != after.User:
	case before.Sandbox != after.Sandbox:
	case before.IgnoreCertificateErrors != after.IgnoreCertificateErrors:
	case before.EphemeralCache != after.EphemeralCache:
	case before.DarkMode != after.DarkMode:
	case before.DeviceScaleFactor != after.DeviceScaleFactor:
	case before.ForceDarkContent != after.ForceDarkContent:
	case !sameStrings(before.CertificateAuthorities, after.CertificateAuthorities):
	case !sameStrings(before.ExtraArguments, after.ExtraArguments):
	default:
		return false
	}
	return true
}

func sameStrings(before, after []string) bool {
	if len(before) != len(after) {
		return false
	}
	for index := range before {
		if before[index] != after[index] {
			return false
		}
	}
	return true
}
