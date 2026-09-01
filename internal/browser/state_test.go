package browser

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

func TestAChangedCommandLineNeedsARestart(t *testing.T) {
	// Every one of these is fixed when Chromium starts, so a change to it
	// that does not restart the browser is a setting the operator saved,
	// which the interface then shows as saved, and which is not in force.
	for name, change := range map[string]func(*config.Browser){
		"the dark mode":          func(browser *config.Browser) { browser.DarkMode = !browser.DarkMode },
		"the sandbox":            func(browser *config.Browser) { browser.Sandbox = !browser.Sandbox },
		"the certificate errors": func(browser *config.Browser) { browser.IgnoreCertificateErrors = true },
		"an extra argument": func(browser *config.Browser) {
			browser.ExtraArguments = []string{"--enable-features=WebContentsForceDark"}
		},
		"a different extra argument": func(browser *config.Browser) {
			browser.ExtraArguments = []string{"--something-else"}
		},
		"a certificate": func(browser *config.Browser) {
			browser.CertificateAuthorities = []string{"-----BEGIN CERTIFICATE-----"}
		},
	} {
		previous := config.Default()
		previous.Browser.ExtraArguments = []string{"--already-here"}
		updated := previous.Clone()
		change(&updated.Browser)

		if !restartNeeded(previous, updated) {
			t.Errorf("changing %s did not ask for a restart, so the change would not be in force", name)
		}
	}
}

func TestAChangeThatIsNotOnTheCommandLineDoesNotRestart(t *testing.T) {
	// Restarting blanks the screen for several seconds. An operator editing a
	// playlist should not see that.
	previous := config.Default()
	updated := previous.Clone()
	updated.Playlist.Items = append(updated.Playlist.Items, config.Item{URL: "https://example.com/"})

	if restartNeeded(previous, updated) {
		t.Error("adding a page to the playlist restarted the browser")
	}
}

// Changing the language restarts the browser, because Chromium reads it once.
//
// Without this the setting would be accepted, written to the file, applied to
// the menu, and do nothing at all to the pages on the screen until something
// else happened to restart the browser -- which on a wall display is a
// watchdog or a power cut. Accepted and silently ignored is the worst of the
// three possible answers.
func TestChangingTheLanguageRestartsTheBrowser(t *testing.T) {
	before := config.Default()
	before.Device.Language = "en"
	after := before.Clone()
	after.Device.Language = "ja"

	if !restartNeeded(before, after) {
		t.Error("the language changed and the browser was left running with the old one")
	}
	// And nothing needless: the same language does not blank the screen.
	if restartNeeded(before, before.Clone()) {
		t.Error("an unchanged configuration restarts the browser")
	}
}

// TestEveryBrowserSettingIsClassified is the guard on the list in
// restartNeeded. That list is written out field by field, and a list written
// out field by field falls behind the struct the moment somebody adds to it:
// audio.sink was on the browser's command line and missing from the list for
// long enough to ship, so setting it was accepted, saved, shown as saved, and
// never reached Chromium.
//
// So every field of the structs the command line is built from has to be one
// of two things, and this test fails until it is said which: it either needs a
// restart, or it is named below as taking effect without one. Adding a field
// and forgetting about it is what stops being possible.
func TestEveryBrowserSettingIsClassified(t *testing.T) {
	// Settings that reach the running browser without restarting it. Each one
	// needs a reason, because "no restart" is also how a setting that does
	// nothing at all looks.
	appliedWithoutARestart := map[string]string{
		// Read fresh every time a tab appears, by the code that closes it.
		"Browser.CloseUnexpectedTabs": "read live, on every unexpected tab",
	}

	for _, field := range fieldsOf(reflect.TypeOf(config.Browser{}), "Browser") {
		checkClassified(t, field, appliedWithoutARestart, func(configuration *config.Configuration) any {
			return &configuration.Browser
		})
	}
	for _, field := range fieldsOf(reflect.TypeOf(config.Log{}), "Log") {
		checkClassified(t, field, map[string]string{
			// Both are applied without touching the browser: the level is
			// global logging state, and how loudly the browser's own output
			// is logged is changed on the running supervisor.
			"Log.Level":         "global logging state, set on every change",
			"Log.BrowserOutput": "set on the running supervisor, so a browser being investigated is not restarted",
		}, func(configuration *config.Configuration) any {
			return &configuration.Log
		})
	}
	for _, field := range fieldsOf(reflect.TypeOf(config.Audio{}), "Audio") {
		checkClassified(t, field, map[string]string{
			// Neither of these is read by anything. They are in the file and
			// in the interface and they do nothing, which is worth knowing.
			"Audio.Volume": "not read by anything (see the configuration audit)",
			"Audio.Source": "not read by anything (see the configuration audit)",
		}, func(configuration *config.Configuration) any {
			return &configuration.Audio
		})
	}
}

func fieldsOf(structure reflect.Type, prefix string) []reflect.StructField {
	fields := make([]reflect.StructField, 0, structure.NumField())
	for index := range structure.NumField() {
		field := structure.Field(index)
		field.Name = prefix + "." + field.Name
		fields = append(fields, field)
	}
	return fields
}

// checkClassified changes one field and reports whether restartNeeded noticed.
func checkClassified(t *testing.T, field reflect.StructField, exempt map[string]string, section func(*config.Configuration) any) {
	t.Helper()

	previous := config.Default()
	updated := config.Default()

	name := field.Name
	target := reflect.ValueOf(section(updated)).Elem().FieldByName(name[strings.Index(name, ".")+1:])
	if !target.CanSet() {
		t.Fatalf("%s cannot be set", name)
	}
	change(t, name, target)

	restart := restartNeeded(previous, updated)
	if reason, ok := exempt[name]; ok {
		if restart {
			t.Errorf("%s is listed as applied without a restart (%s), but restartNeeded says it needs one", name, reason)
		}
		return
	}
	if !restart {
		t.Errorf("%s is a new setting that nothing applies: it is not in restartNeeded, "+
			"and it is not listed as taking effect without a restart. Add it to one or the other.", name)
	}
}

// change puts a different value in a field, whatever its type.
func change(t *testing.T, name string, target reflect.Value) {
	t.Helper()
	switch target.Kind() {
	case reflect.Bool:
		target.SetBool(!target.Bool())
	case reflect.String:
		target.SetString(target.String() + "-changed")
	case reflect.Int, reflect.Int64:
		target.SetInt(target.Int() + 7)
	case reflect.Float64:
		target.SetFloat(target.Float() + 0.5)
	case reflect.Slice:
		if target.Type().Elem().Kind() != reflect.String {
			t.Fatalf("%s: no way to change a %s", name, target.Type())
		}
		target.Set(reflect.ValueOf([]string{"changed"}))
	default:
		t.Fatalf("%s: no way to change a %s", name, target.Type())
	}
}
