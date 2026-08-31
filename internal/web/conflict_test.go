package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// Two people editing a device's configuration must not silently overwrite each
// other.
//
// This was already true of two browser tabs before anything else could
// configure a device: the last save won and the other was gone with nobody
// told. A device that can also be configured from the service it is linked to
// has more ways to do it, not a new problem.
func TestASaveAgainstAStaleConfigurationIsRefused(t *testing.T) {
	server := newTestServer(t, config.Default())
	defer func() { _ = server.device.Linker().Close() }()
	session := setUp(t, server)

	// Two people open the page and are looking at the same thing.
	first := do(server, http.MethodGet, "/api/v1/configuration", nil, session)
	version := first.Header().Get("ETag")
	if version == "" {
		t.Fatal("the configuration is served without a version, so nobody can say what they edited")
	}
	var theirs map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &theirs); err != nil {
		t.Fatal(err)
	}
	var mine map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &mine); err != nil {
		t.Fatal(err)
	}

	// One of them saves.
	device, _ := theirs["device"].(map[string]any)
	device["location"] = "the front desk"
	saved := doWith(server, http.MethodPut, "/api/v1/configuration", theirs, session,
		map[string]string{"If-Match": version})
	if saved.Code != http.StatusOK {
		t.Fatalf("the first save answered %d: %s", saved.Code, saved.Body)
	}
	if after := saved.Header().Get("ETag"); after == "" || after == version {
		t.Errorf("saving did not change the version: %q then %q", version, after)
	}

	// The other saves what they were looking at, which is now out of date.
	device, _ = mine["device"].(map[string]any)
	device["location"] = "the loading bay"
	refused := doWith(server, http.MethodPut, "/api/v1/configuration", mine, session,
		map[string]string{"If-Match": version})
	if refused.Code != http.StatusConflict {
		t.Fatalf("a save against a stale configuration answered %d, want 409", refused.Code)
	}

	// The refusal carries what is actually there, so whoever asked can show
	// what changed rather than saying "try again".
	var told struct {
		Error         string               `json:"error"`
		Configuration config.Configuration `json:"configuration"`
	}
	if err := json.Unmarshal(refused.Body.Bytes(), &told); err != nil {
		t.Fatalf("the refusal is not readable: %s", err)
	}
	if told.Error == "" {
		t.Error("the refusal does not say what happened")
	}
	if told.Configuration.Device.Location != "the front desk" {
		t.Errorf("the refusal carries %q, not what is on the device",
			told.Configuration.Device.Location)
	}

	// And the first save stands: the second did not land.
	if now := server.store.Current().Device.Location; now != "the front desk" {
		t.Errorf("the device's location is %q; the stale save overwrote the fresh one", now)
	}
}

// A caller that says nothing about what it was editing is still served, so
// somebody with curl and a document they just fetched does not have to learn
// about versions to change a setting.
func TestASaveWithoutAVersionStillWorks(t *testing.T) {
	server := newTestServer(t, config.Default())
	defer func() { _ = server.device.Linker().Close() }()
	session := setUp(t, server)

	current := do(server, http.MethodGet, "/api/v1/configuration", nil, session)
	var document map[string]any
	if err := json.Unmarshal(current.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	device, _ := document["device"].(map[string]any)
	device["location"] = "somewhere"

	if code := do(server, http.MethodPut, "/api/v1/configuration", document, session).Code; code != http.StatusOK {
		t.Errorf("a save with no version answered %d", code)
	}
}

// Changing only a password must still be a change.
//
// The version was a hash of the JSON, and JSON renders every secret as the
// same placeholder -- so two configurations differing only in a password were
// byte-identical and shared a version. A second tab could then save a stale
// document, the version would match, no conflict would be reported, and the
// password change would be gone. That is the case conditional writes exist for
// and the one where losing an edit costs the most.
func TestChangingOnlyASecretIsSeenAsAChange(t *testing.T) {
	// Set to begin with, not empty. An empty secret and a set one differ in
	// the JSON as well -- one renders as "" and the other as the placeholder
	// -- so starting from empty would pass against the very bug this is
	// about. The invisible case is one password becoming another.
	configuration := config.Default()
	configuration.VNC.Password = config.Secret("the old password")
	server := newTestServer(t, configuration)
	defer func() { _ = server.device.Linker().Close() }()
	session := setUp(t, server)

	first := do(server, http.MethodGet, "/api/v1/configuration", nil, session)
	version := first.Header().Get("ETag")

	// Somebody changes the VNC password. It goes through the store rather than
	// the API because the API is never sent a real secret to send back.
	if err := server.store.Update(func(updated *config.Configuration) error {
		updated.VNC.Password = config.Secret("a new password")
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	after := do(server, http.MethodGet, "/api/v1/configuration", nil, session)
	if after.Header().Get("ETag") == version {
		t.Fatal("changing a password did not change the version, so a stale save would not be noticed")
	}

	// And a save against the old version is now refused rather than quietly
	// overwriting the new password.
	var document map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	refused := doWith(server, http.MethodPut, "/api/v1/configuration", document, session,
		map[string]string{"If-Match": version})
	if refused.Code != http.StatusConflict {
		t.Errorf("a save against the pre-password version answered %d, want 409", refused.Code)
	}
	if got := server.store.Current().VNC.Password.Reveal(); got != "a new password" {
		t.Errorf("the password is now %q; the stale save overwrote it", got)
	}
}
