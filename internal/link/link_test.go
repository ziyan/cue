package link

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
)

// newStore returns a configuration store on a temporary file, pointed at the
// given service.
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
	var authorised atomic.Bool
	var sawVerifier atomic.Value
	sawVerifier.Store("")

	service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var asked exchangeRequest
		if err := json.NewDecoder(request.Body).Decode(&asked); err != nil {
			t.Errorf("the device sent something undecodable: %s", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		sawVerifier.Store(asked.Verifier)

		// The service checks the pairing exactly as this package documents it.
		if deriveTicket(asked.Verifier) != asked.Ticket {
			t.Errorf("the verifier does not hash to the ticket")
			response.WriteHeader(http.StatusForbidden)
			return
		}
		if !authorised.Load() {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(exchangeResponse{
			Secret:   "an-example-secret",
			Account:  "somebody@example.com",
			DeviceID: "device-1",
		})
	}))
	defer service.Close()

	store := newStore(t, service.URL)
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
	waitFor(t, time.Second, func() bool { return sawVerifier.Load().(string) != "" })
	if store.Current().Service.Secret.IsSet() {
		t.Fatal("a credential was stored before the attempt was authorised")
	}

	authorised.Store(true)
	waitFor(t, 5*time.Second, func() bool { return store.Current().Service.IsLinked() })

	configuration := store.Current()
	if secret := configuration.Service.Secret.Reveal(); secret != "an-example-secret" {
		t.Errorf("the stored credential is %q", secret)
	}
	if configuration.Service.Account != "somebody@example.com" {
		t.Errorf("the device recorded the account as %q", configuration.Service.Account)
	}

	// And the attempt is over: no code is left on the screen.
	final := linker.State()
	if final.Pending {
		t.Error("the attempt is still pending after it succeeded")
	}
	if !final.Linked {
		t.Error("the state does not report the device as linked")
	}
}

// A device on a network that comes and goes must still link when it comes
// back. This is the case the program is built for, so a failure to reach the
// service cannot end an attempt.
func TestANetworkFailureDoesNotEndTheAttempt(t *testing.T) {
	var reachable atomic.Bool
	var calls atomic.Int32

	service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if !reachable.Load() {
			// What a service in the middle of a deploy looks like from here.
			response.WriteHeader(http.StatusBadGateway)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(exchangeResponse{
			Secret: "an-example-secret-that-arrived-late", Account: "somebody@example.com",
		})
	}))
	defer service.Close()

	store := newStore(t, service.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Several failures go by and the attempt is still alive.
	waitFor(t, 5*time.Second, func() bool { return calls.Load() >= 2 })
	if !linker.State().Pending {
		t.Fatal("a network failure ended the attempt")
	}

	reachable.Store(true)
	waitFor(t, 5*time.Second, func() bool { return store.Current().Service.IsLinked() })
	if secret := store.Current().Service.Secret.Reveal(); secret != "an-example-secret-that-arrived-late" {
		t.Errorf("the stored credential is %q", secret)
	}
}

// A refusal is different from a failure: the service has decided, and asking
// again will not change its mind.
func TestARefusalEndsTheAttempt(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusForbidden)
	}))
	defer service.Close()

	store := newStore(t, service.URL)
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
	service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer service.Close()

	store := newStore(t, service.URL)
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
	service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer service.Close()

	store := newStore(t, service.URL)
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
		t.Error("starting again showed the same code")
	}
	if linker.State().URL != second.URL {
		t.Error("the device is not showing the newest code")
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
	var authorised atomic.Bool
	service := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			if !authorised.Load() {
				response.WriteHeader(http.StatusNoContent)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(exchangeResponse{
				Secret:   "an-example-secret",
				Account:  "somebody@example.com",
				DeviceID: "device-1",
			})
		}))
	defer service.Close()

	store := newStore(t, service.URL)
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

	authorised.Store(true)
	waitFor(t, 5*time.Second, func() bool { return store.Current().Service.IsLinked() })
	// Kept sampling for a moment after the credential lands, because the write
	// that clears the attempt is the one that comes second.
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
	var known atomic.Bool
	var polls atomic.Int64

	service := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			polls.Add(1)
			if !known.Load() {
				// Nobody has opened the link yet, so this ticket means nothing
				// here.
				response.WriteHeader(http.StatusNotFound)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(exchangeResponse{
				Secret:   "an-example-secret",
				Account:  "somebody@example.com",
				DeviceID: "device-1",
			})
		}))
	defer service.Close()

	store := newStore(t, service.URL)
	linker := New(store)
	defer func() { _ = linker.Close() }()

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// It has been told no such ticket, more than once, and is still waiting.
	waitFor(t, 5*time.Second, func() bool { return polls.Load() >= 2 })
	if state := linker.State(); !state.Pending || state.Linked {
		t.Fatalf("the attempt gave up on a ticket the service had not heard of: %+v", state)
	}
	if state := linker.State(); state.Error != "" {
		t.Errorf("an unheard-of ticket was reported as a failure: %q", state.Error)
	}

	// Then somebody opens the link on their phone and authorises it.
	known.Store(true)
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
