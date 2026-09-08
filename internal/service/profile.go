package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ziyan/cue/internal/config"
)

// Asking the service what this device should be.
//
// The device pulls rather than the service pushing. A push only works while
// the device is attached, so applying a profile to forty screens would be a
// loop over forty connections, any of which may be down, with no record of
// which ones took it. Pulling makes the state of a screen its own
// responsibility: a device that was off for a week converges when it comes
// back, rather than having missed something nobody wrote down.
//
// The common answer is 304 and costs almost nothing, which is what makes a
// short interval affordable.

const (
	// Bounds on service.pollInterval. A profile cannot set it -- the whole
	// service section is refused -- but a person editing the file can, and a
	// device that polls every second is a nuisance while one that polls once a
	// day is one somebody will describe as broken.
	shortestPoll = 10 * time.Second
	longestPoll  = time.Hour
	defaultPoll  = time.Minute

	// The largest profile worth reading. A settings document is a few
	// kilobytes; anything of this size is a mistake or something hostile, and
	// reading it into memory on a device with two cores is not a favour to
	// anybody.
	largestProfile = 1 << 20
)

// pollInterval is how often to ask, clamped. Following network.Run, which
// falls back rather than believing an interval that cannot be right.
func pollInterval(configuration *config.Configuration) time.Duration {
	interval := configuration.Service.PollInterval.Duration()
	switch {
	case interval <= 0:
		return defaultPoll
	case interval < shortestPoll:
		return shortestPoll
	case interval > longestPoll:
		return longestPoll
	default:
		return interval
	}
}

// pollProfileOnce asks for this device's profile and applies it.
//
// Errors are returned for the caller to log, never for it to act on: a poll
// that fails changes nothing at all, which is the whole of the failure
// behaviour. Only an explicit 200 changes the configuration, and an empty
// document is a 200 -- that is how a device learns to release what it used to
// be managed by.
func (self *Reporter) pollProfileOnce(ctx context.Context, client *http.Client) error {
	asking, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(asking, http.MethodGet,
		"http://"+tunnelHost+"/api/v1/device/profile", nil)
	if err != nil {
		return err
	}
	if tag := self.profileTag(); tag != "" {
		request.Header.Set("If-None-Match", tag)
	}

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusNotModified:
		// The common answer, and the reason a short interval is affordable.
		return nil

	case http.StatusOK:
		// Handled below.

	case http.StatusUnauthorized:
		// Change nothing. This is a successful HTTP conversation rather than a
		// failed request, so it does not fall out of "a failed poll changes
		// nothing" on its own and has to be said: the service does not
		// recognise this device, which is not the same as the service saying
		// this device is managed by nothing.
		//
		// Releasing on a 401 would mean that revoking a device -- or any
		// moment where the tunnel has no device on its context -- silently
		// reverted every managed setting on that screen. Releasing is
		// something the service says on purpose, by answering an empty
		// document, and never something this device infers from not being
		// recognised.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("service: the profile was refused; nothing changed")

	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("service: asking for the profile answered %s", response.Status)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, largestProfile))
	if err != nil {
		return err
	}

	profile := map[string]interface{}{}
	if err := json.Unmarshal(body, &profile); err != nil {
		return fmt.Errorf("service: the profile is not a settings document: %w", err)
	}

	applied := false
	err = self.store.Update(func(configuration *config.Configuration) error {
		changed, err := configuration.ApplyProfile(profile)
		applied = changed
		return err
	})
	if err != nil {
		// Deliberately without remembering the ETag, so the next poll asks
		// again rather than believing it is up to date with something it
		// failed to write.
		return fmt.Errorf("service: cannot apply the profile: %w", err)
	}

	// Remembered only after the document has actually been applied.
	self.setProfileTag(response.Header.Get("ETag"))

	if applied {
		log.Noticef("the service's profile changed this device's settings")
	}
	return nil
}

// profileTag is the ETag of the profile last applied, for the conditional
// request. Held in memory rather than in the file: on a restart the device
// asks once without it and gets the document it already has, which costs one
// request and keeps a restart from being able to leave stale bookkeeping
// behind.
func (self *Reporter) profileTag() string {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.lastProfileTag
}

func (self *Reporter) setProfileTag(tag string) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.lastProfileTag = tag
}

// PollNow makes the next turn of the reporting loop ask for the profile,
// whatever the interval says. This is what the service's nudge calls: applying
// a profile tells the devices it affects to poll at once, so a change lands
// while somebody is still looking at the screen rather than up to an interval
// later.
//
// Best effort by design, and non-blocking: correctness is entirely in the
// poll, so a nudge that arrives while one is already running is dropped rather
// than queued.
func (self *Reporter) PollNow() {
	select {
	case self.pollNow <- struct{}{}:
	default:
	}
}
