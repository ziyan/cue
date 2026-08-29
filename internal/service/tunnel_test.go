package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/service/servicetest"
)

// A stub of the service's device websocket, implementing the framing from the
// other side: text messages are control JSON, binary messages carry a
// big-endian uint16 length, the stream identifier, then the payload.
//
// It serves the real device routes over the stream with Go's own HTTP server,
// which is the point of the exercise: if this device's half of the framing is
// wrong, net/http on this side will not be able to talk to net/http on that
// side, and no amount of asserting on bytes would prove that it can.
// The stub lives in servicetest so that the linking package uses the same one:
// one implementation of the service side rather than two that can drift.
type stubService struct {
	*servicetest.Stub

	screenshots atomic.Int64

	mutex     sync.Mutex
	lastImage []byte
	lastType  string
}

func newStubService(t *testing.T) *stubService {
	t.Helper()
	stub := &stubService{}

	routes := http.NewServeMux()
	routes.HandleFunc("/api/v1/device/screenshot", func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(request.Body, 8<<20))
		stub.mutex.Lock()
		stub.lastImage = body
		stub.lastType = request.Header.Get("Content-Type")
		stub.mutex.Unlock()
		stub.screenshots.Add(1)
		response.WriteHeader(http.StatusNoContent)
	})
	routes.HandleFunc("/api/v1/device/self", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{"id": "device-1", "name": "carbon"})
	})

	stub.Stub = servicetest.New(t, routes, nil)
	return stub
}

func newStore(t *testing.T, address, credential string) *config.Store {
	t.Helper()
	configuration := config.Default()
	configuration.Service.Address = address
	configuration.Service.Secret = config.Secret(credential)
	configuration.Service.DeviceID = "device-1"
	configuration.Normalize()
	return config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)
}

func waitFor(t *testing.T, within time.Duration, why string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

// The whole point: a picture sent by this device arrives at the service, with
// net/http on both ends of a connection this package framed itself.
func TestAPictureReachesTheServiceOverTheTunnel(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, stub.Credential)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("not really a jpeg, but bytes"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "a picture to arrive", func() bool {
		return stub.screenshots.Load() > 0
	})

	stub.mutex.Lock()
	body, contentType := stub.lastImage, stub.lastType
	stub.mutex.Unlock()
	if string(body) != "not really a jpeg, but bytes" {
		t.Errorf("the service received %q", body)
	}
	if contentType != "image/jpeg" {
		t.Errorf("the picture arrived as %q", contentType)
	}

	if state := reporter.State(); !state.Attached || state.LastReportedAt == nil {
		t.Errorf("the reporter says %+v", state)
	}
}

// An unlinked device says nothing to anybody.
func TestAnUnlinkedDeviceDoesNotReport(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, "")

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	time.Sleep(500 * time.Millisecond)

	if opened := stub.Opens.Load(); opened != 0 {
		t.Errorf("an unlinked device opened %d streams", opened)
	}
	if sent := stub.screenshots.Load(); sent != 0 {
		t.Errorf("an unlinked device sent %d pictures", sent)
	}
	if state := reporter.State(); state.Attached {
		t.Error("an unlinked device reports itself attached")
	}
}

// A screen that cannot be photographed is not a reason to drop the connection.
func TestAPictureThatCannotBeTakenKeepsTheConnection(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, stub.Credential)

	var tries atomic.Int64
	reporter := New(store, func(context.Context) ([]byte, string, error) {
		if tries.Add(1) == 1 {
			return nil, "", io.ErrUnexpectedEOF
		}
		return []byte("a picture"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the reporter to attach", func() bool {
		return reporter.State().Attached
	})
	if tries.Load() < 1 {
		t.Error("no picture was attempted")
	}
	if state := reporter.State(); !state.Attached {
		t.Error("a failed photograph dropped the connection")
	}
}

// The device must ask for the one thing the service allows, and nothing else.
func TestTheDeviceOnlyOpensTheServiceItself(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, stub.Credential)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "a stream to be opened", func() bool {
		return stub.Opens.Load() > 0
	})
	// The stub fails the test itself if the device names anything else.
}

// A refused stream is not the end of the world: the connection is dropped and
// attached again rather than the device sitting there believing in a stream it
// does not have.
func TestARefusedStreamIsRecoveredFrom(t *testing.T) {
	stub := newStubService(t)
	stub.Refuse.Store(true)
	store := newStore(t, stub.Server.URL, stub.Credential)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the device to try", func() bool { return stub.Opens.Load() > 0 })

	stub.Refuse.Store(false)
	waitFor(t, 20*time.Second, "a picture to arrive once streams are allowed", func() bool {
		return stub.screenshots.Load() > 0
	})
}
