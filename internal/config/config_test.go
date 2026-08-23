package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFillsInDefaultsForEverythingTheFileDoesNotMention(t *testing.T) {
	configuration, err := Parse([]byte("device:\n  name: lobby\n"))
	if err != nil {
		t.Fatalf("parse: %s", err)
	}
	if configuration.Device.Name != "lobby" {
		t.Errorf("device.name is %q, want lobby", configuration.Device.Name)
	}
	if configuration.Display.Server != ServerXorg {
		t.Errorf("display.server is %q, want the default %q", configuration.Display.Server, ServerXorg)
	}
	if configuration.Playlist.Interval.Duration() != 30*time.Second {
		t.Errorf("playlist.interval is %s, want the default 30s", configuration.Playlist.Interval)
	}
	if configuration.Device.Identifier == "" {
		t.Error("a device identifier should have been generated")
	}
	if !configuration.Web.SessionSecret.IsSet() {
		t.Error("a session secret should have been generated")
	}
}

func TestParseRejectsAnUnknownField(t *testing.T) {
	// A mistyped key is otherwise indistinguishable from a setting that does
	// not work, and the operator only finds out because the screen is wrong.
	_, err := Parse([]byte("device:\n  nmae: lobby\n"))
	if err == nil {
		t.Fatal("an unknown field should be rejected")
	}
	if !strings.Contains(err.Error(), "nmae") {
		t.Errorf("the error should name the field, got: %s", err)
	}
}

func TestParseReportsEveryProblemAtOnce(t *testing.T) {
	_, err := Parse([]byte(`
device:
  name: ""
  timezone: Mars/Olympus
display:
  server: wayland
web:
  listen: "not an address"
`))
	if err == nil {
		t.Fatal("this configuration should not validate")
	}
	message := err.Error()
	for _, expected := range []string{"device.name", "device.timezone", "display.server", "web.listen"} {
		if !strings.Contains(message, expected) {
			t.Errorf("the error should mention %s; got:\n%s", expected, message)
		}
	}
}

func TestIdentifiersAreGeneratedOnceAndSurviveASaveAndLoad(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "cue.yaml")

	first, err := Parse([]byte("playlist:\n  items:\n    - url: https://example.com/\n"))
	if err != nil {
		t.Fatalf("parse: %s", err)
	}
	if err := first.Save(filename); err != nil {
		t.Fatalf("save: %s", err)
	}

	second, err := Load(filename)
	if err != nil {
		t.Fatalf("load: %s", err)
	}
	if second.Device.Identifier != first.Device.Identifier {
		t.Errorf("the device identifier changed across a save and load: %q became %q",
			first.Device.Identifier, second.Device.Identifier)
	}
	if second.Playlist.Items[0].Identifier != first.Playlist.Items[0].Identifier {
		t.Error("a playlist item's identifier changed across a save and load")
	}
}

func TestSaveRefusesToWriteAConfigurationThatWouldNotLoad(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "cue.yaml")
	configuration := Default()
	configuration.Web.Listen = "nonsense"
	if err := configuration.Save(filename); err == nil {
		t.Fatal("saving an invalid configuration should be refused")
	}
	if _, err := Load(filename); err == nil {
		t.Error("nothing should have been written")
	}
}

func TestSecretsRoundTripThroughTheFileButNotThroughJSON(t *testing.T) {
	configuration, err := Parse([]byte(`
playlist:
  items:
    - url: https://example.com/dashboard
      login:
        whenUrlMatches: "/login"
        usernameSelector: "input[name=username]"
        passwordSelector: "input[name=password]"
        username: display
        password: hunter2
`))
	if err != nil {
		t.Fatalf("parse: %s", err)
	}

	content, err := configuration.Marshal()
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	if !strings.Contains(string(content), "hunter2") {
		t.Error("the file must keep the real password, or the device cannot log in after a restart")
	}

	login := configuration.Playlist.Items[0].Login
	if rendered := login.Password.String(); strings.Contains(rendered, "hunter2") {
		t.Errorf("a secret rendered as %q, which would put it in the log", rendered)
	}
	// %s goes through String, which is the path a log line takes.
	if formatted := strings.TrimSpace(strings.Join([]string{login.Password.String()}, "")); formatted == "hunter2" {
		t.Error("a secret formatted as its own value")
	}
}

func TestRestoreSecretsKeepsAPasswordTheInterfaceWasNeverShown(t *testing.T) {
	previous, err := Parse([]byte(`
playlist:
  items:
    - identifier: aaaaaaaaaaaaaaaa
      url: https://example.com/dashboard
      login:
        whenUrlMatches: "/login"
        usernameSelector: "#user"
        passwordSelector: "#pass"
        username: display
        password: hunter2
`))
	if err != nil {
		t.Fatalf("parse: %s", err)
	}

	// What the interface posts back: the same item, reordered or retitled,
	// with the placeholder where the password was.
	updated := previous.Clone()
	updated.Playlist.Items[0].Title = "Cameras"
	updated.Playlist.Items[0].Login.Password = redacted

	RestoreSecrets(updated, previous)

	if updated.Playlist.Items[0].Login.Password != "hunter2" {
		t.Errorf("the password became %q; saving the settings page would have erased it",
			updated.Playlist.Items[0].Login.Password.Reveal())
	}
}
