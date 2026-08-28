// Package upgrade tells a device when a newer release of this program exists,
// and — on a device set up to allow it — replaces the container it is running
// in with one built from that release.
//
// The two halves are deliberately separate. Checking reads a public API and
// changes nothing, so it is always on. Applying needs the Docker socket, which
// is root on the host by another name, so it is off unless somebody has
// mounted the socket and said so in the configuration. See
// docs/planning/active/20260828-upgrading-from-the-web-interface.md.
package upgrade

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("upgrade")

// howOftenToCheck is the background interval. Once a day: a screen that has
// been on a wall for a year has had a year of chances to notice, and anything
// more often is asking a public API for an answer that changes every few
// weeks.
const howOftenToCheck = 24 * time.Hour

// howLongAfterAFailure is the wait before trying again when a check failed. A
// device with no route out must not spend the day retrying: it will fail every
// time, and the log is the only thing that grows.
const howLongAfterAFailure = time.Hour

// State is what the web interface shows.
type State struct {
	// Running is the version this daemon was built as.
	Running string `json:"running"`
	// Latest is the newest published release, empty until one is known.
	Latest string `json:"latest,omitempty"`
	// Notes is that release's notes, as Markdown.
	Notes string `json:"notes,omitempty"`
	// PublishedAt is when it was released.
	PublishedAt time.Time `json:"publishedAt,omitempty"`
	// URL is the release's own page.
	URL string `json:"url,omitempty"`
	// Newer says whether Latest is an upgrade from Running. A development
	// build is never out of date; see Newer.
	Newer bool `json:"newer"`
	// CheckedAt is when the answer was last obtained, so that a page can say
	// how old it is rather than implying it is live.
	CheckedAt time.Time `json:"checkedAt,omitempty"`
	// Trouble is why the last check did not work, in words for a person. A
	// device with no route to the internet is the ordinary case and says so
	// rather than looking up to date.
	Trouble string `json:"trouble,omitempty"`
}

// Checker keeps the answer to "is there a newer one", refreshing it in the
// background and on demand.
type Checker struct {
	repository string
	running    string
	client     *http.Client

	mutex     sync.Mutex
	release   Release
	checkedAt time.Time
	trouble   string
}

// NewChecker builds one. running is the version this binary reports.
func NewChecker(repository, running string) *Checker {
	return &Checker{
		repository: repository,
		running:    running,
		client:     &http.Client{Timeout: howLongToWait},
	}
}

// Run checks now and then once a day until the context ends.
func (self *Checker) Run(ctx context.Context) {
	for {
		wait := howOftenToCheck
		if _, err := self.Check(ctx); err != nil {
			wait = howLongAfterAFailure
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Check asks now and remembers the answer.
//
// A failed check does not erase what was already known: a device that saw
// 0.2.0 yesterday and cannot reach GitHub today still knows 0.2.0 exists, and
// saying so with the date of the answer is more use than saying nothing.
func (self *Checker) Check(ctx context.Context) (State, error) {
	release, err := Latest(ctx, self.client, self.repository)

	self.mutex.Lock()
	defer self.mutex.Unlock()

	if err != nil {
		self.trouble = err.Error()
		log.Debugf("cannot find out whether there is a newer release: %s", err)
		return self.stateLocked(), err
	}

	if release.Version != self.release.Version {
		log.Noticef("the newest release is %s; this is %s", release.Version, self.running)
	}
	self.release = release
	self.checkedAt = time.Now()
	self.trouble = ""
	return self.stateLocked(), nil
}

// State is what is known now, without asking anybody.
func (self *Checker) State() State {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.stateLocked()
}

// The caller holds the mutex.
func (self *Checker) stateLocked() State {
	return State{
		Running:     self.running,
		Latest:      self.release.Version,
		Notes:       self.release.Notes,
		PublishedAt: self.release.PublishedAt,
		URL:         self.release.URL,
		Newer:       Newer(self.running, self.release.Version),
		CheckedAt:   self.checkedAt,
		Trouble:     self.trouble,
	}
}
