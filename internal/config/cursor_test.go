package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTheCursorSettingStillAcceptsWhatItUsedToBe(t *testing.T) {
	// It was a boolean, and every device in service has one of those written
	// in its file. A setting that changes shape has to keep accepting what it
	// used to accept, or the upgrade is a daemon that will not start.
	for text, want := range map[string]CursorMode{
		"false":    CursorHidden,
		"true":     CursorAlways,
		"hidden":   CursorHidden,
		"auto":     CursorAuto,
		"always":   CursorAlways,
		"  Auto  ": CursorAuto,
		`""`:       CursorAuto,
	} {
		var mode CursorMode
		if err := yaml.Unmarshal([]byte(text), &mode); err != nil {
			t.Errorf("%s was refused: %s", text, err)
			continue
		}
		if mode != want {
			t.Errorf("%s read as %q, want %q", text, mode, want)
		}
	}
}

func TestSomethingElseEntirelyIsRefused(t *testing.T) {
	var mode CursorMode
	if err := yaml.Unmarshal([]byte("sometimes"), &mode); err == nil {
		t.Error("an unknown cursor mode was accepted")
	}
}

func TestTheFileIsRewrittenWithTheWordNotTheBoolean(t *testing.T) {
	// The interface rewrites this file. Writing "false" back would keep the
	// old spelling alive for ever.
	encoded, err := yaml.Marshal(CursorHidden)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded); got != "hidden\n" {
		t.Errorf("wrote %q, want %q", got, "hidden\n")
	}
}

func TestOnlyHiddenStartsTheServerWithoutACursor(t *testing.T) {
	// "auto" needs the server to have a cursor, because -nocursor cannot be
	// undone while it runs.
	if CursorHidden.ServerDrawsOne() {
		t.Error("hidden asked the server for a cursor")
	}
	if !CursorAuto.ServerDrawsOne() {
		t.Error("auto did not ask the server for a cursor, so there would be nothing to show")
	}
	if !CursorAlways.ServerDrawsOne() {
		t.Error("always did not ask the server for a cursor")
	}
}
