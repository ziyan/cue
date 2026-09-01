package config

import (
	"github.com/ziyan/cue/internal/util/security"
	"os"
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

func TestAConfigurationWithTheRemovedFleetSectionStillLoads(t *testing.T) {
	// Every device in service has a fleet section written into its file,
	// because the daemon wrote it there. The setting is gone; the files are
	// not, and a daemon that refused to start over one would turn the upgrade
	// into a screen that has gone black on a machine nobody can reach.
	configuration, err := Parse([]byte(`
device:
  name: Reception
fleet:
  enabled: true
  url: https://example.invalid
  enrollmentToken: a test placeholder token
browser:
  sandbox: false
`))
	if err != nil {
		t.Fatalf("a file with the removed fleet section was refused: %s", err)
	}
	if configuration.Browser.Sandbox {
		t.Error("the setting after the removed section was not read, so parsing stopped there")
	}
	if len(configuration.IgnoredSettings) == 0 {
		t.Error("the removed section was skipped without being reported anywhere")
	}
}

// A setting this version does not have is dropped from the file, and stops
// being reported once it is gone.
//
// The reporting half is what this is really pinning. Rewrite clears the list
// on the very configuration the caller is holding, so a caller that counts the
// list after calling it counts nothing and says so — which is how a device
// that had just had its obsolete "fleet" section removed announced that it had
// removed none.
func TestARemovedSettingIsTakenOutOfTheFileAndThenStopsBeingReported(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "cue.yaml")
	written := "device:\n  name: Reception\nfleet:\n  enabled: true\n"
	if err := os.WriteFile(filename, []byte(written), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Open(filename)
	if err != nil {
		t.Fatalf("a file with an unknown setting must still load: %s", err)
	}
	configuration := store.Current()
	if len(configuration.IgnoredSettings) != 1 {
		t.Fatalf("ignored %d setting(s), want 1: %v",
			len(configuration.IgnoredSettings), configuration.IgnoredSettings)
	}
	// The rest of the file is read, not abandoned at the unknown name.
	if configuration.Device.Name != "Reception" {
		t.Errorf("the device is named %q, want %q", configuration.Device.Name, "Reception")
	}

	// Counting has to happen here, before the rewrite empties it.
	counted := len(configuration.IgnoredSettings)

	if err := store.Rewrite(); err != nil {
		t.Fatalf("rewriting: %s", err)
	}
	if counted != 1 {
		t.Errorf("counted %d setting(s) before the rewrite, want 1", counted)
	}
	if len(configuration.IgnoredSettings) != 0 {
		t.Errorf("the list still has %d setting(s) after the rewrite; a caller "+
			"counting it here is the bug this test exists for",
			len(configuration.IgnoredSettings))
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "fleet") {
		t.Errorf("the rewritten file still mentions fleet:\n%s", content)
	}

	// And it loads clean the next time, which is the point of tidying it.
	again, err := Open(filename)
	if err != nil {
		t.Fatalf("reopening the tidied file: %s", err)
	}
	if got := len(again.Current().IgnoredSettings); got != 0 {
		t.Errorf("the tidied file still reports %d ignored setting(s)", got)
	}
	if name := again.Current().Device.Name; name != "Reception" {
		t.Errorf("the tidied file names the device %q, want %q", name, "Reception")
	}
}

// An item written under the old "video:" key must keep working. Dropping
// somebody's content because a field was renamed is the failure this
// repository keeps finding, and it costs ten lines to avoid.
func TestAnItemWrittenUnderTheOldNameKeepsItsFile(t *testing.T) {
	written := "playlist:\n" +
		"  items:\n" +
		"    - identifier: promo\n" +
		"      video:\n" +
		"        file: 0123456789abcdef0123456789abcdef\n" +
		"        name: promo.mp4\n" +
		"        sound: true\n"

	configuration, err := Parse([]byte(written))
	if err != nil {
		t.Fatalf("a file written by an older version must still load: %s", err)
	}
	if len(configuration.Playlist.Items) != 1 {
		t.Fatalf("the item was dropped: %+v", configuration.Playlist.Items)
	}

	item := configuration.Playlist.Items[0]
	if item.Media == nil {
		t.Fatal("the item lost its file")
	}
	if item.Media.File != "0123456789abcdef0123456789abcdef" || item.Media.Name != "promo.mp4" {
		t.Errorf("the file came across as %+v", item.Media)
	}
	if !item.Media.Sound {
		t.Error("the item lost its sound setting")
	}
	// Everything written under the old name was a video; there were no
	// pictures then.
	if item.Media.Kind != "video" {
		t.Errorf("the item came across as kind %q, want video", item.Media.Kind)
	}
	// And the old field is cleared, so it is never written back.
	if item.Video != nil {
		t.Error("the old field survived, so it would be written back out again")
	}
}

// The upload limit was called something else. A file written by an older
// version must keep the number somebody chose, not silently fall back to a
// default that might be smaller than the videos they already use.
func TestTheUploadLimitFromAnOlderVersionIsKept(t *testing.T) {
	configuration, err := Parse([]byte("playlist:\n  maximumVideoSize: 12345678\n"))
	if err != nil {
		t.Fatalf("a file written by an older version must still load: %s", err)
	}
	if configuration.Playlist.MaximumUploadSize != 12345678 {
		t.Errorf("the limit came across as %d, want 12345678",
			configuration.Playlist.MaximumUploadSize)
	}
	if configuration.Playlist.MaximumVideoSize != 0 {
		t.Error("the old field survived, so it would be written back out again")
	}
}

// A file that sets both keeps the one this version writes.
func TestTheCurrentUploadLimitWinsOverTheOldOne(t *testing.T) {
	configuration, err := Parse([]byte(
		"playlist:\n  maximumVideoSize: 111\n  maximumUploadSize: 222\n"))
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Playlist.MaximumUploadSize != 222 {
		t.Errorf("the limit is %d, want the current setting 222",
			configuration.Playlist.MaximumUploadSize)
	}
}

// The example beside the real file has to describe the version running, and
// has to do nothing at all if somebody copies it into place by mistake.
func TestTheExampleIsEntirelyCommentedOut(t *testing.T) {
	content, err := Example()
	if err != nil {
		t.Fatal(err)
	}

	for number, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		t.Errorf("line %d is not commented out: %q", number+1, line)
	}

	// It is only useful if it actually lists things.
	for _, wanted := range []string{"device:", "playlist:", "network:", "browser:", "watchdog:"} {
		if !strings.Contains(string(content), wanted) {
			t.Errorf("the example does not mention %q", wanted)
		}
	}
}

// Read as a configuration, the example must be empty rather than a second
// opinion about what the settings are.
func TestTheExampleParsesAsNothing(t *testing.T) {
	content, err := Example()
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := Parse(content)
	if err != nil {
		t.Fatalf("the example does not parse: %s", err)
	}
	if configuration.Device.Name != Default().Device.Name {
		t.Error("the example carries settings of its own")
	}
}

// Showing a device identifier would invite somebody to copy it, and two
// devices calling themselves the same thing is a confusing thing to chase.
func TestTheExampleCarriesNoIdentifier(t *testing.T) {
	content, err := Example()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `identifier: ""`) {
		t.Error("the example shows an identifier, which somebody will copy")
	}
}

// A device carrying the old sixteen-character identifier is given a ULID.
//
// The service takes a device's identifier as its own name for the device and
// refuses anything that is not a ULID, so an old one could not link at all --
// and would fail at the far end with nothing on this side to explain it.
func TestAnOldDeviceIdentifierIsReplaced(t *testing.T) {
	configuration := Default()
	configuration.Device.Identifier = "t6ny2v00xad86aj0"
	configuration.Normalize()

	if configuration.Device.Identifier == "t6ny2v00xad86aj0" {
		t.Fatal("the old identifier was kept, so this device could not link")
	}
	if !security.IsDeviceIdentifier(configuration.Device.Identifier) {
		t.Errorf("it became %q, which the service would refuse too",
			configuration.Device.Identifier)
	}
}

// And one that is already a ULID is left exactly alone. An identifier that
// changed on every save would be a device that looked like a different one
// each time anybody wrote to its file.
func TestADeviceIdentifierIsGeneratedOnlyOnce(t *testing.T) {
	configuration := Default()
	configuration.Normalize()
	first := configuration.Device.Identifier
	if first == "" {
		t.Fatal("a device with no identifier was not given one")
	}

	for attempt := 0; attempt < 5; attempt++ {
		configuration.Normalize()
		if configuration.Device.Identifier != first {
			t.Fatalf("normalising again changed %q to %q", first, configuration.Device.Identifier)
		}
	}
}

// Two screens flashed from one disk come apart rather than becoming one device
// on the service, which takes the identifier as its own name.
func TestClonedDevicesStopSharingAnIdentifier(t *testing.T) {
	// The same old file, on two machines.
	first, err := Parse([]byte("device:\n  identifier: t6ny2v00xad86aj0\n  name: carbon\n"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse([]byte("device:\n  identifier: t6ny2v00xad86aj0\n  name: carbon\n"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Device.Identifier == second.Device.Identifier {
		t.Errorf("both clones came up as %q, so they would fight over one device",
			first.Device.Identifier)
	}
}

// A device that has been running since before identifiers were written in
// lower case keeps its identifier, in the new case. Keeping it is the point:
// minting a new one would be a new screen as far as the service is concerned,
// and the whole reason the identifier is the service's primary key is that a
// screen relinked is the same screen.
func TestAnUpperCaseIdentifierIsKeptAndLowered(t *testing.T) {
	configuration := Default()
	configuration.Device.Identifier = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	configuration.Normalize()

	if configuration.Device.Identifier != "01arz3ndektsv4rrffq69g5fav" {
		t.Errorf("the identifier became %q; it should be the same one in lower case",
			configuration.Device.Identifier)
	}
}
