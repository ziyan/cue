// Package watchdog decides whether the display has stopped working, and gets
// it going again.
//
// A frozen screen looks exactly like a working one. Nothing about the
// processes says anything is wrong: Chromium is running, the X server is
// running, the last log line was hours ago because everything is fine. So the
// daemon has to ask, on a timer, and it has to ask a question that a display
// which has stopped working cannot answer.
//
// Three questions, in order, because they fail differently and need different
// remedies:
//
//  1. Does the X server answer? If not, the thing the browser draws into is
//     the problem, and restarting the browser will not help.
//  2. Does the browser answer a trivial piece of JavaScript within the
//     deadline? If not, the renderer is wedged — the common case, and the one
//     a heartbeat sent by the page itself could also have caught.
//  3. Does the page reach its next animation frame? A renderer can answer
//     JavaScript while never painting again, and that is a screen showing a
//     picture from three hours ago. Nothing but this catches it.
//
// Failures escalate along a ladder, each step heavier than the last and each
// with its own threshold, so that a page which is merely slow gets reloaded
// rather than restarting the machine's graphics.
package watchdog

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/deferutil"
)

var log = logging.MustGetLogger("watchdog")

// Probe is one question the watchdog asks. It must return an error if it
// cannot answer within the context's deadline.
type Probe func(ctx context.Context) error

// Remedies are the steps of the recovery ladder, from cheapest to heaviest.
// Any of them may be nil, in which case that step is skipped.
type Remedies struct {
	// ReloadPage fetches the current page again.
	ReloadPage func(ctx context.Context) error

	// RecreatePage closes the tab and opens it again, which clears state a
	// reload does not.
	RecreatePage func(ctx context.Context) error

	// ClearCache empties the browser's HTTP cache and reloads. A corrupted
	// cache is a fault that survives restarts and presents as a page that
	// will not load for no visible reason.
	ClearCache func(ctx context.Context) error

	// RestartBrowser stops and starts Chromium.
	RestartBrowser func(ctx context.Context) error

	// RestartDisplay restarts the X server as well, for when the browser
	// cannot come back because the server it draws into is what is wedged.
	RestartDisplay func(ctx context.Context) error
}

// State is what the interface shows. The useful question about a display is
// not "is it up" but "how often does it have to be rescued", and this is the
// answer.
type State struct {
	Enabled bool `json:"enabled"`

	// ConsecutiveFailures is how many probes in a row have failed. Zero is
	// the healthy state.
	ConsecutiveFailures int `json:"consecutiveFailures"`

	LastProbeAt     time.Time `json:"lastProbeAt"`
	LastSuccessAt   time.Time `json:"lastSuccessAt"`
	LastFailureAt   time.Time `json:"lastFailureAt"`
	LastFailure     string    `json:"lastFailure"`
	LastRemedy      string    `json:"lastRemedy"`
	LastRemedyAt    time.Time `json:"lastRemedyAt"`
	TotalFailures   int       `json:"totalFailures"`
	RemediesApplied int       `json:"remediesApplied"`
	Suspended       bool      `json:"suspended"`
}

// Watchdog runs the probes and applies the remedies.
type Watchdog struct {
	settings *config.Watchdog
	probes   []namedProbe
	remedies Remedies

	mutex     sync.Mutex
	state     State
	suspended int

	// appliedAt records when each step of the ladder was last used, so that a
	// step is not repeated while the previous attempt is still settling.
	appliedAt map[string]time.Time
}

type namedProbe struct {
	name  string
	probe Probe
}

// New returns a watchdog. Nothing runs until Start.
func New(settings *config.Watchdog, remedies Remedies) *Watchdog {
	return &Watchdog{
		settings:  settings,
		remedies:  remedies,
		appliedAt: map[string]time.Time{},
		state:     State{Enabled: settings.Enabled},
	}
}

// AddProbe registers a question to ask. The order matters: the first one to
// fail is the one reported, so they are added most fundamental first.
func (self *Watchdog) AddProbe(name string, probe Probe) {
	self.probes = append(self.probes, namedProbe{name: name, probe: probe})
}

// Start begins asking.
func (self *Watchdog) Start(ctx context.Context) {
	if !self.settings.Enabled {
		log.Noticef("the watchdog is disabled; a frozen screen will stay frozen")
		return
	}
	go func() {
		defer deferutil.Recover()
		self.run(ctx)
	}()
}

// Suspend stops the watchdog counting failures, and returns a function that
// resumes it. Everything that deliberately stops the browser or the X server
// wraps itself in this, so that a planned restart is never mistaken for a
// fault and escalated.
func (self *Watchdog) Suspend() func() {
	self.mutex.Lock()
	self.suspended++
	self.state.Suspended = true
	self.mutex.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			self.mutex.Lock()
			self.suspended--
			if self.suspended <= 0 {
				self.suspended = 0
				self.state.Suspended = false
				// A planned restart starts the count again from zero: the
				// failures that led to it have been dealt with.
				self.state.ConsecutiveFailures = 0
			}
			self.mutex.Unlock()
		})
	}
}

// State is a snapshot for the interface.
func (self *Watchdog) State() State {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.state
}

func (self *Watchdog) run(ctx context.Context) {
	interval := self.settings.Interval.Duration()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		self.mutex.Lock()
		suspended := self.suspended > 0
		self.mutex.Unlock()
		if suspended {
			continue
		}

		name, err := self.ask(ctx)
		self.record(name, err)

		if err == nil {
			continue
		}

		self.mutex.Lock()
		failures := self.state.ConsecutiveFailures
		self.mutex.Unlock()

		log.Warningf("the %s probe failed (%d in a row): %s", name, failures, err)
		self.escalate(ctx, failures)
	}
}

// ask runs every probe with the configured deadline and returns the first
// failure. The deadline is the whole point: a probe that eventually answers
// after ninety seconds describes a display nobody can read.
func (self *Watchdog) ask(ctx context.Context) (string, error) {
	for _, current := range self.probes {
		probeContext, cancel := context.WithTimeout(ctx, self.settings.Timeout.Duration())
		err := current.probe(probeContext)
		cancel()
		if err != nil {
			return current.name, err
		}
	}
	return "", nil
}

func (self *Watchdog) record(name string, err error) {
	self.mutex.Lock()
	defer self.mutex.Unlock()

	now := time.Now()
	self.state.LastProbeAt = now
	if err == nil {
		if self.state.ConsecutiveFailures > 0 {
			log.Noticef("the display is answering again after %d failed probe(s)", self.state.ConsecutiveFailures)
		}
		self.state.ConsecutiveFailures = 0
		self.state.LastSuccessAt = now
		self.state.LastFailure = ""
		return
	}

	self.state.ConsecutiveFailures++
	self.state.TotalFailures++
	self.state.LastFailureAt = now
	self.state.LastFailure = fmt.Sprintf("%s: %s", name, err)
}

// escalate applies the heaviest remedy whose threshold the failure count has
// reached, and only if that step has not been tried too recently.
func (self *Watchdog) escalate(ctx context.Context, failures int) {
	type step struct {
		name      string
		threshold int
		remedy    func(ctx context.Context) error
	}

	// Heaviest first: the count only ever goes up, so once it has passed the
	// restart threshold there is no point reloading the page again.
	steps := []step{
		{"restart the display", self.settings.FailuresBeforeRestartDisplay, self.remedies.RestartDisplay},
		{"restart the browser", self.settings.FailuresBeforeRestart, self.remedies.RestartBrowser},
		{"clear the cache", self.settings.FailuresBeforeClearCache, self.remedies.ClearCache},
		{"recreate the page", self.settings.FailuresBeforeRecreate, self.remedies.RecreatePage},
		{"reload the page", self.settings.FailuresBeforeReload, self.remedies.ReloadPage},
	}

	for _, current := range steps {
		if current.remedy == nil || current.threshold <= 0 || failures < current.threshold {
			continue
		}

		// Each step is given time to work before it is tried again. Without
		// this, a page that takes twenty seconds to load is reloaded every
		// time the probe runs and never finishes loading.
		cooldown := time.Duration(current.threshold) * self.settings.Interval.Duration()
		self.mutex.Lock()
		last := self.appliedAt[current.name]
		self.mutex.Unlock()
		if time.Since(last) < cooldown {
			return
		}

		self.mutex.Lock()
		self.appliedAt[current.name] = time.Now()
		self.state.LastRemedy = current.name
		self.state.LastRemedyAt = time.Now()
		self.state.RemediesApplied++
		self.mutex.Unlock()

		log.Errorf("after %d failed probes, trying to %s", failures, current.name)
		remedyContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := current.remedy(remedyContext)
		cancel()
		if err != nil {
			log.Errorf("cannot %s: %s", current.name, err)
		}
		return
	}
}
