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
	attempt, err := newTicket(time.Now())
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
	other, err := newTicket(time.Now())
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
	attempt, err := newTicket(time.Now())
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

	if _, err := linker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Reach in and age the attempt, rather than waiting ten minutes.
	linker.mutex.Lock()
	linker.attempt.ExpiresAt = time.Now().Add(-time.Second)
	linker.mutex.Unlock()

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
