package servicetest

import (
	"net/http"
	"sync"
	"testing"
)

// The stub is used from two goroutines and has to be safe in that use.
//
// httptest serves on goroutines of its own, so every field of the stub that a
// handler consults is shared with whatever the test does next -- and revoking a
// device mid-flight, which is what tunnel_test.go does, means writing one of
// those fields while the handlers are running.
//
// This is here rather than left to the tests that use the stub because of how
// it was found: the race detector named it on a pull request bumping React,
// which cannot break a Go test about credentials. It had been losing the race
// only occasionally, so it surfaced on an unrelated commit and would have sent
// whoever owned that commit looking in the wrong place. Driving it hard here
// makes it fail every time instead of once in a hundred runs, and on this
// machine rather than only in CI.
func TestTheCredentialIsSafeToChangeWhileServing(t *testing.T) {
	stub := New(t, http.NotFoundHandler(), nil)
	address := stub.Server.URL + "/api/v1/device/websocket"

	var waiting sync.WaitGroup
	waiting.Add(2)

	go func() {
		defer waiting.Done()
		for count := range 200 {
			if count%2 == 0 {
				stub.SetCredential("one-credential")
			} else {
				stub.SetCredential("another-credential")
			}
		}
	}()

	go func() {
		defer waiting.Done()
		for range 200 {
			// Not a websocket request, so the upgrade never happens -- but the
			// handler reads the credential before it gets that far, which is
			// the access this is about.
			response, err := http.Get(address)
			if err != nil {
				continue
			}
			_ = response.Body.Close()
		}
	}()

	waiting.Wait()
}
