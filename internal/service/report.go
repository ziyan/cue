package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/deferutil"
)

const (
	// How often a picture is sent. Under the two minutes after which the
	// service closes an idle stream, so the stream stays useful between
	// reports rather than being reopened every time -- and often enough that
	// somebody looking at a screen from elsewhere is looking at something
	// recent.
	reportInterval = 30 * time.Second

	// How long to wait before attaching again. A screen's network is the
	// thing somebody is often in the room to fix, so this backs off to a
	// minute and stays there rather than giving up: a device that stopped
	// trying would need somebody to walk up to it, which is the situation
	// this program exists to avoid.
	firstRetry   = 2 * time.Second
	longestRetry = time.Minute
)

// Picture is what the reporter sends. Taken as a function rather than a
// dependency so that this package does not need to know about X, and so a test
// can hand it a picture without one.
type Picture func(ctx context.Context) (body []byte, contentType string, err error)

// Reporter keeps a connection to the service open and tells it what this
// screen is showing.
//
// It runs only while the device is linked. An unlinked device has no
// credential and nothing to say to anybody, and there is no separate setting
// for reporting: being linked is the choice, and a second switch would be one
// more thing to check when a picture is missing.
type Reporter struct {
	store   *config.Store
	picture Picture

	mutex     sync.Mutex
	attached  bool
	lastSent  time.Time
	trouble   string
	cancel    context.CancelFunc
	waitGroup sync.WaitGroup
}

// State is what the interface shows about reporting.
type State struct {
	// Attached reports that the connection is up.
	Attached bool `json:"attached"`

	// LastReportedAt is when a picture was last accepted.
	LastReportedAt *time.Time `json:"lastReportedAt,omitempty"`

	// Trouble is why it is not attached, when it is not. A sentence, because
	// somebody reads it.
	Trouble string `json:"trouble,omitempty"`
}

func New(store *config.Store, picture Picture) *Reporter {
	return &Reporter{store: store, picture: picture}
}

// State reports what the interface should show.
func (self *Reporter) State() State {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	state := State{Attached: self.attached, Trouble: self.trouble}
	if !self.lastSent.IsZero() {
		sent := self.lastSent
		state.LastReportedAt = &sent
	}
	return state
}

// Start begins attaching, and keeps attaching for as long as the device is
// linked.
func (self *Reporter) Start(ctx context.Context) {
	running, cancel := context.WithCancel(ctx)

	self.mutex.Lock()
	if self.cancel != nil {
		self.cancel()
	}
	self.cancel = cancel
	self.mutex.Unlock()

	self.waitGroup.Add(1)
	go func() {
		defer deferutil.Recover()
		defer self.waitGroup.Done()
		self.run(running)
	}()
}

// Close stops reporting and waits for it to stop.
func (self *Reporter) Close() error {
	self.mutex.Lock()
	if self.cancel != nil {
		self.cancel()
		self.cancel = nil
	}
	self.mutex.Unlock()
	self.waitGroup.Wait()
	return nil
}

// run attaches, reports until something breaks, and attaches again.
func (self *Reporter) run(ctx context.Context) {
	wait := firstRetry
	for {
		if ctx.Err() != nil {
			return
		}

		configuration := self.store.Current()
		if !configuration.Service.IsLinked() {
			// Nothing to say and nobody to say it to. Checked on a timer
			// rather than watched, because linking is rare and a device that
			// has just been linked can wait a moment.
			self.setTrouble("")
			if !sleep(ctx, reportInterval) {
				return
			}
			continue
		}

		err := self.attach(ctx, configuration)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			self.setTrouble(err.Error())
			log.Debugf("cannot report to the service: %s", err)
			if !sleep(ctx, wait) {
				return
			}
			wait *= 2
			if wait > longestRetry {
				wait = longestRetry
			}
			continue
		}
		wait = firstRetry
	}
}

// attach holds one connection for as long as it lasts.
func (self *Reporter) attach(ctx context.Context, configuration *config.Configuration) error {
	connection, err := dial(ctx, configuration.Service.Address, configuration.Service.Secret.Reveal())
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()

	log.Noticef("attached to the service at %s", configuration.Service.Address)
	self.setAttached(true)
	defer self.setAttached(false)

	// One HTTP client over the tunnel. Every request it makes is dialled as a
	// stream, and keep-alive means a run of reports shares one rather than
	// opening a stream each time.
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(dialing context.Context, _, _ string) (net.Conn, error) {
				return connection.open(dialing)
			},
			// The service closes an idle stream after two minutes, so a
			// connection kept longer than that is one that will fail on its
			// next use. Dropped a little sooner instead.
			IdleConnTimeout: streamIdleTimeout - 15*time.Second,
		},
		Timeout: 60 * time.Second,
	}

	// Asked once, before anything is sent. It proves the tunnel carries a
	// whole HTTP conversation rather than just opening, and it is the first
	// thing that would fail if this device had been revoked since it linked --
	// which is worth finding out before spending a photograph on it.
	who, err := self.identity(ctx, client)
	if err != nil {
		return fmt.Errorf("service: attached but could not ask who this device is: %w", err)
	}
	log.Noticef("the service knows this device as %v (%v)", who["name"], who["id"])
	if expected := configuration.Service.DeviceID; expected != "" && who["id"] != expected {
		// The credential works and is for something else. Nothing good
		// follows from reporting this screen as that device.
		return fmt.Errorf("service: this credential is for %v, not %s", who["id"], expected)
	}

	for {
		if err := self.reportOnce(ctx, client); err != nil {
			return err
		}
		if !connection.alive() {
			return fmt.Errorf("service: the connection went away")
		}
		if !sleep(ctx, reportInterval) {
			return nil
		}
	}
}

// reportOnce sends one picture.
func (self *Reporter) reportOnce(ctx context.Context, client *http.Client) error {
	sending, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	body, contentType, err := self.picture(sending)
	if err != nil {
		// A screen that cannot be photographed is not a reason to drop the
		// connection: the X server may be restarting, and the next report is
		// thirty seconds away.
		log.Debugf("cannot photograph the screen to report it: %s", err)
		return nil
	}

	request, err := http.NewRequestWithContext(sending, http.MethodPost,
		"http://"+tunnelHost+"/api/v1/device/screenshot", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))

	switch {
	case response.StatusCode == http.StatusUnauthorized:
		// The credential has stopped working, which usually means somebody
		// revoked this device. Reconnecting will not help.
		return fmt.Errorf("service: this device is no longer accepted")
	case response.StatusCode >= 300:
		return fmt.Errorf("service: the service answered %s", response.Status)
	}

	self.mutex.Lock()
	self.lastSent = time.Now()
	self.trouble = ""
	self.mutex.Unlock()
	return nil
}

// identity asks the service who this device is, over the tunnel.
func (self *Reporter) identity(ctx context.Context, client *http.Client) (map[string]any, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+tunnelHost+"/api/v1/device/self", nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("service: the service answered %s", response.Status)
	}
	var answer map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&answer); err != nil {
		return nil, err
	}
	return answer, nil
}

func (self *Reporter) setAttached(attached bool) {
	self.mutex.Lock()
	self.attached = attached
	self.mutex.Unlock()
}

func (self *Reporter) setTrouble(trouble string) {
	self.mutex.Lock()
	self.trouble = trouble
	self.mutex.Unlock()
}

// sleep waits, and reports whether it finished rather than being stopped.
func sleep(ctx context.Context, howLong time.Duration) bool {
	timer := time.NewTimer(howLong)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Confirm asks the service who this device is, over a connection opened for
// the purpose and closed again.
//
// Used when a device has just been handed a credential and needs to know
// whether it works before calling itself linked. It goes over the tunnel
// rather than to a public endpoint, because the tunnel is the thing the
// credential is for: proving a credential by using it the way it will actually
// be used is worth more than proving it against a second door built for the
// question. It also means there need not be a second door.
func Confirm(ctx context.Context, address, credential string) (map[string]any, error) {
	connection, err := dial(ctx, address, credential)
	if err != nil {
		return nil, err
	}
	defer func() { _ = connection.Close() }()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(dialing context.Context, _, _ string) (net.Conn, error) {
				return connection.open(dialing)
			},
		},
		Timeout: 30 * time.Second,
	}
	defer client.CloseIdleConnections()

	reporter := &Reporter{}
	return reporter.identity(ctx, client)
}
