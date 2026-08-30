package link

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/service"
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

// ErrRejected means the service would not accept the request itself: not a
// decision anybody made, but a call this device should not have made. Separate
// from ErrRefused because it says something different to whoever is looking --
// one is "somebody said no", the other is "this is a bug" -- and separate from
// a network failure because a body that is wrong now will still be wrong on
// the next tick.
var ErrRejected = errors.New("link: the service would not accept the request")

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

	// Checking reports that somebody has authorised the attempt and the device
	// is collecting the credential and proving it works. Shown because it is a
	// different thing to be waiting for: the code has done its job and the
	// person can stop holding their phone up.
	Checking bool `json:"checking,omitempty"`

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

	mutex    sync.Mutex
	attempt  *Ticket
	checking bool
	failure  string
	cancel   context.CancelFunc

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
			state.Checking = self.checking
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
	self.checking = false
	self.failure = ""
}

// Unlink forgets the credential. Nothing else: the device keeps its name, its
// identifier and its playlist, because unlinking is about who it answers to
// and not about what it is.
func (self *Linker) Unlink() error {
	self.Abandon()

	// Said out loud, because a device that has quietly stopped belonging to
	// anybody is hard to explain afterwards. This happened once during
	// development and there was no way to tell what had asked for it: nothing
	// wrote a line, and the only evidence was a section missing from a file.
	before := self.store.Current().Service
	if before.IsLinked() {
		log.Noticef("this device is no longer linked to %s (was known there as %s)",
			before.Account, before.DeviceID)
	}

	return self.store.Update(func(configuration *config.Configuration) error {
		configuration.Service.Secret = ""
		configuration.Service.Account = ""
		configuration.Service.DeviceID = ""
		configuration.Service.Name = ""
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

		authorised, err := self.ask(ctx, attempt)
		switch {
		case errors.Is(err, ErrRejected):
			log.Errorf("the service would not accept this device's request: %s", err)
			self.finish(attempt, "this device asked the service for something it would not accept")
			return
		case errors.Is(err, ErrRefused):
			self.finish(attempt, "the service refused this device")
			return
		case err != nil:
			// Worth a line but not the end: the next tick tries again.
			log.Debugf("cannot ask the service about the link yet: %s", err)
			continue
		case !authorised:
			// Nobody has pressed anything yet, which is the usual answer.
			continue
		}

		// Only now is the verifier sent, and this is the only call that sends
		// it. See redeem.
		self.beginChecking(attempt)
		secret, account, deviceId, err := self.redeem(ctx, attempt)
		switch {
		case errors.Is(err, ErrRejected):
			log.Errorf("the service would not accept this device's request: %s", err)
			self.finish(attempt, "this device asked the service for something it would not accept")
			return
		case errors.Is(err, ErrRefused):
			self.finish(attempt, "the service refused this device")
			return
		case err != nil:
			// Authorised, and then unreachable. Asked again on the next tick
			// rather than losing a link somebody has already agreed to;
			// redeeming twice returns the same credential.
			log.Debugf("cannot collect the credential yet: %s", err)
			continue
		}

		// Used before it is believed.
		//
		// Everything up to here proves somebody authorised something. It does
		// not prove that what came back works, and a screen on a wall saying
		// "linked" on the strength of an unexamined string is the kind of
		// thing nobody discovers until much later, with no keyboard in the
		// room. So the credential is spent once, on asking the service who
		// this device is, and only an answer makes it a link.
		who, err := self.identity(ctx, secret)
		switch {
		case errors.Is(err, ErrRefused):
			self.finish(attempt, "the service issued a credential that does not work")
			return
		case err == nil && deviceId != "" && who.ID != deviceId:
			// The credential works but describes a different device than the
			// one this link was for. Nothing good follows from carrying on.
			log.Errorf("the service issued a credential for %s but says this device is %s",
				deviceId, who.ID)
			self.finish(attempt, "the service disagreed about which device this is")
			return
		case err != nil:
			// The credential may be perfectly good and the network not. Keep
			// the attempt alive and try again; redeem will hand back the same
			// credential.
			log.Debugf("cannot confirm the credential yet: %s", err)
			continue
		}

		if err := self.complete(attempt, secret, account, who); err != nil {
			log.Errorf("cannot save the credential the service issued: %s", err)
			return
		}
		log.Noticef("this device is now linked to %s, known there as %s (%s)",
			account, who.Name, who.ID)
		return
	}
}

// beginChecking records that the code has done its job and the device is now
// collecting and proving the credential.
func (self *Linker) beginChecking(attempt *Ticket) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	if self.attempt == attempt {
		self.checking = true
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
func (self *Linker) complete(attempt *Ticket, secret, account string, who *Identity) error {
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
		configuration.Service.DeviceID = who.ID
		// What the service calls it, which is not always what it calls itself.
		configuration.Service.Name = who.Name
		return nil
	})

	self.attempt = nil
	self.checking = false
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
	self.checking = false
	self.failure = failure
	self.cancel = nil
}

// What is asked on every poll. No verifier: see redeem.
type exchangeRequest struct {
	Ticket string `json:"ticket"`

	// What the authorisation page shows the person deciding. Sent every time
	// rather than registered up front, because the device may be renamed
	// between showing a code and somebody scanning it.
	Name       string `json:"name,omitempty"`
	Identifier string `json:"identifier,omitempty"`
}

// What is sent once, to collect the credential.
type redeemRequest struct {
	Ticket   string `json:"ticket"`
	Verifier string `json:"verifier"`
}

// Identity is what the service says this device is, asked with the credential
// the service itself issued.
type Identity struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	UserID      string `json:"userId,omitempty"`
	IsRevoked   bool   `json:"isRevoked,omitempty"`
}

type exchangeResponse struct {
	// Secret is empty until somebody authorises the attempt, which is the
	// answer this call usually gets.
	Secret   string `json:"secret,omitempty"`
	Account  string `json:"account,omitempty"`
	DeviceID string `json:"deviceId,omitempty"`
}

// ask makes one poll: has anybody authorised this yet?
//
// It carries the ticket and never the verifier. The verifier is the half that
// redeems, and this call runs every two seconds for up to ten minutes -- three
// hundred times per attempt, all but one of them redeeming nothing. Sending
// the secret on all of them would push it through every proxy and access log
// in front of the service to accomplish nothing. See redeem.
func (self *Linker) ask(ctx context.Context, attempt *Ticket) (bool, error) {
	configuration := self.store.Current()

	body, err := json.Marshal(exchangeRequest{
		Ticket:     attempt.Ticket,
		Name:       configuration.Device.Name,
		Identifier: configuration.Device.Identifier,
	})
	if err != nil {
		return false, err
	}

	address, err := exchangeURL(configuration.Service.Address)
	if err != nil {
		return false, err
	}
	response, err := self.send(ctx, http.MethodPost, address, body, "")
	if err != nil {
		return false, err
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusAccepted:
		// Authorised, or already redeemed. Indistinguishable here and there is
		// nothing to do differently: both mean a credential is waiting. They
		// have to be indistinguishable, or a lost answer to redeem would end
		// the attempt instead of being retried.
		return true, nil
	case http.StatusForbidden:
		// The only permanent answer to this call: a person decided against it.
		return false, ErrRefused
	case http.StatusNoContent:
		// Not authorised yet, which is the usual answer.
		return false, nil
	case http.StatusBadRequest:
		// Should never happen, and if it does it is this device's fault. The
		// service says why, so that is carried through rather than replaced
		// with a guess.
		return false, fmt.Errorf("%w: %s", ErrRejected, reasonFrom(response))
	case http.StatusNotFound:
		// Not a refusal, which is what this used to be read as.
		//
		// The device shows its code before it has ever spoken to the service
		// -- the point of deriving the ticket rather than being given one,
		// because the network may be the thing somebody is in the room to fix.
		// So the service has not heard of the ticket until somebody opens the
		// link on their phone. Treating that as the end meant no attempt could
		// live long enough to be authorised. It also covers a service that has
		// not deployed the endpoint, whose router answers 404 to everything:
		// that attempt should run out and say the code expired, which is true.
		return false, nil
	default:
		return false, fmt.Errorf("link: the service answered %s", response.Status)
	}
}

// redeem collects the credential. It is the only call that sends the verifier,
// and it happens once per link.
//
// Safe to call again when the answer goes missing: the service returns the
// same credential for the same ticket until it expires. Burning the ticket on
// first success would mean a lost response locks out the device -- the one
// party that is certainly not an attacker, being the only one that holds the
// verifier at all.
func (self *Linker) redeem(ctx context.Context, attempt *Ticket) (string, string, string, error) {
	configuration := self.store.Current()

	body, err := json.Marshal(redeemRequest{
		Ticket:   attempt.Ticket,
		Verifier: attempt.Verifier,
	})
	if err != nil {
		return "", "", "", err
	}

	address, err := redeemURL(configuration.Service.Address)
	if err != nil {
		return "", "", "", err
	}
	response, err := self.send(ctx, http.MethodPost, address, body, "")
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = response.Body.Close() }()

	switch {
	case response.StatusCode == http.StatusForbidden:
		return "", "", "", ErrRefused
	case response.StatusCode == http.StatusBadRequest:
		return "", "", "", fmt.Errorf("%w: %s", ErrRejected, reasonFrom(response))
	case response.StatusCode != http.StatusOK:
		return "", "", "", fmt.Errorf("link: the service answered %s", response.Status)
	}

	var answer exchangeResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maximumResponseBytes)).Decode(&answer); err != nil {
		return "", "", "", fmt.Errorf("link: cannot read what the service said: %w", err)
	}
	if answer.Secret == "" {
		return "", "", "", fmt.Errorf("link: the service handed back no credential")
	}
	return answer.Secret, answer.Account, answer.DeviceID, nil
}

// identity asks the service who this device is, using the credential it has
// just been handed.
//
// This is what makes "linked" mean something. Until it answers, all the device
// holds is a string the service said was a credential; a screen on a wall that
// reports itself linked on the strength of that would be reporting a guess.
//
// It goes over the tunnel -- the outward websocket this device will hold open
// for as long as it is linked -- rather than to a public address. Proving a
// credential by using it exactly the way it will be used from now on is worth
// more than proving it against a second door built for the question, and it
// means the service need not have a second door at all.
func (self *Linker) identity(ctx context.Context, credential string) (*Identity, error) {
	configuration := self.store.Current()

	answer, err := service.Confirm(ctx, configuration.Service.Address, credential)
	if err != nil {
		// A credential that cannot attach is not one this device should call
		// itself linked with. The service refuses a revoked or unknown one
		// with 401 during the handshake, which arrives here as a failure to
		// attach rather than as an answer.
		if errors.Is(err, service.ErrNotAccepted) {
			return nil, ErrRefused
		}
		return nil, err
	}

	identity := &Identity{}
	if value, _ := answer["id"].(string); value != "" {
		identity.ID = value
	}
	if value, _ := answer["name"].(string); value != "" {
		identity.Name = value
	}
	if value, _ := answer["description"].(string); value != "" {
		identity.Description = value
	}
	if value, _ := answer["userId"].(string); value != "" {
		identity.UserID = value
	}
	if revoked, _ := answer["isRevoked"].(bool); revoked {
		return nil, ErrRefused
	}
	if identity.ID == "" {
		return nil, fmt.Errorf("link: the service did not say which device this is")
	}
	return identity, nil
}

// reasonFrom reads the short explanation a service puts in the body of a
// refusal. Bounded and flattened: it is written into a state the interface
// shows, and an unbounded one would be a service deciding how much of a screen
// it gets.
func reasonFrom(response *http.Response) string {
	body, err := io.ReadAll(io.LimitReader(response.Body, 512))
	if err != nil {
		return response.Status
	}
	// A JSON error object, which is what this project's own API answers with,
	// or plain text from something else.
	var carried struct {
		Error string `json:"error"`
	}
	reason := string(body)
	if err := json.Unmarshal(body, &carried); err == nil && carried.Error != "" {
		reason = carried.Error
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		return response.Status
	}
	if len(reason) > 160 {
		reason = reason[:160]
	}
	return reason
}

// send makes one request, with the credential as a bearer token when there is
// one to send.
func (self *Linker) send(ctx context.Context, method, address string, body []byte, credential string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	return self.client.Do(request)
}

// exchangeURL is where the device asks about its attempt. Not the address the
// phone opens: that one is a page for a person, this one is for the daemon.
func exchangeURL(service string) (string, error) {
	return serviceURL(service, "api", "v1", "device", "link", "exchange")
}

// redeemURL is where the verifier goes, and the only place it goes.
func redeemURL(service string) (string, error) {
	return serviceURL(service, "api", "v1", "device", "link", "redeem")
}
