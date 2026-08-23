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

func TestParseReportsAnUnknownFieldWithoutRefusingTheFile(t *testing.T) {
	// Two requirements pull against each other here, and both are real.
	//
	// A mistyped key is otherwise indistinguishable from a setting that does
	// not work, and the operator only finds out because the screen is wrong —
	// so it cannot be ignored silently.
	//
	// But every device in service has the settings of the version that wrote
	// its file, including ones a later version has removed, and a daemon that
	// refuses to start over one of those turns an upgrade into a screen that
	// has gone black on a machine nobody can reach — so it cannot be fatal.
	//
	// It is therefore recorded and carried, and the interface shows it.
	configuration, err := Parse([]byte("device:\n  nmae: lobby\n  name: Lobby\n"))
	if err != nil {
		t.Fatalf("a mistyped key stopped the daemon: %s", err)
	}
	if len(configuration.IgnoredSettings) != 1 {
		t.Fatalf("the mistyped key was not recorded: %q", configuration.IgnoredSettings)
	}
	if !strings.Contains(configuration.IgnoredSettings[0], "nmae") {
		t.Errorf("what was recorded does not name the field: %q", configuration.IgnoredSettings[0])
	}
	if configuration.Device.Name != "Lobby" {
		t.Errorf("the rest of the file was not read: %q", configuration.Device.Name)
	}
}

func TestAFileWithNothingWrongWithItRecordsNothing(t *testing.T) {
	configuration, err := Parse([]byte("device:\n  name: Lobby\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(configuration.IgnoredSettings) != 0 {
		t.Errorf("a correct file recorded %q", configuration.IgnoredSettings)
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

func TestASettingThatNoLongerExistsDoesNotStopTheDaemon(t *testing.T) {
	// Every device in service has its whole configuration written into its
	// file, including settings a later version has removed. A daemon that
	// refuses to start over one of them turns an upgrade into a screen that
	// has gone black on a machine nobody can reach.
	//
	// browser.debuggingPort is the real case: it was removed because having
	// it at all was the bug.
	configuration, err := Parse([]byte(`
device:
  name: Reception
browser:
  debuggingPort: 9222
  sandbox: false
playlist:
  items:
    - url: https://example.com/
`))
	if err != nil {
		t.Fatalf("a file with a setting that no longer exists was refused: %s", err)
	}

	// And the rest of the file was still read — not abandoned at the point
	// the unknown setting appeared.
	if configuration.Browser.Sandbox {
		t.Error("browser.sandbox came after the unknown setting and was not read")
	}
	if len(configuration.Playlist.Items) != 1 {
		t.Fatalf("the playlist came after the unknown setting and was not read: %d items",
			len(configuration.Playlist.Items))
	}
	if configuration.Device.Name != "Reception" {
		t.Errorf("the device name was not read: %q", configuration.Device.Name)
	}
}

func TestAValueOfTheWrongKindIsStillRefused(t *testing.T) {
	// Tolerating a name this version does not have must not become tolerating
	// anything at all.
	_, err := Parse([]byte("watchdog:\n  interval: [1, 2, 3]\n"))
	if err == nil {
		t.Fatal("a duration written as a list was accepted")
	}
	if strings.TrimSpace(err.Error()) == "config: yaml: unmarshal errors:" {
		t.Errorf("the error says nothing about what was wrong: %q", err)
	}
}

func TestTheErrorSaysWhichSettingWasWrong(t *testing.T) {
	// go-yaml puts every problem in one error whose Error() is a heading
	// followed by indented lines, and %w prints the heading alone — which is
	// how a device logged "yaml: unmarshal errors:" ten times and said nothing
	// at all about its configuration.
	_, err := Parse([]byte("web:\n  listen: [not, a, string]\n"))
	if err == nil {
		t.Fatal("expected an error")
	}
	// go-yaml does not name the field for a scalar of the wrong kind, so what
	// is required here is the line and what was wrong with it — enough for
	// somebody with the file in front of them.
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("the error does not say where the problem is: %q", err)
	}
	if !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("the error does not say what the problem is: %q", err)
	}
}
