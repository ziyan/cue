package link

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/deferutil"
)

var log = logging.MustGetLogger("link")

const (
	// exchangeInterval is how often the device asks whether the link has been
	// authorised yet. Somebody is standing at the screen waiting for it to
	// change, so this is short enough not to feel broken and long enough that
	// ten minutes of waiting is not thousands of requests.
	exchangeInterval = 2 * time.Second

	// exchangeTimeout bounds one attempt. Short: the device is asking a
	// question whose answer is almost always "not yet".
	exchangeTimeout = 15 * time.Second

	// maximumResponseBytes bounds what is read back from the service. The
	// answer is a small object; anything larger is something else, and reading
	// it into memory on a device with a screen attached is not required to
	// find that out.
	maximumResponseBytes = 64 << 10
)

// ErrRefused means the service has decided this attempt will not succeed.
// Distinct from a network failure on purpose: one is worth retrying and the
// other is not.
var ErrRefused = errors.New("link: the service refused the attempt")

// State is what the interface shows about linking.
type State struct {
	// Linked reports that this device holds a credential.
	Linked bool `json:"linked"`

	// Account names who it belongs to, when it is linked and the service has
	// said. Empty otherwise.
	Account string `json:"account,omitempty"`

	// Pending reports that an attempt is in progress, and URL is what the QR
	// code carries. Both are empty when nothing is being attempted.
	Pending bool   `json:"pending"`
	URL     string `json:"url,omitempty"`

	// ExpiresAt is when the pending attempt stops being answerable.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`

	// Error is why the last attempt ended, when it ended badly. Shown to
	// whoever is standing at the screen, so it is a sentence rather than a
	// code.
	Error string `json:"error,omitempty"`
}

// Linker owns the one linking attempt a device may have in progress.
//
// One at a time, deliberately. Two codes on one screen is a question nobody
// should have to answer, and starting a second attempt is what somebody does
// when they think the first went wrong.
type Linker struct {
	store  *config.Store
	client *http.Client

	// How long one attempt is good for. Fixed at construction; a field rather
	// than the constant so a test can watch an attempt expire.
	lifetime time.Duration

	mutex   sync.Mutex
	attempt *Ticket
	failure string
	cancel  context.CancelFunc

	waitGroup sync.WaitGroup
}

// New returns a linker over the given configuration store.
func New(store *config.Store) *Linker {
	return &Linker{
		store:    store,
		lifetime: ticketLifetime,
		client: &http.Client{
			Timeout: exchangeTimeout,
		},
	}
}

// State reports what the interface should show.
func (self *Linker) State() State {
	configuration := self.store.Current()

	self.mutex.Lock()
	defer self.mutex.Unlock()

	state := State{
		Linked:  configuration.Service.Secret.IsSet(),
		Account: configuration.Service.Account,
		Error:   self.failure,
	}
	if self.attempt != nil {
		if address, err := self.attempt.URL(configuration.Service.Address); err == nil {
			expiresAt := self.attempt.ExpiresAt
			state.Pending = true
			state.URL = address
			state.ExpiresAt = &expiresAt
		}
	}
	return state
}

// Start begins an attempt, abandoning any attempt already running.
//
// It returns as soon as there is a code to show. Whether anybody authorises it
// is somebody else's decision, made somewhere else, and waiting for it here
// would leave a person looking at a screen that had not changed.
func (self *Linker) Start(ctx context.Context) (State, error) {
	configuration := self.store.Current()
	if !configuration.Service.IsConfigured() {
		return State{}, fmt.Errorf("link: no service address is configured")
	}

	attempt, err := newTicket(time.Now(), self.lifetime)
	if err != nil {
		return State{}, err
	}
	if _, err := attempt.URL(configuration.Service.Address); err != nil {
		return State{}, err
	}

	self.mutex.Lock()
	if self.cancel != nil {
		self.cancel()
	}
	exchangeContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	self.attempt = attempt
	self.failure = ""
	self.cancel = cancel
	self.mutex.Unlock()

	self.waitGroup.Add(1)
	go func() {
		defer deferutil.Recover()
		defer self.waitGroup.Done()
		self.exchangeUntilLinked(exchangeContext, attempt)
	}()

	return self.State(), nil
}

// Abandon forgets the attempt in progress, if there is one.
func (self *Linker) Abandon() {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	if self.cancel != nil {
		self.cancel()
		self.cancel = nil
	}
	self.attempt = nil
	self.failure = ""
}

// Unlink forgets the credential. Nothing else: the device keeps its name, its
// identifier and its playlist, because unlinking is about who it answers to
// and not about what it is.
func (self *Linker) Unlink() error {
	self.Abandon()
	return self.store.Update(func(configuration *config.Configuration) error {
		configuration.Service.Secret = ""
		configuration.Service.Account = ""
		configuration.Service.DeviceID = ""
		return nil
	})
}

// Close stops any attempt and waits for it to finish.
func (self *Linker) Close() error {
	self.Abandon()
	self.waitGroup.Wait()
	return nil
}

// exchangeUntilLinked asks the service, on a timer, whether the attempt has
// been authorised.
//
// A network failure is not the end of the attempt. This runs on devices whose
// network is the thing somebody is in the room to fix, and a link that only
// worked when the network happened to be up at the instant somebody pressed
// authorise would be infuriating. A refusal is different: the service has
// decided, and asking again will not change its mind.
func (self *Linker) exchangeUntilLinked(ctx context.Context, attempt *Ticket) {
	ticker := time.NewTicker(exchangeInterval)
	defer ticker.Stop()

	// Asked once before the first tick. Somebody who authorises the link on
	// their phone the instant the code appears should not then watch the
	// screen say nothing for another two seconds.
	for first := true; ; first = false {
		if !first {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}

		if attempt.IsExpired(time.Now()) {
			self.finish(attempt, "the code expired before it was authorised")
			return
		}

		secret, account, deviceId, err := self.exchange(ctx, attempt)
		switch {
		case err == nil && secret == "":
			// Not authorised yet, which is the usual answer.
			continue
		case errors.Is(err, ErrRefused):
			self.finish(attempt, "the service refused this device")
			return
		case err != nil:
			// Worth a line but not the end: the next tick tries again.
			log.Debugf("cannot ask the service about the link yet: %s", err)
			continue
		}

		if err := self.complete(attempt, secret, account, deviceId); err != nil {
			log.Errorf("cannot save the credential the service issued: %s", err)
			return
		}
		log.Noticef("this device is now linked to %s", account)
		return
	}
}

// complete stores the credential and ends the attempt as one step.
//
// One step because the two orders are both wrong separately. Saving first
// leaves a moment where the device is linked and still showing a live code --
// which anything polling can see, and which serves a QR code for an attempt
// that is already over. Ending first and then failing to save loses the reason
// it failed. So the attempt is cleared and the credential written while the
// same lock is held, and whoever asks next sees one state or the other.
func (self *Linker) complete(attempt *Ticket, secret, account, deviceId string) error {
	self.mutex.Lock()
	defer self.mutex.Unlock()

	// A later attempt started while this one was in flight owns the state now,
	// and its code is the one on the screen.
	if self.attempt != attempt {
		return nil
	}

	err := self.store.Update(func(configuration *config.Configuration) error {
		configuration.Service.Secret = config.Secret(secret)
		configuration.Service.Account = account
		configuration.Service.DeviceID = deviceId
		return nil
	})

	self.attempt = nil
	self.cancel = nil
	if err != nil {
		self.failure = "the credential could not be saved"
		return err
	}
	self.failure = ""
	return nil
}

// finish clears the attempt, provided it is still the one running. A later
// attempt started while this one was in flight owns the state now.
func (self *Linker) finish(attempt *Ticket, failure string) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	if self.attempt != attempt {
		return
	}
	self.attempt = nil
	self.failure = failure
	self.cancel = nil
}

type exchangeRequest struct {
	Ticket   string `json:"ticket"`
	Verifier string `json:"verifier"`

	// What the authorisation page shows the person deciding. Sent every time
	// rather than registered up front, because the device may be renamed
	// between showing a code and somebody scanning it.
	Name       string `json:"name,omitempty"`
	Identifier string `json:"identifier,omitempty"`
}

type exchangeResponse struct {
	// Secret is empty until somebody authorises the attempt, which is the
	// answer this call usually gets.
	Secret   string `json:"secret,omitempty"`
	Account  string `json:"account,omitempty"`
	DeviceID string `json:"deviceId,omitempty"`
}

// exchange makes one call. An empty secret with no error means "not yet".
func (self *Linker) exchange(ctx context.Context, attempt *Ticket) (string, string, string, error) {
	configuration := self.store.Current()

	body, err := json.Marshal(exchangeRequest{
		Ticket:     attempt.Ticket,
		Verifier:   attempt.Verifier,
		Name:       configuration.Device.Name,
		Identifier: configuration.Device.Identifier,
	})
	if err != nil {
		return "", "", "", err
	}

	address, err := exchangeURL(configuration.Service.Address)
	if err != nil {
		return "", "", "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return "", "", "", err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := self.client.Do(request)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = response.Body.Close() }()

	switch {
	case response.StatusCode == http.StatusNotFound:
		// Not a refusal, which is what this used to be read as.
		//
		// The device shows its code before it has ever spoken to the service
		// -- that is the point of deriving the ticket rather than being given
		// one, because the network may be the thing somebody is in the room to
		// fix. So the service has not heard of the ticket until somebody opens
		// the link on their phone, and a service that says so with a 404
		// answers the first poll of every attempt that way. Treating that as
		// the end meant no attempt could ever survive long enough to be
		// authorised.
		//
		// It also covers a service that has not deployed this endpoint yet,
		// where every poll is a 404 from the router. That attempt should run
		// out its ten minutes and say the code expired, which is true, rather
		// than claim the service refused the device, which is not.
		return "", "", "", nil
	case response.StatusCode == http.StatusForbidden:
		// A decision, rather than an absence: the verifier did not hash to the
		// ticket, or the ticket has already been redeemed.
		return "", "", "", ErrRefused
	case response.StatusCode == http.StatusNoContent:
		// Known, not authorised yet.
		return "", "", "", nil
	case response.StatusCode != http.StatusOK:
		return "", "", "", fmt.Errorf("link: the service answered %s", response.Status)
	}

	var answer exchangeResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maximumResponseBytes)).Decode(&answer); err != nil {
		return "", "", "", fmt.Errorf("link: cannot read what the service said: %w", err)
	}
	return answer.Secret, answer.Account, answer.DeviceID, nil
}

// exchangeURL is where the device asks about its attempt. Not the address the
// phone opens: that one is a page for a person, this one is for the daemon.
func exchangeURL(service string) (string, error) {
	return serviceURL(service, "api", "v1", "device", "link", "exchange")
}
