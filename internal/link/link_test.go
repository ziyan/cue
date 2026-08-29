package link

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
)

// newStore returns a configuration store on a temporary file, pointed at the
// given service.
// A stub of the service side, implementing the contract the two sides agreed:
//
//	POST .../device/link/exchange  {ticket, name, identifier}
//	  204  unknown, or pending      (the first poll registers, later ones do not)
//	  202  authorised or redeemed   go and collect it
//	  403  refused                  a person said no
//	POST .../device/link/redeem     {ticket, verifier}
//	  200  {secret, account, deviceId}, the same answer until the ticket expires
//	  403  the verifier does not hash to the ticket, or it was never authorised
//	GET  .../device/self            with the credential as a bearer token
//	  200  {id, name, description, userId}
//	  401  the credential does not verify
//
// It derives the ticket itself rather than calling the package's own
// deriveTicket, so the two implementations cannot drift together unnoticed.
type stubService struct {
	server *httptest.Server

	authorised atomic.Bool
	refused    atomic.Bool
	// Every request body, in order, so a test can assert where the verifier
	// did and did not appear.
	mutex    sync.Mutex
	requests []stubRequest
	// Set to answer everything with this status, for the failure cases.
	trouble atomic.Int64
	// Set to refuse the identity call, for a credential that does not work.
	identityRefuses atomic.Bool

	registeredName       string
	registeredIdentifier string
	redeemed             atomic.Bool
}

type stubRequest struct {
	path string
	body string
}

func newStubService(t *testing.T) *stubService {
	t.Helper()
	stub := &stubService{}

	handler := func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(request.Body, 1<<20))
		stub.mutex.Lock()
		stub.requests = append(stub.requests, stubRequest{path: request.URL.Path, body: string(body)})
		stub.mutex.Unlock()

		if code := stub.trouble.Load(); code != 0 {
			response.WriteHeader(int(code))
			return
		}

		switch request.URL.Path {
		case "/api/v1/device/link/exchange":
			var asked struct {
				Ticket     string `json:"ticket"`
				Verifier   string `json:"verifier"`
				Name       string `json:"name"`
				Identifier string `json:"identifier"`
			}
			_ = json.Unmarshal(body, &asked)
			if asked.Verifier != "" {
				t.Errorf("the verifier was sent to the polling call: %q", asked.Verifier)
			}
			// First write wins: only the first poll may say what this is.
			stub.mutex.Lock()
			if stub.registeredName == "" {
				stub.registeredName = asked.Name
				stub.registeredIdentifier = asked.Identifier
			}
			stub.mutex.Unlock()

			switch {
			case stub.refused.Load():
				response.WriteHeader(http.StatusForbidden)
			case stub.authorised.Load(), stub.redeemed.Load():
				response.WriteHeader(http.StatusAccepted)
			default:
				response.WriteHeader(http.StatusNoContent)
			}

		case "/api/v1/device/link/redeem":
			var asked struct {
				Ticket   string `json:"ticket"`
				Verifier string `json:"verifier"`
			}
			_ = json.Unmarshal(body, &asked)
			sum := sha256.Sum256([]byte(asked.Verifier))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != asked.Ticket {
				response.WriteHeader(http.StatusForbidden)
				return
			}
			if !stub.authorised.Load() {
				response.WriteHeader(http.StatusForbidden)
				return
			}
			// Idempotent until the ticket expires: a lost answer must not lock
			// out the one party holding the verifier.
			stub.redeemed.Store(true)
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(exchangeResponse{
				Secret:   "an-example-secret",
				Account:  "s•••@example.com",
				DeviceID: "device-1",
			})

		case "/api/v1/device/self":
			if stub.identityRefuses.Load() {
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			if request.Header.Get("Authorization") != "Bearer an-example-secret" {
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(Identity{
				ID: "device-1", Name: "Reception", Description: "an-example-identifier",
				UserID: "user-1",
			})

		default:
			http.NotFound(response, request)
		}
	}

	stub.server = httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(stub.server.Close)
	return stub
}

// bodiesFor returns every request body sent to one path.
func (self *stubService) bodiesFor(path string) []string {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	var found []string
	for _, one := range self.requests {
		if one.path == path {
			found = append(found, one.body)
		}
	}
	return found
}

// timesTheVerifierWasSent counts every request whose body mentions it at all,
// wherever it went.
func (self *stubService) timesTheVerifierWasSent(verifier string) int {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	count := 0
	for _, one := range self.requests {
		if strings.Contains(one.body, verifier) {
			count++
		}
	}
	return count
}

func (self *stubService) registered() (string, string) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.registeredName, self.registeredIdentifier
}

func newStore(t *testing.T, service string) *config.Store {
	t.Helper()
	configuration := config.Default()
	configuration.Service.Address = service
	return config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)
}

// The ticket in the QR code must be the hash of the verifier that never
// leaves the device. It is the whole reason a photograph of the screen is not
// enough to finish a link.
func TestTheTicketIsTheHashOfTheVerifier(t *testing.T) {
	attempt, err := newTicket(time.Now(), ticketLifetime)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Verifier == "" || attempt.Ticket == "" {
		t.Fatal("an attempt was minted with an empty half")
	}
	if attempt.Ticket == attempt.Verifier {
		t.Fatal("the public half is the secret half")
	}

	sum := sha256.Sum256([]byte(attempt.Verifier))
	if wanted := base64.RawURLEncoding.EncodeToString(sum[:]); attempt.Ticket != wanted {
		t.Errorf("the ticket is %q, want the hash of the verifier", attempt.Ticket)
	}

	// And two attempts do not collide.
	other, err := newTicket(time.Now(), ticketLifetime)
	if err != nil {
		t.Fatal(err)
	}
	if other.Ticket == attempt.Ticket {
		t.Error("two attempts produced the same ticket")
	}
}

// The URL is what a phone opens, so it has to carry the ticket and never the
// verifier.
func TestTheURLCarriesTheTicketAndNotTheVerifier(t *testing.T) {
	attempt, err := newTicket(time.Now(), ticketLifetime)
	if err != nil {
		t.Fatal(err)
	}
	address, err := attempt.URL("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(address, attempt.Ticket) {
		t.Errorf("%q does not carry the ticket", address)
	}
	if strings.Contains(address, attempt.Verifier) {
		t.Fatalf("%q carries the verifier, which must never leave the device", address)
	}

	// A trailing slash on the configured address must not double up.
	trailing, err := attempt.URL("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(trailing, "//link") {
		t.Errorf("%q has a doubled slash", trailing)
	}

	for _, nonsense := range []string{"", "   ", "example.com", "not a url"} {
		if _, err := attempt.URL(nonsense); err == nil {
			t.Errorf("%q was accepted as a service address", nonsense)
		}
	}
}

// The whole flow: a code is shown, somebody authorises it elsewhere, and the
// device ends up holding the credential without anybody touching it.
func TestAnAuthorisedAttemptStoresTheCredential(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	state, err := linker.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !state.Pending || state.URL == "" {
		t.Fatalf("starting an attempt did not produce a code to show: %+v", state)
	}
	if state.Linked {
		t.Error("a device reported itself linked before anybody authorised anything")
	}

	// Nothing is stored while nobody has authorised it.
	waitFor(t, time.Second, func() bool { return len(stub.bodiesFor("/api/v1/device/link/exchange")) > 0 })
	if store.Current().Service.Secret.IsSet() {
		t.Fatal("a credential was stored before the attempt was authorised")
	}

	stub.authorised.Store(true)
	waitFor(t, 5*time.Second, func() bool { return store.Current().Service.IsLinked() })

	configuration := store.Current()
	if secret := configuration.Service.Secret.Reveal(); secret != "an-example-secret" {
		t.Errorf("the stored credential is %q", secret)
	}
	if configuration.Service.Account != "s•••@example.com" {
		t.Errorf("the device recorded the account as %q", configuration.Service.Account)
	}

	final := linker.State()
	if final.Pending || final.Checking {
		t.Error("the attempt is still going after it succeeded")
	}
	if !final.Linked {
		t.Error("the state does not report the device as linked")
	}
}

// A device on a network that comes and goes must still link when it comes
// back. This is the case the program is built for, so a failure to reach the
// service cannot end an attempt.
func TestANetworkFailureDoesNotEndTheAttempt(t *testing.T) {
	stub := newStubService(t)
	stub.trouble.Store(http.StatusBadGateway)

	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return len(stub.bodiesFor("/api/v1/device/link/exchange")) >= 2 })
	if state := linker.State(); !state.Pending {
		t.Fatalf("a failure to reach the service ended the attempt: %+v", state)
	}

	// The network comes back, and somebody has authorised it in the meantime.
	stub.authorised.Store(true)
	stub.trouble.Store(0)
	waitFor(t, 10*time.Second, func() bool { return store.Current().Service.IsLinked() })
}

// A refusal is different from a failure: the service has decided, and asking
// again will not change its mind.
func TestARefusalEndsTheAttempt(t *testing.T) {
	stub := newStubService(t)
	stub.refused.Store(true)

	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return !linker.State().Pending })

	state := linker.State()
	if state.Linked {
		t.Error("a refused attempt linked the device anyway")
	}
	if state.Error == "" {
		t.Error("a refused attempt left nothing to show the person waiting")
	}
}

// A code left on a screen by somebody who wandered off stops being useful.
func TestAnExpiredAttemptEnds(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	// Born expired, rather than waiting ten minutes -- and rather than ageing
	// a live attempt, which meant writing to a ticket another goroutine was
	// already reading.
	linker.lifetime = -time.Second

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return !linker.State().Pending })
	if state := linker.State(); state.Error == "" || state.Linked {
		t.Errorf("an expired attempt ended as %+v", state)
	}
}

// Starting again abandons the first attempt, so there is never more than one
// code that could be scanned.
func TestStartingAgainAbandonsTheFirstAttempt(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	first, err := linker.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := linker.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.URL == second.URL {
		t.Error("starting again produced the same code")
	}
	if linker.State().URL != second.URL {
		t.Error("the code being shown is not the one from the latest attempt")
	}
}

// Unlinking forgets the credential and nothing else.
func TestUnlinkingForgetsOnlyTheCredential(t *testing.T) {
	store := newStore(t, "https://example.com")
	if err := store.Update(func(configuration *config.Configuration) error {
		configuration.Device.Name = "the lobby screen"
		configuration.Service.Secret = "an-example-secret"
		configuration.Service.Account = "somebody@example.com"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	linker := New(store)
	if err := linker.Unlink(); err != nil {
		t.Fatal(err)
	}

	configuration := store.Current()
	if configuration.Service.IsLinked() || configuration.Service.Account != "" {
		t.Error("unlinking left the credential behind")
	}
	if configuration.Device.Name != "the lobby screen" {
		t.Error("unlinking changed what the device is")
	}
	if configuration.Service.Address != "https://example.com" {
		t.Error("unlinking forgot where the service is, so linking again would be impossible")
	}
}

// A device with nowhere to link to says so, rather than showing a code that
// cannot work.
func TestLinkingNeedsAServiceAddress(t *testing.T) {
	store := newStore(t, "")
	linker := New(store)
	if _, err := linker.Start(context.Background()); err == nil {
		t.Error("an attempt started with no service configured")
	}
}

func waitFor(t *testing.T, within time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition was not met within %s", within)
}

// A plain address is refused, because the exchange carries the verifier up and
// the credential back down. Over http anybody on the same network gets both,
// which undoes the whole reason a photographed code is harmless.
func TestAPlainAddressIsRefused(t *testing.T) {
	for _, address := range []string{
		"http://example.com",
		"http://example.com:8080/cue",
		"http://192.0.2.10:8080",
	} {
		if _, err := serviceURL(address, "link", "a-ticket"); err == nil {
			t.Errorf("%s was accepted, and would send the credential in the clear", address)
		}
	}

	// https is the ordinary case.
	if _, err := serviceURL("https://example.com", "link", "a-ticket"); err != nil {
		t.Errorf("https://example.com was refused: %s", err)
	}

	// And a stub on this machine still works, which is what anybody building
	// against the service side runs.
	for _, address := range []string{
		"http://127.0.0.1:8080",
		"http://localhost:8080",
		"http://[::1]:8080",
	} {
		if _, err := serviceURL(address, "link", "a-ticket"); err != nil {
			t.Errorf("%s was refused: %s", address, err)
		}
	}
}

// Refused where somebody sees it, not silently at the next exchange.
func TestStartingAgainstAPlainAddressSaysSo(t *testing.T) {
	store := newStore(t, "http://example.com")
	linker := New(store)
	defer func() { _ = linker.Close() }()

	_, err := linker.Start(context.Background())
	if err == nil {
		t.Fatal("linking to a plain address was allowed")
	}
	if !strings.Contains(err.Error(), "https") && !strings.Contains(err.Error(), "in the clear") {
		t.Errorf("the refusal does not say why: %s", err)
	}
	if linker.State().Pending {
		t.Error("a code is being shown for an attempt that cannot work")
	}
}

// A device is never both linked and still showing a code.
//
// The credential used to be written to the configuration and the attempt
// cleared afterwards, so anything asking in between saw a device that was
// linked and still advertising a live code to scan -- and the picture endpoint
// went on serving one for an attempt that was already over.
//
// Caught by sampling the state as fast as it can be read, across the moment it
// changes and for a while after, rather than by looking once when the
// credential appears.
//
// It does not catch it every time: with the two writes put back the wrong way
// round this fails about half of its runs, because the window between them is
// short and a sampling goroutine is not always scheduled inside it. It cannot
// report one that is not there, though -- seeing both flags at once is only
// possible if they really were both set -- so a failure here is always real,
// and the CI machine runs it on every change.
func TestNothingSeesADeviceBothLinkedAndPending(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	done := make(chan State, 1)
	go func() {
		defer close(done)
		for {
			if state := linker.State(); state.Linked && state.Pending {
				done <- state
				return
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()

	stub.authorised.Store(true)
	waitFor(t, 5*time.Second, func() bool { return store.Current().Service.IsLinked() })
	time.Sleep(100 * time.Millisecond)
	close(stop)

	if state, caught := <-done; caught {
		t.Errorf("a device was seen linked and still showing a code: %+v", state)
	}
	if final := linker.State(); !final.Linked || final.Pending {
		t.Errorf("the settled state is %+v", final)
	}
}

// A ticket the service has not heard of yet is not a refusal.
//
// The device shows its code before it has ever spoken to the service, so the
// service learns the ticket only when somebody opens the link on their phone.
// Until then it has every right to say "no such ticket", and that is the answer
// to the first poll of every single attempt. Reading it as the end meant no
// attempt could survive long enough for anybody to authorise it -- which is how
// this behaved against a real service with the endpoint not yet deployed, where
// the router answers 404 to everything.
func TestATicketTheServiceHasNotHeardOfKeepsWaiting(t *testing.T) {
	stub := newStubService(t)
	stub.trouble.Store(http.StatusNotFound)

	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Told no such ticket, more than once, and still waiting.
	waitFor(t, 5*time.Second, func() bool { return len(stub.bodiesFor("/api/v1/device/link/exchange")) >= 2 })
	if state := linker.State(); !state.Pending || state.Linked {
		t.Fatalf("the attempt gave up on a ticket the service had not heard of: %+v", state)
	}
	if state := linker.State(); state.Error != "" {
		t.Errorf("an unheard-of ticket was reported as a failure: %q", state.Error)
	}

	// Then somebody opens the link on their phone and authorises it.
	stub.trouble.Store(0)
	stub.authorised.Store(true)
	waitFor(t, 5*time.Second, func() bool { return store.Current().Service.IsLinked() })
	if state := linker.State(); !state.Linked || state.Pending {
		t.Errorf("after authorising, the state is %+v", state)
	}
}

// A service with this endpoint not deployed at all answers 404 to every poll,
// and the attempt should run out its life saying the code expired -- which is
// true -- rather than claiming the service refused this device, which is not.
func TestAServiceWithoutTheEndpointExpiresRatherThanRefuses(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			http.NotFound(response, request)
		}))
	defer service.Close()

	store := newStore(t, service.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	// Short enough to watch it run out.
	linker.lifetime = 300 * time.Millisecond

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return !linker.State().Pending })

	state := linker.State()
	if state.Linked {
		t.Error("a device linked itself against a service that has no such endpoint")
	}
	if !strings.Contains(state.Error, "expired") {
		t.Errorf("the attempt ended as %q, which does not say the code ran out", state.Error)
	}
}

// The derivation, pinned to a fixed vector.
//
// There is a second implementation of this now -- the service checks the
// pairing by computing the same thing -- so deriveTicket is no longer an
// internal detail this package may change its mind about. Two mistakes are
// easy and both are silent: hashing the decoded 32 bytes rather than the
// verifier as it is sent, and emitting base64 with padding. Either produces a
// ticket the other side cannot match, and the only symptom is that every link
// is refused for ever.
func TestTheDerivationIsFixed(t *testing.T) {
	raw := make([]byte, 32)
	for index := range raw {
		raw[index] = byte(index)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)

	if verifier != "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8" {
		t.Fatalf("the verifier encoding changed: %q", verifier)
	}
	if ticket := deriveTicket(verifier); ticket != "6oZqdX5MOLq_qBJ8vppAnT4fk6AP8UiP9zX8-Rev_9A" {
		t.Errorf("the ticket for a known verifier is %q, which the service will not match", ticket)
	}

	// The mistake worth naming: hashing the bytes the verifier encodes rather
	// than the verifier itself. It produces a perfectly good-looking ticket.
	wrong := sha256.Sum256(raw)
	if base64.RawURLEncoding.EncodeToString(wrong[:]) == deriveTicket(verifier) {
		t.Error("hashing the raw bytes and hashing the verifier agree, so this test proves nothing")
	}

	// Neither half may carry padding: it travels in a URL and in a QR code.
	attempt, err := newTicket(time.Now(), ticketLifetime)
	if err != nil {
		t.Fatal(err)
	}
	for what, value := range map[string]string{"verifier": attempt.Verifier, "ticket": attempt.Ticket} {
		if strings.Contains(value, "=") {
			t.Errorf("the %s carries base64 padding: %q", what, value)
		}
		if len(value) != 43 {
			t.Errorf("the %s is %d characters, want 43", what, len(value))
		}
	}
}

// The whole point of splitting the poll from the redemption.
//
// The verifier is the half that redeems. Sending it on every poll -- 2s apart
// for up to ten minutes, three hundred times, all but one of them redeeming
// nothing -- pushes the secret through every proxy and access log in front of
// the service to accomplish nothing. PKCE, which this is shaped after, sends
// it exactly once, at redemption.
func TestTheVerifierIsSentOnceAndOnlyToRedeem(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Several polls before anybody authorises, so there is a real opportunity
	// to leak it.
	waitFor(t, 8*time.Second, func() bool {
		return len(stub.bodiesFor("/api/v1/device/link/exchange")) >= 3
	})

	verifier := linker.attempt.Verifier

	stub.authorised.Store(true)
	waitFor(t, 5*time.Second, func() bool { return store.Current().Service.IsLinked() })

	// Not once in a poll, whatever else those polls carried.
	for index, body := range stub.bodiesFor("/api/v1/device/link/exchange") {
		if strings.Contains(body, verifier) {
			t.Errorf("poll %d carried the verifier: %s", index, body)
		}
		if strings.Contains(body, `"verifier"`) {
			t.Errorf("poll %d carries a verifier field at all: %s", index, body)
		}
	}

	// And exactly once in total, across every request of any kind.
	if times := stub.timesTheVerifierWasSent(verifier); times != 1 {
		t.Errorf("the verifier crossed the wire %d times, want exactly 1", times)
	}
	if bodies := stub.bodiesFor("/api/v1/device/link/redeem"); len(bodies) != 1 {
		t.Errorf("redeem was called %d times", len(bodies))
	}
}

// Linked has to mean the credential works.
//
// Everything up to redemption proves somebody authorised something. It does
// not prove that what came back is usable, and a screen on a wall reporting
// itself linked on the strength of an unexamined string is a lie nobody finds
// out about until much later, with no keyboard in the room.
func TestACredentialThatDoesNotWorkIsNotALink(t *testing.T) {
	stub := newStubService(t)
	stub.identityRefuses.Store(true)
	stub.authorised.Store(true)

	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return !linker.State().Pending })

	if store.Current().Service.Secret.IsSet() {
		t.Error("a credential the service would not honour was stored anyway")
	}
	if state := linker.State(); state.Linked {
		t.Error("the device reported itself linked with a credential that does not work")
	}
	if state := linker.State(); state.Error == "" {
		t.Error("nothing was shown to the person waiting")
	}
}

// The credential is proved by using it, with the credential as a bearer token.
func TestTheCredentialIsProvedBeforeItIsBelieved(t *testing.T) {
	stub := newStubService(t)
	stub.authorised.Store(true)

	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return store.Current().Service.IsLinked() })

	// It asked who it was, and only stored anything afterwards.
	asked := false
	stub.mutex.Lock()
	for _, one := range stub.requests {
		if one.path == "/api/v1/device/self" {
			asked = true
		}
	}
	stub.mutex.Unlock()
	if !asked {
		t.Error("the device never asked the service who it was")
	}
}

// A lost answer to redeem must not lose the link. The device is the only party
// holding the verifier, so it is the wrong one to lock out.
func TestRedeemingAgainAfterALostAnswerStillLinks(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	// Authorised, but every answer is lost on the way back.
	stub.authorised.Store(true)
	stub.trouble.Store(http.StatusGatewayTimeout)

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 8*time.Second, func() bool { return len(stub.bodiesFor("/api/v1/device/link/exchange")) >= 2 })
	if store.Current().Service.IsLinked() {
		t.Fatal("linked despite never receiving an answer")
	}

	// The network recovers. The ticket has been marked redeemed on the service
	// by then in the real one; here the point is that the device asks again
	// and completes rather than having given up.
	stub.trouble.Store(0)
	waitFor(t, 10*time.Second, func() bool { return store.Current().Service.IsLinked() })
	if state := linker.State(); !state.Linked {
		t.Errorf("the attempt did not recover: %+v", state)
	}
}

// Only the first poll may say what the device is called. Dropping the verifier
// from the poll removed the only thing that gated registration, so without
// first-write-wins anybody holding a photograph of the code could rewrite what
// the authorisation page shows the person deciding.
func TestOnlyTheFirstPollSaysWhatThisDeviceIs(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.server.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return len(stub.bodiesFor("/api/v1/device/link/exchange")) >= 1 })

	name, identifier := stub.registered()
	if name != store.Current().Device.Name {
		t.Errorf("the service registered the name %q", name)
	}
	if identifier != store.Current().Device.Identifier {
		t.Errorf("the service registered the identifier %q", identifier)
	}
}
