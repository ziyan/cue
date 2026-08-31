package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// Nothing the service can reach carries a secret.
//
// A guard rather than a review: the list of what a linked device offers its
// service will grow, and the way this goes wrong is somebody adding a route
// that answers with more than they looked at. Every secret this device holds
// is set to something recognisable and then looked for in each answer.
func TestNothingTheServiceCanReachCarriesASecret(t *testing.T) {
	configuration := config.Default()
	configuration.VNC.Password = config.Secret("the vnc password")
	configuration.Service.Secret = config.Secret("the service credential")
	configuration.Web.PasswordHash = "$argon2id$the-password-hash"
	configuration.Web.SessionSecret = config.Secret("the session secret")
	configuration.Network.Interfaces = []config.Interface{{
		Name: "wlp4s0", Method: "dhcp",
		Wireless: &config.Wireless{SSID: "joe", Passphrase: config.Secret("the wifi password")},
	}}
	configuration.Playlist.Items = []config.Item{{
		Identifier: "one", URL: "https://example.com",
		Login: &config.Login{Username: "somebody", Password: config.Secret("the login password")},
	}}

	server := newTestServer(t, configuration)
	defer func() { _ = server.device.Linker().Close() }()
	handler := server.FromService()

	secrets := []string{
		"the vnc password", "the service credential", "$argon2id$the-password-hash",
		"the session secret", "the wifi password", "the login password",
	}

	for _, path := range []string{
		"/api/v1/configuration", "/api/v1/status", "/api/v1/network",
		"/api/v1/media", "/healthz",
	} {
		request := httptest.NewRequest(http.MethodGet, path, strings.NewReader(""))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		body := response.Body.String()

		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("%s carries %q", path, secret)
			}
		}
		// Hashes are not secrets exactly, and are still nobody else's
		// business: one is enough to attack offline at leisure.
		if found := regexp.MustCompile(`\$argon2[^"]*`).FindString(body); found != "" {
			t.Errorf("%s carries a password hash: %s", path, found)
		}
	}
}

// What is on the list is on it because somebody decided, and what is not gets
// a refusal that says so rather than a 404 somebody has to interpret.
func TestTheServiceIsRefusedWhatIsNotOnTheList(t *testing.T) {
	server := newTestServer(t, config.Default())
	defer func() { _ = server.device.Linker().Close() }()
	handler := server.FromService()

	for _, call := range []struct {
		method string
		path   string
	}{
		// Never: a service that could unlink a device could hand it to
		// somebody else, and the whole authority here comes from the link.
		{http.MethodPost, "/api/v1/link/forget"},
		{http.MethodPost, "/api/v1/link"},
		// Nor the password, nor setting the device up again.
		{http.MethodPost, "/api/v1/setup"},
		{http.MethodPost, "/api/v1/session"},
		// Nor the screen over VNC, which has a name of its own.
		{http.MethodGet, "/api/v1/vnc"},
	} {
		request := httptest.NewRequest(call.method, call.path, strings.NewReader(""))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s %s answered %d, want 404", call.method, call.path, response.Code)
		}
		if !strings.Contains(response.Body.String(), "does not offer") {
			t.Errorf("%s %s refused with %q, which does not say why",
				call.method, call.path, response.Body.String())
		}
	}
}

// The service cannot move a device to a different service.
//
// It can write the whole configuration, which is the parity that was asked
// for -- and the address it reports to is the one thing in that document that
// decides who it obeys. A service able to change it could hand a screen to
// somebody else and the device would go on working, reporting to a stranger,
// with nothing on it saying anything had happened.
//
// The address is restored on every write, whoever asks. This checks the
// service is included in "whoever".
func TestTheServiceCannotMoveTheDeviceElsewhere(t *testing.T) {
	server := newTestServer(t, config.Default())
	defer func() { _ = server.device.Linker().Close() }()
	handler := server.FromService()

	was := server.store.Current().Service.Address
	if was == "" {
		t.Fatal("the device has no service address, so this proves nothing")
	}

	current := do(server, http.MethodGet, "/api/v1/configuration", nil, setUp(t, server))
	var document map[string]any
	if err := json.Unmarshal(current.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	service, _ := document["service"].(map[string]any)
	service["address"] = "https://somewhere-else.example.com"

	body, _ := json.Marshal(document)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/configuration", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("the save answered %d: %s", response.Code, response.Body)
	}
	if now := server.store.Current().Service.Address; now != was {
		t.Errorf("the service moved this device from %q to %q", was, now)
	}
}
