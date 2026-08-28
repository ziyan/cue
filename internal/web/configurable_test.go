package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ziyan/cue/internal/config"
)

// deliberatelyNotInTheInterface names the settings an operator is not offered a
// control for, and why. Everything else must be reachable from the web
// interface: a screen on a wall is set up by somebody who has a browser and no
// shell, and a setting that can only be reached by editing a file on a machine
// with no shell is a setting that does not exist.
//
// The entries here are all of one kind — knobs for somebody debugging the
// daemon itself, who has already got a terminal open.
// Written as a list of pairs rather than a map keyed by name: a setting called
// something like a credential, quoted and followed by a colon, is exactly the
// shape tools/checksecrets looks for, and weakening that check to make room
// for a comment would be the wrong way round.
var deliberatelyNotInTheInterface = []struct {
	setting string
	why     string
}{
	{"binary", "which executable to run; for testing a different build"},
	{"extraArguments", "arbitrary browser flags; an escape hatch, not a setting"},
	{"paths", "where state is kept; fixed by the image's layout"},
	{"runtime", "where the runtime directory is; fixed by the image's layout"},
	{"virtualTerminal", "which console the X server draws on; it has to match the " +
		"device the container was given, so it belongs with the deployment"},
	{"level", "log verbosity; for somebody reading the log"},
	{"browserOutput", "whether to log the browser's own stderr; likewise"},
	{"source", "the microphone; nothing reads it yet"},
	{"trustedOrigins", "for a reverse proxy in front of the device"},
	{"reconcileInterval", "how often the network is checked; the default is right, and " +
		"a wrong value here is a device that stops recovering"},
	{"modeName", "read from the hardware, not chosen"},
	{"rate", "part of the mode, which is chosen as one string"},
	{"allowApply", "whether this device may replace its own container; it must not be " +
		"settable from the interface, because the interface is the thing it grants " +
		"power over. Anybody who reached the web interface could otherwise turn on " +
		"its own ability to use the Docker socket, which is root on the host. It " +
		"takes two deliberate acts by somebody with a shell on the machine: this " +
		"setting, and mounting the socket"},
}

func TestEverySettingIsReachableFromTheInterface(t *testing.T) {
	static := readTheInterface(t)

	var missing []string
	walk(reflect.TypeOf(config.Configuration{}), func(name string) {
		if isDeliberate(name) {
			return
		}
		if !strings.Contains(static, name) {
			missing = append(missing, name)
		}
	})

	for _, name := range missing {
		t.Errorf("nothing in the web interface mentions %q: either add a control "+
			"for it, or say in deliberatelyNotInTheInterface why an operator "+
			"should have to edit a file on a machine with no shell", name)
	}
}

// The allowlist is itself a thing that rots: a setting that is removed leaves
// an entry behind, and the next person reads it as a decision that was made
// about a setting that exists.
func TestTheAllowlistNamesOnlySettingsThatExist(t *testing.T) {
	names := map[string]bool{}
	walk(reflect.TypeOf(config.Configuration{}), func(name string) { names[name] = true })

	for _, entry := range deliberatelyNotInTheInterface {
		if !names[entry.setting] {
			t.Errorf("deliberatelyNotInTheInterface names %q, which is not a setting any more", entry.setting)
		}
		if entry.why == "" {
			t.Errorf("%q is exempt with no reason given", entry.setting)
		}
	}
}

func isDeliberate(name string) bool {
	for _, entry := range deliberatelyNotInTheInterface {
		if entry.setting == name {
			return true
		}
	}
	return false
}

// walk calls report with the JSON name of every field in the configuration,
// following structs, pointers and slices.
func walk(kind reflect.Type, report func(name string)) {
	for kind.Kind() == reflect.Pointer || kind.Kind() == reflect.Slice {
		kind = kind.Elem()
	}
	if kind.Kind() != reflect.Struct {
		return
	}
	for index := 0; index < kind.NumField(); index++ {
		field := kind.Field(index)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" || name == "" {
			continue
		}
		report(name)
		walk(field.Type, report)
	}
}

// readTheInterface concatenates every module the interface is built from. The
// vendored VNC client is not ours and mentions nothing of ours.
func readTheInterface(t *testing.T) string {
	t.Helper()

	// The interface's own source, in web/src, rather than the built bundle:
	// the bundle is a build artefact that may not exist in a fresh checkout,
	// and minified names would not match a setting's name anyway.
	//
	// This walks a directory outside the package on purpose. It is a test, so
	// it runs from the package's own directory and can climb out; the embed
	// that ships the interface cannot, which is why the bundle is written to
	// internal/web/dist instead.
	source := filepath.Join("..", "..", "web", "src")
	if _, err := os.Stat(source); err != nil {
		t.Skipf("no interface source to read: %s", err)
	}

	var builder strings.Builder
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".ts", ".tsx":
		default:
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		builder.Write(content)
		return nil
	})
	if err != nil {
		t.Fatalf("cannot read the interface: %s", err)
	}
	if builder.Len() == 0 {
		t.Fatal("read no modules at all, so this test would pass for the wrong reason")
	}
	return builder.String()
}
