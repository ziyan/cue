package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/link"
	"github.com/ziyan/cue/internal/service/servicetest"
)

// A stub of the service side, implementing the protocol as the plan describes
// it and nothing else.
//
// Written here rather than reused from internal/link on purpose: this one
// checks the pairing from the outside, the way a real service would, so it
// derives the ticket itself instead of calling the function the daemon uses.
// If those two ever disagree, this notices.
type stubService struct {
	server     *httptest.Server
	authorised atomic.Bool
	exchanges  atomic.Int64
	sawTicket  atomic.Value
}

func newStubService(t *testing.T) *stubService {
	t.Helper()
	stub := &stubService{}
	stub.sawTicket.Store("")
	// What the device reaches once it holds a credential, over the tunnel.
	// There is no public route for this any more: every device API but the
	// linking bootstrap goes through the websocket.
	overTheTunnel := http.NewServeMux()
	overTheTunnel.HandleFunc("/api/v1/device/self", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"id": "device-1", "name": "Reception", "userId": "user-1",
		})
	})

	public := http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/api/v1/device/link/exchange":
				stub.exchanges.Add(1)
				var asked struct {
					Ticket     string `json:"ticket"`
					Verifier   string `json:"verifier"`
					Name       string `json:"name"`
					Identifier string `json:"identifier"`
				}
				if err := json.NewDecoder(request.Body).Decode(&asked); err != nil {
					response.WriteHeader(http.StatusBadRequest)
					return
				}
				if asked.Verifier != "" {
					t.Errorf("the verifier was sent to the polling call")
				}
				stub.sawTicket.Store(asked.Ticket)
				if !stub.authorised.Load() {
					response.WriteHeader(http.StatusNoContent)
					return
				}
				response.WriteHeader(http.StatusAccepted)

			case "/api/v1/device/link/redeem":
				var asked struct {
					Ticket   string `json:"ticket"`
					Verifier string `json:"verifier"`
				}
				if err := json.NewDecoder(request.Body).Decode(&asked); err != nil {
					response.WriteHeader(http.StatusBadRequest)
					return
				}
				// The pairing checked the way the service checks it, from the
				// outside, rather than by calling the daemon's own function.
				sum := sha256.Sum256([]byte(asked.Verifier))
				if base64.RawURLEncoding.EncodeToString(sum[:]) != asked.Ticket {
					t.Errorf("the verifier does not hash to the ticket")
					response.WriteHeader(http.StatusForbidden)
					return
				}
				response.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(response).Encode(map[string]string{
					"secret":   "an example secret",
					"account":  "s•••@example.com",
					"deviceId": "device-1",
				})

			default:
				t.Errorf("the device asked for %q", request.URL.Path)
				http.NotFound(response, request)
			}
		})

	tunnel := servicetest.New(t, overTheTunnel, public)
	tunnel.Credential = "an example secret"
	stub.server = tunnel.Server
	return stub
}

// waitFor polls until the condition holds, so that nothing here depends on how
// long a goroutine takes to be scheduled.
func waitFor(t *testing.T, why string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

func linkStateOf(t *testing.T, server *Server, session *http.Cookie) link.State {
	t.Helper()
	response := do(server, http.MethodGet, "/api/v1/link", nil, session)
	if response.Code != http.StatusOK {
		t.Fatalf("asking about the link answered %d: %s", response.Code, response.Body)
	}
	var state link.State
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatalf("cannot read the link state: %s", err)
	}
	return state
}

// The whole journey through the interface a person actually uses: sign in,
// press link, watch a code appear, have somebody authorise it elsewhere, and
// find the device holding a credential.
//
// The pieces of this are tested in internal/link. What this one is for is the
// wiring between them -- the routes, the session, the picture, and the status
// the overview page reads -- which is where a working linker and a working
// page can still add up to nothing.
func TestLinkingThroughTheInterfaceEndToEnd(t *testing.T) {
	stub := newStubService(t)

	configuration := config.Default()
	configuration.Service.Address = stub.server.URL
	server := newTestServer(t, configuration)
	defer func() { _ = server.device.Linker().Close() }()
	session := setUp(t, server)

	// Nothing yet.
	if state := linkStateOf(t, server, session); state.Linked || state.Pending {
		t.Fatalf("a device that has never linked says %+v", state)
	}

	// Press the button.
	response := do(server, http.MethodPost, "/api/v1/link", nil, session)
	if response.Code != http.StatusOK {
		t.Fatalf("starting a link answered %d: %s", response.Code, response.Body)
	}

	state := linkStateOf(t, server, session)
	if !state.Pending || state.URL == "" {
		t.Fatalf("no code to scan: %+v", state)
	}
	if !strings.HasPrefix(state.URL, stub.server.URL+"/link/") {
		t.Errorf("the code opens %q, which is not a page on the service", state.URL)
	}
	if state.ExpiresAt == nil || !state.ExpiresAt.After(time.Now()) {
		t.Errorf("the attempt is not good for any length of time: %+v", state.ExpiresAt)
	}

	// The picture the page shows.
	picture := do(server, http.MethodGet, "/api/v1/link/code.svg", nil, session)
	if picture.Code != http.StatusOK {
		t.Fatalf("the code picture answered %d", picture.Code)
	}
	if kind := picture.Header().Get("Content-Type"); kind != "image/svg+xml" {
		t.Errorf("the code came back as %q", kind)
	}
	if kept := picture.Header().Get("Cache-Control"); kept != "no-store" {
		t.Errorf("the code is cacheable (%q), and a cached code no longer works", kept)
	}

	// The daemon is asking on its own, without anybody polling it.
	waitFor(t, "the device to ask the service", func() bool { return stub.exchanges.Load() > 0 })
	if ticket, _ := stub.sawTicket.Load().(string); !strings.Contains(state.URL, ticket) {
		t.Errorf("the ticket sent to the service is not the one in the code")
	}

	// Somebody presses authorise on their phone.
	stub.authorised.Store(true)

	waitFor(t, "the device to be linked", func() bool {
		return server.device.Linker().State().Linked
	})

	final := linkStateOf(t, server, session)
	if !final.Linked || final.Pending {
		t.Fatalf("after authorising, the state is %+v", final)
	}
	if final.Account != "s•••@example.com" {
		t.Errorf("the account is %q", final.Account)
	}

	// The credential reached the configuration, and the interface is not shown
	// it.
	saved := server.store.Current()
	if !saved.Service.IsLinked() {
		t.Error("nothing was saved")
	}
	if saved.Service.DeviceID != "device-1" {
		t.Errorf("the device identifier the service gave is %q", saved.Service.DeviceID)
	}
	shown := do(server, http.MethodGet, "/api/v1/configuration", nil, session).Body.String()
	if strings.Contains(shown, "an example secret") {
		t.Error("the credential is in what the interface is served")
	}

	// The overview reads it from the status, without a second request.
	status := do(server, http.MethodGet, "/api/v1/status", nil, session)
	var reported struct {
		Link link.State `json:"link"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &reported); err != nil {
		t.Fatalf("cannot read the status: %s", err)
	}
	if !reported.Link.Linked {
		t.Error("the status does not say the device is linked")
	}

	// And it can be given up again.
	if code := do(server, http.MethodPost, "/api/v1/link/forget", nil, session).Code; code != http.StatusOK {
		t.Fatalf("unlinking answered %d", code)
	}
	if after := linkStateOf(t, server, session); after.Linked {
		t.Error("the device is still linked after being unlinked")
	}
	if server.store.Current().Service.Address != stub.server.URL {
		t.Error("unlinking forgot the address as well as the credential")
	}
}

// None of it is reachable without a session. The code on the wall is enough to
// see, and a device is not given away to whoever can see it.
func TestLinkingFromTheInterfaceNeedsASession(t *testing.T) {
	stub := newStubService(t)
	configuration := config.Default()
	configuration.Service.Address = stub.server.URL
	server := newTestServer(t, configuration)
	defer func() { _ = server.device.Linker().Close() }()
	setUp(t, server)

	for _, call := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/link"},
		{http.MethodPost, "/api/v1/link"},
		{http.MethodDelete, "/api/v1/link"},
		{http.MethodPost, "/api/v1/link/forget"},
		{http.MethodGet, "/api/v1/link/code.svg"},
	} {
		if code := do(server, call.method, call.path, nil, nil).Code; code != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d without a session, want 401", call.method, call.path, code)
		}
	}
	if stub.exchanges.Load() != 0 {
		t.Error("a request with no session reached the service")
	}
}

// Cancelling leaves the device as it was, and stops it asking.
func TestAbandoningALinkStopsTheAsking(t *testing.T) {
	stub := newStubService(t)
	configuration := config.Default()
	configuration.Service.Address = stub.server.URL
	server := newTestServer(t, configuration)
	defer func() { _ = server.device.Linker().Close() }()
	session := setUp(t, server)

	if code := do(server, http.MethodPost, "/api/v1/link", nil, session).Code; code != http.StatusOK {
		t.Fatal("could not start a link")
	}
	waitFor(t, "the device to ask the service", func() bool { return stub.exchanges.Load() > 0 })

	if code := do(server, http.MethodDelete, "/api/v1/link", nil, session).Code; code != http.StatusOK {
		t.Fatal("could not abandon the link")
	}
	if state := linkStateOf(t, server, session); state.Pending {
		t.Error("a code is still being shown after cancelling")
	}
	// The picture goes with it, so a page left open cannot keep showing a code
	// that is no longer being watched for.
	if code := do(server, http.MethodGet, "/api/v1/link/code.svg", nil, session).Code; code != http.StatusNotFound {
		t.Errorf("the code picture answered %d after cancelling, want 404", code)
	}

	// Authorising it now must do nothing: the device has stopped asking.
	stub.authorised.Store(true)
	settled := stub.exchanges.Load()
	time.Sleep(300 * time.Millisecond)
	if stub.exchanges.Load() > settled {
		t.Error("the device is still asking the service about an abandoned attempt")
	}
	if server.device.Linker().State().Linked {
		t.Error("an abandoned attempt linked the device anyway")
	}
}

// Every device knows where to report, so the old "nowhere to link to" state is
// gone: an empty address in a file is filled in rather than meaning off.
//
// Nothing is given away by having an address. A device contacts the service
// only when somebody presses Link, so what makes a device private is being
// unlinked, not being unaddressed.
func TestADeviceAlwaysKnowsWhereToReport(t *testing.T) {
	server := newTestServer(t, config.Default())
	defer func() { _ = server.device.Linker().Close() }()
	session := setUp(t, server)

	if address := server.store.Current().Service.Address; address != config.DefaultServiceAddress {
		t.Errorf("a device with nothing in its file points at %q", address)
	}
	if state := linkStateOf(t, server, session); state.Linked || state.Pending {
		t.Errorf("a device that has never linked says %+v", state)
	}
}

// The picture has to be an SVG a browser will actually draw.
//
// It was not. The same helper draws the setup code inline in a page and this
// one on its own, and only the standalone case needs an XML namespace: inline,
// the HTML parser supplies it. So the setup code drew correctly while this one
// was a broken image on a phone, and no amount of looking at the setup page
// would have shown the difference.
//
// Checked by parsing it as XML, the way a browser loading an <img> does,
// rather than by looking at the bytes. The mistake that hid this the first
// time was reading naturalWidth of 0 as "an SVG has no intrinsic size" when it
// is also exactly what a browser reports for a picture it could not decode.
func TestTheLinkingCodeIsAnSVGABrowserWillDraw(t *testing.T) {
	stub := newStubService(t)
	configuration := config.Default()
	configuration.Service.Address = stub.server.URL
	server := newTestServer(t, configuration)
	defer func() { _ = server.device.Linker().Close() }()
	session := setUp(t, server)

	if code := do(server, http.MethodPost, "/api/v1/link", nil, session).Code; code != http.StatusOK {
		t.Fatal("could not start a link")
	}
	picture := do(server, http.MethodGet, "/api/v1/link/code.svg", nil, session)
	if picture.Code != http.StatusOK {
		t.Fatalf("the code picture answered %d", picture.Code)
	}

	var drawing struct {
		XMLName xml.Name `xml:"svg"`
		ViewBox string   `xml:"viewBox,attr"`
		Rects   []struct {
			Fill string `xml:"fill,attr"`
		} `xml:"rect"`
	}
	if err := xml.Unmarshal(picture.Body.Bytes(), &drawing); err != nil {
		t.Fatalf("the picture is not well-formed XML: %s", err)
	}
	// The namespace is the thing that was missing, and an XML parser is what
	// notices: without it the root element is "svg" in no namespace, which is
	// not an SVG.
	if drawing.XMLName.Space != "http://www.w3.org/2000/svg" {
		t.Errorf("the root element is in the namespace %q, so a browser will not draw it",
			drawing.XMLName.Space)
	}
	if drawing.ViewBox == "" {
		t.Error("the picture has no viewBox, so it has no size to be drawn at")
	}
	if len(drawing.Rects) < 2 {
		t.Errorf("the picture has %d rectangles, which is not a code", len(drawing.Rects))
	}
}

// The service address is the file's to decide, and the interface saves the
// whole document — so hiding the control is not enough on its own. A save that
// carries a different address must leave the device pointing where it pointed.
func TestTheInterfaceCannotChangeTheServiceAddress(t *testing.T) {
	server := newTestServer(t, config.Default())
	defer func() { _ = server.device.Linker().Close() }()
	session := setUp(t, server)

	was := server.store.Current().Service.Address
	if was != config.DefaultServiceAddress {
		t.Fatalf("a device with nothing in its file points at %q", was)
	}

	current := do(server, http.MethodGet, "/api/v1/configuration", nil, session)
	var document map[string]any
	if err := json.Unmarshal(current.Body.Bytes(), &document); err != nil {
		t.Fatalf("cannot read the configuration: %s", err)
	}
	service, _ := document["service"].(map[string]any)
	if service == nil {
		t.Fatal("the configuration has no service section")
	}
	service["address"] = "https://somewhere-else.example.com"

	saved := do(server, http.MethodPut, "/api/v1/configuration", document, session)
	if saved.Code != http.StatusOK {
		t.Fatalf("saving answered %d: %s", saved.Code, saved.Body)
	}
	if now := server.store.Current().Service.Address; now != was {
		t.Errorf("the interface moved the device from %q to %q", was, now)
	}

	// And the answer it gets back says so, rather than echoing what it sent.
	var returned map[string]any
	if err := json.Unmarshal(saved.Body.Bytes(), &returned); err != nil {
		t.Fatalf("cannot read what was saved: %s", err)
	}
	if section, _ := returned["service"].(map[string]any); section["address"] != was {
		t.Errorf("the interface was told the address is now %v", section["address"])
	}
}

// A device with nothing in its file still knows where to report.
func TestAServiceAddressIsFilledInRatherThanRequired(t *testing.T) {
	configuration := config.Default()
	if configuration.Service.Address != config.DefaultServiceAddress {
		t.Errorf("a default configuration points at %q", configuration.Service.Address)
	}

	// A file that names one keeps what it names.
	chosen := &config.Configuration{}
	chosen.Service.Address = "https://staging.example.com"
	chosen.Normalize()
	if chosen.Service.Address != "https://staging.example.com" {
		t.Errorf("a configured address became %q", chosen.Service.Address)
	}
}
