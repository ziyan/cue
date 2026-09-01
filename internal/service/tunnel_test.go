package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
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
	states      atomic.Int64
	profiles    atomic.Int64
	playlists   atomic.Int64

	mutex     sync.Mutex
	lastImage []byte
	lastType  string
	lastState []byte

	// What the stub answers when asked for a profile, and what it says the
	// version of that answer is.
	profile     string
	profileTag  string
	profileCode int
	lastAsked   string

	// The same for the playlist. playlistCode defaults to 204, which is what
	// a device with none assigned is told and is the state every screen in
	// service is in.
	playlist     string
	playlistTag  string
	playlistCode int
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
	routes.HandleFunc("/api/v1/device/state", func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(request.Body, 128<<10))
		if !json.Valid(body) {
			// The service refuses anything that is not JSON, so this must too
			// or a device could send rubbish and the test would not notice.
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		stub.mutex.Lock()
		stub.lastState = body
		stub.mutex.Unlock()
		stub.states.Add(1)
		response.WriteHeader(http.StatusNoContent)
	})
	routes.HandleFunc("/api/v1/device/profile", func(response http.ResponseWriter, request *http.Request) {
		stub.mutex.Lock()
		document, tag, code := stub.profile, stub.profileTag, stub.profileCode
		stub.lastAsked = request.Header.Get("If-None-Match")
		stub.mutex.Unlock()
		stub.profiles.Add(1)

		if code != 0 && code != http.StatusOK {
			response.WriteHeader(code)
			return
		}
		if tag != "" && request.Header.Get("If-None-Match") == tag {
			response.WriteHeader(http.StatusNotModified)
			return
		}
		if tag != "" {
			response.Header().Set("ETag", tag)
		}
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		if document == "" {
			document = "{}"
		}
		_, _ = response.Write([]byte(document))
	})
	routes.HandleFunc("/api/v1/device/playlist", func(response http.ResponseWriter, request *http.Request) {
		stub.mutex.Lock()
		document, tag, code := stub.playlist, stub.playlistTag, stub.playlistCode
		stub.mutex.Unlock()
		stub.playlists.Add(1)

		if code == 0 {
			code = http.StatusNoContent
		}
		if code != http.StatusOK {
			response.WriteHeader(code)
			return
		}
		if tag != "" && request.Header.Get("If-None-Match") == tag {
			response.WriteHeader(http.StatusNotModified)
			return
		}
		if tag != "" {
			response.Header().Set("ETag", tag)
		}
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = response.Write([]byte(document))
	})
	routes.HandleFunc("/api/v1/device/self", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{"id": "device-1", "name": "carbon"})
	})

	stub.Stub = servicetest.New(t, routes, nil)
	return stub
}

// serves sets what the stub answers when asked for a profile.
func (self *stubService) serves(document, tag string) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.profile, self.profileTag, self.profileCode = document, tag, http.StatusOK
}

// showsPlaylist makes the stub answer 200 with this document.
func (self *stubService) showsPlaylist(document, tag string) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.playlist, self.playlistTag, self.playlistCode = document, tag, http.StatusOK
}

func (self *stubService) refuses(code int) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.profileCode = code
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
	}, nil)
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
	}, nil)
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
	}, nil)
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
	}, nil)
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
	}, nil)
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the device to try", func() bool { return stub.Opens.Load() > 0 })

	stub.Refuse.Store(false)
	waitFor(t, 20*time.Second, "a picture to arrive once streams are allowed", func() bool {
		return stub.screenshots.Load() > 0
	})
}

// What the screen is showing goes up beside the picture, so an account can
// read it without looking at one.
func TestWhatTheScreenIsShowingIsReported(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, stub.Credential)

	reporter := New(store,
		func(context.Context) ([]byte, string, error) {
			return []byte("a picture"), "image/jpeg", nil
		},
		func(context.Context) (any, error) {
			return map[string]any{"showing": map[string]any{"title": "Reception dashboard"}}, nil
		})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "a description to arrive", func() bool {
		return stub.states.Load() > 0
	})

	stub.mutex.Lock()
	described := string(stub.lastState)
	stub.mutex.Unlock()
	if !strings.Contains(described, "Reception dashboard") {
		t.Errorf("the service received %q", described)
	}
	if !json.Valid([]byte(described)) {
		t.Error("what was sent is not valid JSON, which the service refuses")
	}

	// It rides the stream the picture is already using.
	if opened := stub.Opens.Load(); opened != 1 {
		t.Errorf("%d streams were opened; the description should share one", opened)
	}
}

// A device that cannot say what it is showing still sends pictures.
func TestADescriptionThatCannotBeMadeDoesNotStopPictures(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, stub.Credential)

	reporter := New(store,
		func(context.Context) ([]byte, string, error) {
			return []byte("a picture"), "image/jpeg", nil
		},
		func(context.Context) (any, error) { return nil, io.ErrUnexpectedEOF })
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "a picture", func() bool { return stub.screenshots.Load() > 0 })
	if state := reporter.State(); !state.Attached {
		t.Error("a description that could not be made dropped the connection")
	}
}

// A revoked device meets a refusal at the handshake, and must know it is one.
//
// This is the path nobody can exercise against the real service without
// revoking a real device, so it is pinned here instead. The check is that the
// refusal is a sentinel rather than a sentence: the first version asked
// whether the error text contained "401", which worked, and would have gone on
// working right up until somebody reworded a message -- at which point a
// revoked device would have retried a dead credential for ten minutes instead
// of saying so.
func TestARefusedCredentialIsToldApartFromAnUnreachableService(t *testing.T) {
	stub := newStubService(t)

	// A credential the service will not take, which is what revocation looks
	// like from here.
	_, err := dial(context.Background(), stub.Server.URL, "a-revoked-credential", nil, nil)
	if err == nil {
		t.Fatal("the service accepted a credential it should not have")
	}
	if !errors.Is(err, ErrNotAccepted) {
		t.Errorf("a refused credential gave %v, which callers cannot tell from a network failure", err)
	}

	// And a service that is not there at all is a different thing, worth
	// trying again.
	_, err = dial(context.Background(), "http://127.0.0.1:1", "any-credential", nil, nil)
	if err == nil {
		t.Fatal("dialling nothing succeeded")
	}
	if errors.Is(err, ErrNotAccepted) {
		t.Error("an unreachable service was reported as a refused credential")
	}
}

// A device whose credential has been revoked stops reporting rather than
// retrying for ever, and says why.
func TestARevokedDeviceStopsReporting(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, stub.Credential)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	}, nil)
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the first picture", func() bool {
		return stub.screenshots.Load() > 0
	})

	// Somebody revokes the device. The tunnel authenticates once, at the
	// handshake, so an attached device goes on reporting until its connection
	// ends -- which is worth knowing and is true of the real service too. The
	// connection is dropped here to get to the part this test is about.
	stub.Credential = "something-else-entirely"
	sent := stub.screenshots.Load()
	stub.Disconnect()

	waitFor(t, 20*time.Second, "the device to notice it is no longer accepted", func() bool {
		state := reporter.State()
		return !state.Attached && state.Trouble != ""
	})
	if now := stub.screenshots.Load(); now > sent+1 {
		t.Errorf("a revoked device sent %d more pictures", now-sent)
	}
}

// The service can reach this device's management interface, and nothing else.
func TestTheServiceReachesOnlyTheManagementInterface(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, stub.Credential)

	// What this device offers: one route, so the test is about which streams
	// are opened rather than about routing.
	offered := http.NewServeMux()
	offered.HandleFunc("/healthz", func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("ok"))
	})

	reporter := New(store,
		func(context.Context) ([]byte, string, error) {
			return []byte("a picture"), "image/jpeg", nil
		}, nil).WithManagement(offered)
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the device to attach", func() bool {
		return reporter.State().Attached
	})

	// The management interface, by its reserved name.
	request, _ := http.NewRequest(http.MethodGet, "http://device/healthz", nil)
	response, err := stub.Ask(managementHost, managementPort, request)
	if err != nil {
		t.Fatalf("the service could not reach the management interface: %s", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<10))
	if response.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Errorf("the device answered %d %q", response.StatusCode, body)
	}

	// Anything else on the device's own network is refused, with a sentence
	// rather than a silence -- an unanswered open tells the far end nothing
	// and makes it wait to learn it.
	for _, refused := range []struct {
		host string
		port int
	}{
		{"127.0.0.1", 8080},
		{"192.0.2.10", 22}, // documentation range: some other host on the device's network
		{"device", 8080},
		{"cue", 80},
	} {
		asking, _ := http.NewRequest(http.MethodGet, "http://elsewhere/", nil)
		if _, err := stub.Ask(refused.host, refused.port, asking); err == nil {
			t.Errorf("the device opened a stream to %s:%d", refused.host, refused.port)
		} else if !strings.Contains(err.Error(), "management interface") {
			t.Errorf("refusing %s:%d said %q, which does not say why", refused.host, refused.port, err)
		}
	}
}

// A device that has been given nothing to offer refuses rather than hangs.
func TestADeviceOfferingNothingSaysSo(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, stub.Credential)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	}, nil)
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the device to attach", func() bool {
		return reporter.State().Attached
	})

	request, _ := http.NewRequest(http.MethodGet, "http://device/healthz", nil)
	if _, err := stub.Ask(managementHost, managementPort, request); err == nil {
		t.Error("a device with nothing to offer opened a stream anyway")
	} else if !strings.Contains(err.Error(), "nothing to offer") {
		t.Errorf("it refused with %q, which does not say why", err)
	}
}

// The service can be spliced straight to this device's screen, with nothing
// between the two but the tunnel.
//
// What travels is whatever the VNC server says, byte for byte. There is no
// HTTP on this stream and no bridge on the device: reaching the device's own
// websocket bridge through the tunnel would put an upgrade in the middle of a
// stream that is already a tunnel.
func TestTheServiceCanBeSplicedToTheScreen(t *testing.T) {
	// Something that behaves like a VNC server: it greets, then echoes.
	screen, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = screen.Close() }()
	go func() {
		for {
			viewer, err := screen.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = viewer.Close() }()
				_, _ = viewer.Write([]byte("RFB 003.008\n"))
				_, _ = io.Copy(viewer, viewer)
			}()
		}
	}()

	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, stub.Credential)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	}, nil).WithScreen(func(ctx context.Context) (net.Conn, error) {
		return net.Dial("tcp", screen.Addr().String())
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the device to attach", func() bool {
		return reporter.State().Attached
	})

	spliced, err := stub.Splice(screenHost, screenPort)
	if err != nil {
		t.Fatalf("the service could not reach the screen: %s", err)
	}

	// The server's greeting arrives unchanged.
	greeting := make([]byte, len("RFB 003.008\n"))
	if _, err := io.ReadFull(spliced, greeting); err != nil {
		t.Fatalf("nothing came back from the screen: %s", err)
	}
	if string(greeting) != "RFB 003.008\n" {
		t.Errorf("the screen said %q", greeting)
	}

	// And bytes travel the other way, which is what makes it drivable rather
	// than only watchable.
	if _, err := spliced.Write([]byte("a pointer moved")); err != nil {
		t.Fatalf("could not send to the screen: %s", err)
	}
	echoed := make([]byte, len("a pointer moved"))
	if _, err := io.ReadFull(spliced, echoed); err != nil {
		t.Fatalf("the screen did not answer: %s", err)
	}
	if string(echoed) != "a pointer moved" {
		t.Errorf("the screen echoed %q", echoed)
	}
}

// A device with no screen to show says so, rather than opening a stream it
// will immediately close.
func TestADeviceWithNoScreenSaysSo(t *testing.T) {
	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, stub.Credential)

	offered := http.NewServeMux()
	offered.HandleFunc("/healthz", func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("ok"))
	})

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	}, nil).WithManagement(offered).WithScreen(func(context.Context) (net.Conn, error) {
		return nil, errors.New("this device is not running a VNC server, so there is no screen to watch")
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the device to attach", func() bool {
		return reporter.State().Attached
	})

	if _, err := stub.Splice(screenHost, screenPort); err == nil {
		t.Error("a device with no VNC server was spliced to a screen anyway")
	} else if !strings.Contains(err.Error(), "no screen") {
		t.Errorf("it refused with %q, which does not say why", err)
	}

	// And it is still managed, which is why the two are separate names.
	request, _ := http.NewRequest(http.MethodGet, "http://device/healthz", nil)
	response, err := stub.Ask(managementHost, managementPort, request)
	if err != nil {
		t.Fatalf("refusing the screen also refused management: %s", err)
	}
	_ = response.Body.Close()
}

// A frame the size the service actually sends must not close the connection.
//
// The read limit was exactly one megabyte and the service writes one-megabyte
// frames, so a full frame plus the two bytes and the stream identifier in
// front of it was over the limit. The first large upload would have dropped
// the tunnel with "message too big" -- the same failure as sizing writes from
// somebody else's limit, pointing the other way.
//
// Written twice. The first version sent a large body through an HTTP request
// and passed against the old limit, because Go's server writes a body through
// a four-kilobyte buffer and the stub never produced a frame anywhere near
// the size in question. It proved that a large body works and said nothing
// about frames. This one writes one frame of exactly the size the service
// uses, which is the thing that was broken.
func TestAFullSizedFrameFromTheServiceDoesNotCloseTheConnection(t *testing.T) {
	// Somewhere for the bytes to go: the raw path takes whatever it is given
	// without an HTTP server buffering it on the way.
	sink, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close() }()
	arrived := make(chan int, 1)
	go func() {
		connection, err := sink.Accept()
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		// Read a known length rather than to the end: the stream stays open
		// after the write, so waiting for EOF would wait for ever and report
		// a delivery failure that had not happened.
		into := make([]byte, 1<<20)
		read, _ := io.ReadFull(connection, into)
		arrived <- read
	}()

	stub := newStubService(t)
	store := newStore(t, stub.Server.URL, stub.Credential)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	}, nil).WithScreen(func(context.Context) (net.Conn, error) {
		return net.Dial("tcp", sink.Addr().String())
	})
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())
	waitFor(t, 10*time.Second, "the device to attach", func() bool {
		return reporter.State().Attached
	})

	spliced, err := stub.Splice(screenHost, screenPort)
	if err != nil {
		t.Fatalf("could not open a stream: %s", err)
	}

	// One write, one frame: a megabyte of payload, plus the framing in front
	// of it, which is what put it over a limit set at exactly a megabyte.
	large := bytes.Repeat([]byte("x"), 1<<20)
	if _, err := spliced.Write(large); err != nil {
		t.Fatalf("writing a full-sized frame failed: %s", err)
	}
	_ = spliced.Close()

	select {
	case got := <-arrived:
		if got != len(large) {
			t.Errorf("%d bytes of %d arrived", got, len(large))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the frame never arrived, which is what the old limit did to it")
	}

	// And the connection is still up, which "message too big" would have
	// taken away along with every other stream on it.
	if !reporter.State().Attached {
		t.Error("the connection did not survive a full-sized frame")
	}
}

// A device that has just been linked reports at once.
//
// The loop used to ask whether it was linked yet on the same thirty-second
// timer it reported on, so a device could sit silent for half a minute after
// being linked -- with somebody watching a dashboard that stayed empty,
// wondering whether it had worked. That is the one moment somebody is
// certainly looking.
func TestLinkingIsNoticedAtOnce(t *testing.T) {
	stub := newStubService(t)

	// Not linked to begin with: no credential.
	store := newStore(t, stub.Server.URL, "")

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("a picture"), "image/jpeg", nil
	}, nil)
	defer func() { _ = reporter.Close() }()

	reporter.Start(context.Background())

	// Give it time to settle into waiting, so this measures being woken
	// rather than catching it before it slept.
	time.Sleep(300 * time.Millisecond)
	if stub.Attaches.Load() != 0 {
		t.Fatal("a device with no credential attached anyway")
	}

	// Somebody links it. This is what the linker does when an attempt
	// completes: it writes the credential to the configuration.
	linkedAt := time.Now()
	if err := store.Update(func(updated *config.Configuration) error {
		updated.Service.Secret = config.Secret(stub.Credential)
		updated.Service.Account = "somebody@example.com"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 10*time.Second, "the first picture", func() bool {
		return stub.screenshots.Load() > 0
	})
	took := time.Since(linkedAt)
	t.Logf("first picture %s after linking", took.Round(time.Millisecond))

	// Comfortably inside the thirty seconds the timer would have cost, and
	// loose enough not to fail on a slow machine.
	if took > 5*time.Second {
		t.Errorf("the first picture took %s; the device is waiting for a timer "+
			"rather than noticing it was linked", took)
	}
}

// The device asks for its profile over the same tunnel it reports on, and what
// the service says becomes the device's own configuration.
func TestAProfileFromTheServiceReachesTheConfiguration(t *testing.T) {
	stub := newStubService(t)
	stub.serves(`{"browser":{"darkMode":true}}`, `"one"`)

	store := newStore(t, stub.Server.URL, stub.Credential)
	store.Current().Browser.DarkMode = false

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("bytes"), "image/jpeg", nil
	}, nil)
	defer func() { _ = reporter.Close() }()
	reporter.Start(context.Background())

	waitFor(t, 10*time.Second, "the profile to be applied", func() bool {
		return store.Current().Browser.DarkMode
	})

	keys := store.Current().Service.ProfileKeys
	if len(keys) != 1 || keys[0] != "browser.darkMode" {
		t.Errorf("recorded %v as taken from the profile", keys)
	}
}

// The common answer is 304, which is what makes a short interval affordable.
// Asking without the version it already has would mean a device fetching and
// re-applying the same document every minute for ever.
func TestTheDeviceAsksWithTheVersionItAlreadyHas(t *testing.T) {
	stub := newStubService(t)
	stub.serves(`{"browser":{"darkMode":false}}`, `"one"`)

	store := newStore(t, stub.Server.URL, stub.Credential)
	store.Current().Service.PollInterval = config.Duration(shortestPoll)

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("bytes"), "image/jpeg", nil
	}, nil)
	defer func() { _ = reporter.Close() }()
	reporter.Start(context.Background())

	waitFor(t, 10*time.Second, "the profile to be applied", func() bool {
		return !store.Current().Browser.DarkMode
	})

	// Nudged rather than waited for, so this does not depend on an interval.
	reporter.PollNow()
	waitFor(t, 10*time.Second, "a second ask", func() bool {
		return stub.profiles.Load() >= 2
	})

	stub.mutex.Lock()
	asked := stub.lastAsked
	stub.mutex.Unlock()
	if asked != `"one"` {
		t.Errorf("asked with If-None-Match %q; it should send the version it applied", asked)
	}
}

// A refused profile changes nothing. This is a successful HTTP conversation
// rather than a failed request, so it does not fall out of "a failed poll
// changes nothing" on its own: without this, revoking a device -- or any
// moment where the tunnel has no device on its context -- would silently
// revert every managed setting on that screen.
func TestARefusedProfileReleasesNothing(t *testing.T) {
	stub := newStubService(t)
	// darkMode false, because the default is true: a profile that agreed with
	// the default would be applied and prove nothing, and the wait below would
	// pass before anything had happened.
	stub.serves(`{"browser":{"darkMode":false}}`, `"one"`)

	store := newStore(t, stub.Server.URL, stub.Credential)
	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("bytes"), "image/jpeg", nil
	}, nil)
	defer func() { _ = reporter.Close() }()
	reporter.Start(context.Background())

	waitFor(t, 10*time.Second, "the profile to be applied", func() bool {
		return len(store.Current().Service.ProfileKeys) == 1
	})

	stub.refuses(http.StatusUnauthorized)
	before := stub.profiles.Load()
	reporter.PollNow()
	waitFor(t, 10*time.Second, "the refused ask", func() bool {
		return stub.profiles.Load() > before
	})

	if store.Current().Browser.DarkMode {
		t.Error("a 401 released a managed setting; only an empty document may do that")
	}
	if len(store.Current().Service.ProfileKeys) != 1 {
		t.Errorf("a 401 changed what this device claims to be managed by: %v",
			store.Current().Service.ProfileKeys)
	}
}

// An empty document is how a device is un-managed, and it has to be told
// apart from every kind of not being told anything.
func TestAnEmptyDocumentReleasesWhatTheProfileGave(t *testing.T) {
	stub := newStubService(t)
	stub.serves(`{"browser":{"darkMode":false}}`, `"one"`)

	store := newStore(t, stub.Server.URL, stub.Credential)
	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("bytes"), "image/jpeg", nil
	}, nil)
	defer func() { _ = reporter.Close() }()
	reporter.Start(context.Background())

	waitFor(t, 10*time.Second, "the profile to be applied", func() bool {
		return !store.Current().Browser.DarkMode
	})

	stub.serves(`{}`, `"two"`)
	reporter.PollNow()

	waitFor(t, 10*time.Second, "the setting to be released", func() bool {
		return store.Current().Browser.DarkMode == config.Default().Browser.DarkMode &&
			len(store.Current().Service.ProfileKeys) == 0
	})
}

// The one that would have wiped every screen in service. A device with no
// playlist assigned is answered 204, and its own items -- set up on the device,
// by somebody standing at it -- must survive that untouched. Getting this wrong
// is not a bug on one screen; it is every unmanaged screen going blank the
// first time it polls.
func TestNoPlaylistAssignedLeavesTheDevicesOwnItemsAlone(t *testing.T) {
	stub := newStubService(t)
	// The stub answers 204 by default, which is what an unassigned device gets.

	store := newStore(t, stub.Server.URL, stub.Credential)
	mine := []config.Item{{Identifier: "mine", URL: "https://example.com/local"}}
	store.Current().Playlist.Items = mine

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("bytes"), "image/jpeg", nil
	}, nil)
	defer func() { _ = reporter.Close() }()
	reporter.Start(context.Background())

	waitFor(t, 10*time.Second, "the device to ask for a playlist", func() bool {
		return stub.playlists.Load() > 0
	})

	items := store.Current().Playlist.Items
	if len(items) != 1 || items[0].Identifier != "mine" {
		t.Fatalf("a device with no playlist assigned lost its own items: %v", items)
	}
}

// An assigned playlist replaces what the screen shows, identifier and all.
func TestAnAssignedPlaylistReplacesTheItems(t *testing.T) {
	stub := newStubService(t)
	stub.showsPlaylist(`{"interval":45,"items":[
		{"identifier":"01aaa","url":"https://example.com/one","title":"One"},
		{"identifier":"01bbb","url":"https://example.com/two","duration":20}
	]}`, `"p1"`)

	store := newStore(t, stub.Server.URL, stub.Credential)
	store.Current().Playlist.Items = []config.Item{{Identifier: "mine", URL: "https://example.com/local"}}

	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("bytes"), "image/jpeg", nil
	}, nil)
	defer func() { _ = reporter.Close() }()
	reporter.Start(context.Background())

	waitFor(t, 10*time.Second, "the playlist to be applied", func() bool {
		return len(store.Current().Playlist.Items) == 2
	})

	items := store.Current().Playlist.Items
	if items[0].Identifier != "01aaa" || items[1].Identifier != "01bbb" {
		t.Errorf("identifiers are %q and %q; they must be carried across, because "+
			"browser tabs are keyed by them", items[0].Identifier, items[1].Identifier)
	}
	if items[1].Duration.Duration() != 20*time.Second {
		t.Errorf("the second item lasts %s; the document said 20 seconds", items[1].Duration.Duration())
	}
	if store.Current().Playlist.Interval.Duration() != 45*time.Second {
		t.Errorf("the interval is %s", store.Current().Playlist.Interval.Duration())
	}
}

// An item's media is stored under the digest rather than the service's own
// identifier, so the same bytes in several items or several playlists are one
// file on a disk that is not large.
func TestMediaIsCarriedByItsDigest(t *testing.T) {
	stub := newStubService(t)
	stub.showsPlaylist(`{"items":[{"identifier":"01aaa","media":{
		"file":"0123456789abcdef0123456789abcdef","mediaId":"01m1zzz","name":"promo.mp4","kind":"video","sound":true}}]}`, `"p1"`)

	store := newStore(t, stub.Server.URL, stub.Credential)
	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("bytes"), "image/jpeg", nil
	}, nil)
	defer func() { _ = reporter.Close() }()
	reporter.Start(context.Background())

	waitFor(t, 10*time.Second, "the playlist to be applied", func() bool {
		return len(store.Current().Playlist.Items) == 1
	})

	media := store.Current().Playlist.Items[0].Media
	if media == nil {
		t.Fatal("the item lost its media")
	}
	if media.File != "0123456789abcdef0123456789abcdef" {
		t.Errorf("stored under %q; it must be the digest", media.File)
	}
	if media.Kind != "video" || !media.Sound {
		t.Errorf("kind %q, sound %v; both change what the screen does", media.Kind, media.Sound)
	}
}

// A slide's login travels with its credential reference, so a dashboard behind
// a sign-in keeps signing in when a playlist is applied to it -- without the
// password having gone anywhere near the service.
func TestALoginTravelsWithItsCredentialName(t *testing.T) {
	stub := newStubService(t)
	stub.showsPlaylist(`{"items":[{"identifier":"01aaa","url":"https://example.com/",
		"login":{"whenUrlMatches":"/login","passwordSelector":"#password",
		"credential":"the-dashboard"}}]}`, `"p1"`)

	store := newStore(t, stub.Server.URL, stub.Credential)
	reporter := New(store, func(context.Context) ([]byte, string, error) {
		return []byte("bytes"), "image/jpeg", nil
	}, nil)
	defer func() { _ = reporter.Close() }()
	reporter.Start(context.Background())

	waitFor(t, 10*time.Second, "the playlist to be applied", func() bool {
		return len(store.Current().Playlist.Items) == 1
	})

	login := store.Current().Playlist.Items[0].Login
	if login == nil {
		t.Fatal("the item lost its login, so this screen will sit on a sign-in page")
	}
	if login.Credential != "the-dashboard" {
		t.Errorf("the credential name is %q", login.Credential)
	}
	if login.Password.IsSet() {
		t.Error("a password arrived from the service; it never should")
	}
}
