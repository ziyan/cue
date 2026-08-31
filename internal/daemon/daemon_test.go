package daemon

import (
	"github.com/ziyan/cue/internal/media"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
)

// displayRestartNeeded decides whether a configuration change can be applied
// to a running X server or needs a new one. Getting it wrong in one direction
// blanks the screen for no reason; in the other, a setting is accepted through
// the interface and silently does nothing, which is how switching the server
// from xorg to xvfb appeared to work and did not.
func TestWhatNeedsTheXServerRestartedAndWhatDoesNot(t *testing.T) {
	running := config.Default().Display

	cases := map[string]struct {
		change func(*config.Display)
		needed bool
	}{
		"the server itself": {
			func(display *config.Display) { display.Server = config.ServerXvfb }, true,
		},
		"the display number": {
			func(display *config.Display) { display.Number = 3 }, true,
		},
		"taking the server's cursor away, which is a command line flag": {
			func(display *config.Display) { display.Cursor = config.CursorHidden }, true,
		},
		"showing a cursor the server already has, which the daemon does itself": {
			func(display *config.Display) { display.Cursor = config.CursorAlways }, false,
		},
		"an output's mode, which RandR can change": {
			func(display *config.Display) { display.Outputs[0].Mode = "1280x720" }, false,
		},
		"the framebuffer on a real server, which RandR can change": {
			func(display *config.Display) { display.Framebuffer = "1920x1080" }, false,
		},
		"nothing at all": {
			func(display *config.Display) {}, false,
		},
	}

	for what, expected := range cases {
		updated := config.Default()
		expected.change(&updated.Display)

		daemon := &Daemon{}
		if got := daemon.displayRestartWouldBeNeeded(running, updated); got != expected.needed {
			t.Errorf("changing %s: restart=%v, want %v", what, got, expected.needed)
		}
	}
}

func TestTheVirtualServersSizeIsFixedWhenItStarts(t *testing.T) {
	// Xvfb takes its screen size on the command line and RandR cannot change
	// it, so unlike a real server this one does need restarting for it.
	running := config.Default().Display
	running.Server = config.ServerXvfb
	running.Framebuffer = "1280x720"

	updated := config.Default()
	updated.Display.Server = config.ServerXvfb
	updated.Display.Framebuffer = "1920x1080"

	daemon := &Daemon{}
	if !daemon.displayRestartWouldBeNeeded(running, updated) {
		t.Error("resizing a virtual screen was accepted without a restart, so it would have done nothing")
	}
}

// A device that has been told about a network but cannot join it is not
// settled, and used to be treated as though it were.
//
// That is the failure this whole path exists for. A wireless passphrase
// changed underneath a device left it configured, unable to join, without an
// address, and -- by the old reading -- settled: it never offered its setup
// code again and nobody could reach it to say so. Only somebody with physical
// access and a shell could put it right.
func TestBeingToldAboutANetworkIsNotTheSameAsBeingOnOne(t *testing.T) {
	configuration := config.Default()
	configuration.Network.Manage = true
	configuration.Network.Interfaces = []config.Interface{{
		Name:     "wlan0",
		Method:   "dhcp",
		Wireless: &config.Wireless{SSID: "a test network", Passphrase: "the wrong test passphrase"},
	}}
	configuration.Normalize()

	// hasSomewhereToBe asks the machine what it can reach, so on a machine
	// with any working interface it says yes and there is nothing to assert.
	// What can be asserted anywhere is that the configuration alone no longer
	// decides it: nothing in the function reads the interface list from the
	// file.
	if !anyConfiguredNetwork(configuration) {
		t.Fatal("the test configuration does not name a network")
	}

	// Configured, and reaching nothing. It must be declared lost -- under the
	// old reading it never was, because the file named a network.
	configuration.Network.LostAfter = config.Duration(time.Minute)
	stranded := &Daemon{canReach: neverReachable}
	stranded.lastReachable = time.Now().Add(-time.Hour)
	if !stranded.networkLooksLost(configuration) {
		t.Error("a device that cannot join the network it was told about was " +
			"treated as settled, which is how one becomes unreachable for ever")
	}

	// And a device that has never had an address must not be declared lost the
	// moment it starts: its interfaces have not finished coming up.
	empty := config.Default()
	empty.Network.LostAfter = config.Duration(time.Hour)
	empty.Normalize()
	lonely := &Daemon{canReach: neverReachable}
	if lonely.networkLooksLost(empty) {
		t.Error("a device decided its network was lost before it had finished starting")
	}
}

// A device that can reach something is never lost, however long it runs.
func TestADeviceThatCanReachSomethingIsNeverLost(t *testing.T) {
	configuration := config.Default()
	configuration.Normalize()

	daemon := &Daemon{canReach: alwaysReachable}
	daemon.lastReachable = time.Now().Add(-24 * time.Hour)

	if daemon.networkLooksLost(configuration) {
		t.Error("a device that can reach something was declared lost")
	}
	// And seeing it reachable resets the clock, so a moment of trouble later
	// does not count from a sighting hours ago.
	if time.Since(daemon.lastReachable) > time.Minute {
		t.Error("seeing the network did not reset the clock")
	}
}

// The moment it stops reaching anything the clock starts, and it is not
// declared lost until the configured time has passed.
func TestADeviceIsGivenTheConfiguredTimeBeforeBeingDeclaredLost(t *testing.T) {
	configuration := config.Default()
	configuration.Network.LostAfter = config.Duration(30 * time.Minute)
	configuration.Normalize()

	daemon := &Daemon{canReach: neverReachable}

	// Just lost: the clock starts now, and nothing is concluded yet.
	if daemon.networkLooksLost(configuration) {
		t.Error("a device was declared lost the moment it stopped reaching anything")
	}

	daemon.lastReachable = time.Now().Add(-20 * time.Minute)
	if daemon.networkLooksLost(configuration) {
		t.Error("gave up after 20 minutes when the setting says 30")
	}

	daemon.lastReachable = time.Now().Add(-40 * time.Minute)
	if !daemon.networkLooksLost(configuration) {
		t.Error("did not give up after 40 minutes when the setting says 30")
	}
}

// A device with a cable is never declared lost because of its wireless: what
// is asked is whether anything reaches anything.
func TestReachingSomethingByAnyMeansIsEnough(t *testing.T) {
	configuration := config.Default()
	configuration.Network.LostAfter = config.Duration(time.Minute)
	configuration.Network.Manage = true
	configuration.Network.Interfaces = []config.Interface{{
		Name:     "wlan0",
		Method:   "dhcp",
		Wireless: &config.Wireless{SSID: "a test network", Passphrase: "a test passphrase"},
	}}
	configuration.Normalize()

	daemon := &Daemon{canReach: alwaysReachable}
	daemon.lastReachable = time.Now().Add(-time.Hour)

	if daemon.networkLooksLost(configuration) {
		t.Error("a device that reaches something over one interface was declared " +
			"lost because another was not working")
	}
}

func alwaysReachable(*config.Configuration) bool { return true }
func neverReachable(*config.Configuration) bool  { return false }

// The network a device was told about is never forgotten by falling back:
// it is what the retry uses, and a device that heals itself has to end up
// where it started.
func TestFallingBackKeepsTheNetworkItWasToldAbout(t *testing.T) {
	configuration := config.Default()
	configuration.Network.Manage = true
	configuration.Network.Interfaces = []config.Interface{{
		Name:     "wlan0",
		Method:   "dhcp",
		Wireless: &config.Wireless{SSID: "a test network", Passphrase: "a test passphrase"},
	}}
	configuration.Normalize()

	if !anyConfiguredNetwork(configuration) {
		t.Fatal("the network is not in the configuration to begin with")
	}

	daemon := &Daemon{canReach: neverReachable}
	daemon.lastReachable = time.Now().Add(-time.Hour)
	if !daemon.networkLooksLost(configuration) {
		t.Fatal("the device did not decide its network was lost")
	}

	if !anyConfiguredNetwork(configuration) {
		t.Error("deciding the network was lost removed it from the configuration, " +
			"so there is nothing left to go back to")
	}
}

// Deleting a playlist item takes its video with it.
//
// The sweep existed and was wired to the wrong event: it ran when the screens
// attached to the machine changed, under a comment claiming that was where a
// deleted item is noticed. It is not -- that loop watches monitors being
// plugged in. So removing an item left its file on the disk until the daemon
// next restarted or somebody moved a cable, and on a device managed entirely
// from elsewhere neither of those is something anybody does.
func TestDeletingAnItemSweepsItsUpload(t *testing.T) {
	directory := t.TempDir()
	uploads, err := media.Open(directory)
	if err != nil {
		t.Fatal(err)
	}

	stored, err := uploads.Add("promo.mp4", "video/mp4", strings.NewReader("not really a video"))
	if err != nil {
		t.Fatal(err)
	}

	configuration := config.Default()
	configuration.Playlist.Items = []config.Item{{
		Identifier: "one",
		Title:      "Promo",
		Media:      &config.ItemMedia{File: stored.File, Kind: "video", Name: "promo.mp4"},
	}}

	daemon := &Daemon{
		uploads: uploads,
		store:   config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration),
	}

	// While something refers to it, it stays -- whatever else happens.
	daemon.sweepUploads()
	if _, err := uploads.Details(stored.File); err != nil {
		t.Fatalf("a file the playlist refers to was removed: %s", err)
	}

	// The item goes.
	if err := daemon.store.Update(func(updated *config.Configuration) error {
		updated.Playlist.Items = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// A freshly written file is left alone for a while, so that an upload
	// which has not been saved into the playlist yet is not swept out from
	// under whoever is still filling in the form. Aged past that here rather
	// than waiting.
	older := time.Now().Add(-24 * time.Hour)
	for _, name := range []string{stored.File + ".media", stored.File + ".json"} {
		_ = os.Chtimes(filepath.Join(directory, name), older, older)
	}

	daemon.sweepUploads()
	if _, err := uploads.Details(stored.File); err == nil {
		t.Error("the file outlived the only item that referred to it")
	}
}

// The VNC server and the clock are started, stopped and restarted while the
// daemon runs, unlike the X server and the browser which are up for as long as
// it is. Every one of these cases was silently wrong before: the daemon started
// x11vnc once, at boot, and nothing ever looked at those settings again. Turning
// screen sharing on did nothing until the container was restarted, and so did
// moving it off the loopback, and so did setting a password on it.
func TestAChildIsStartedStoppedAndRestartedToFollowTheConfiguration(t *testing.T) {
	for name, expected := range map[string]struct {
		running, wanted, changed bool
		action                   action
	}{
		"turning it on starts it":                    {running: false, wanted: true, action: childStart},
		"turning it off stops it":                    {running: true, wanted: false, action: childStop},
		"changing a setting restarts it":             {running: true, wanted: true, changed: true, action: childRestart},
		"changing nothing leaves it alone":           {running: true, wanted: true, changed: false, action: childNothing},
		"an unrelated change to a stopped one":       {running: false, wanted: false, changed: true, action: childNothing},
		"a setting changed while it was off is kept": {running: false, wanted: true, changed: true, action: childStart},
	} {
		t.Run(name, func(t *testing.T) {
			got := childAction(expected.running, expected.wanted, expected.changed)
			if got != expected.action {
				t.Errorf("childAction(running=%v, wanted=%v, changed=%v) = %v, want %v",
					expected.running, expected.wanted, expected.changed, got, expected.action)
			}
		})
	}
}

// The comparison that decides "changed" has to see every VNC setting. It is a
// whole-struct comparison so that it cannot fall behind, and this is the test
// that says so: a field added to config.VNC is covered without anybody editing
// this file, and if somebody replaces the comparison with a list of fields, the
// field they forget fails here.
func TestEveryVNCSettingReachesTheServer(t *testing.T) {
	for index := range reflect.TypeOf(config.VNC{}).NumField() {
		field := reflect.TypeOf(config.VNC{}).Field(index)
		t.Run(field.Name, func(t *testing.T) {
			running := config.Default().VNC
			running.Enabled = true

			updated := running
			target := reflect.ValueOf(&updated).Elem().Field(index)
			switch target.Kind() {
			case reflect.Bool:
				target.SetBool(!target.Bool())
			case reflect.String:
				target.SetString(target.String() + "-changed")
			default:
				t.Fatalf("no way to change a %s", target.Type())
			}
			// Enabled is the one field that is not a restart: turning it off
			// stops the server rather than restarting it, which the action
			// test above covers.
			if field.Name == "Enabled" {
				return
			}

			if childAction(true, updated.Enabled, running != updated) != childRestart {
				t.Errorf("changing VNC.%s does not reach the running server: it is accepted, "+
					"saved, and x11vnc keeps the setting it started with", field.Name)
			}
		})
	}
}

// The same guard as the browser's, for the display. Three settings were on the
// X server's command line and missing from displayRestartWouldBeNeeded --
// which console to draw on, the extra arguments, and the Xorg configuration --
// so editing any of them was accepted, saved, and did nothing until the next
// boot.
//
// Every field of config.Display has to be one of two things, and this fails
// until it is said which: it needs the server restarted, or it is named below
// as reaching the screen without one.
func TestEveryDisplaySettingIsClassified(t *testing.T) {
	appliedWithoutARestart := map[string]string{
		"Wallpaper":         "drawn by the daemon, not the server",
		"Outputs":           "arranged over RandR by applyLayout",
		"Modeline":          "added over RandR by applyLayout",
		"ModeName":          "chosen over RandR by applyLayout",
		"BlankAfter":        "set over the screensaver extension",
		"CursorIdleTimeout": "the daemon hides and shows the pointer itself",
		"ReconcileInterval": "read each time round the arrange loop",
		"Framebuffer":       "resized over RandR under Xorg; the Xvfb case needs a restart and is covered by its own case",
		"Cursor":            "only whether the server draws one at all needs a restart, which is its own case",
	}

	daemon := &Daemon{}
	for index := range reflect.TypeOf(config.Display{}).NumField() {
		field := reflect.TypeOf(config.Display{}).Field(index)
		t.Run(field.Name, func(t *testing.T) {
			running := config.Default().Display
			updated := config.Default()

			target := reflect.ValueOf(&updated.Display).Elem().Field(index)
			switch target.Kind() {
			case reflect.Bool:
				target.SetBool(!target.Bool())
			case reflect.String:
				target.SetString(target.String() + "-changed")
			case reflect.Int, reflect.Int64:
				target.SetInt(target.Int() + 7)
			case reflect.Slice:
				if target.Type().Elem().Kind() != reflect.String {
					t.Skipf("no way to change a %s", target.Type())
				}
				target.Set(reflect.ValueOf([]string{"--changed"}))
			default:
				t.Skipf("no way to change a %s", target.Type())
			}

			restart := daemon.displayRestartWouldBeNeeded(running, updated)
			if reason, ok := appliedWithoutARestart[field.Name]; ok {
				if restart {
					t.Errorf("Display.%s is listed as reaching the screen without a restart (%s), "+
						"but the daemon restarts the X server for it, which blanks the screen",
						field.Name, reason)
				}
				return
			}
			if !restart {
				t.Errorf("Display.%s is a setting that nothing applies: the X server is not "+
					"restarted for it, and it is not listed as reaching the screen without one. "+
					"Add it to one or the other.", field.Name)
			}
		})
	}
}
