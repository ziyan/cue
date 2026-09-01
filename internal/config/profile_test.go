package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// profileOf reads a profile the way it arrives on the wire: a sparse document
// in the same shape as the pruned file.
func profileOf(t *testing.T, document string) map[string]interface{} {
	t.Helper()
	profile := map[string]interface{}{}
	if err := yaml.Unmarshal([]byte(document), &profile); err != nil {
		t.Fatalf("the test's own profile does not parse: %s", err)
	}
	return profile
}

func applied(t *testing.T, configuration *Configuration, document string) {
	t.Helper()
	if _, err := configuration.ApplyProfile(profileOf(t, document)); err != nil {
		t.Fatalf("applying the profile: %s", err)
	}
}

// The whole of the merge rule in one test: what the profile names is taken,
// and what it does not name is left exactly as it was. The second half is what
// makes one profile safe on forty machines, and it is the half that would fail
// silently -- a device whose name or network was quietly replaced looks fine
// until somebody goes to find it.
func TestAProfileWinsWhereItSpeaksAndIsSilentElsewhere(t *testing.T) {
	configuration := Default()
	configuration.Device.Name = "The screen by the door"
	configuration.Device.Identifier = "01arz3ndektsv4rrffq69g5fav"
	configuration.Browser.DarkMode = false
	configuration.Watchdog.Enabled = false

	applied(t, configuration, `
browser:
  darkMode: true
watchdog:
  enabled: true
`)

	if !configuration.Browser.DarkMode {
		t.Error("the profile set browser.darkMode and the device did not take it")
	}
	if !configuration.Watchdog.Enabled {
		t.Error("the profile set watchdog.enabled and the device did not take it")
	}
	if configuration.Device.Name != "The screen by the door" {
		t.Errorf("the device's name became %q; the profile said nothing about it",
			configuration.Device.Name)
	}
	if configuration.Device.Identifier != "01arz3ndektsv4rrffq69g5fav" {
		t.Errorf("the device's identifier became %q; the profile said nothing about it",
			configuration.Device.Identifier)
	}
}

// Drift is corrected. A setting changed on the device by somebody with the
// local interface goes back to what the profile says at the next poll, which
// is the point of a profile rather than a side effect of one.
func TestAProfileCorrectsLocalDrift(t *testing.T) {
	configuration := Default()
	applied(t, configuration, "browser:\n  darkMode: true\n")

	configuration.Browser.DarkMode = false
	applied(t, configuration, "browser:\n  darkMode: true\n")

	if !configuration.Browser.DarkMode {
		t.Error("a setting changed on the device was not corrected by the next poll")
	}
}

// The record of what was taken is what makes releasing possible at all.
func TestTheKeysTakenAreRecordedSorted(t *testing.T) {
	configuration := Default()
	applied(t, configuration, `
watchdog:
  enabled: true
browser:
  darkMode: true
  deviceScaleFactor: 1.5
`)

	want := []string{"browser.darkMode", "browser.deviceScaleFactor", "watchdog.enabled"}
	got := configuration.Service.ProfileKeys
	if len(got) != len(want) {
		t.Fatalf("recorded %v; wanted %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			// Sorted is a requirement rather than tidiness: versionOf hashes
			// the marshalled file, so a list that reordered between writes
			// would move the configuration's version on every poll and make
			// conditional writes from the web interface fail against a
			// document nobody edited.
			t.Fatalf("recorded %v; wanted %v, in that order", got, want)
		}
	}
}

// A key the profile stops naming goes back to the device's own default. Without
// this the whole thing is a one-way door: a setting removed from a profile
// stays stuck on every screen it ever touched.
func TestASettingDroppedFromAProfileIsReleased(t *testing.T) {
	configuration := Default()
	applied(t, configuration, "browser:\n  darkMode: true\n  forceDarkContent: true\n")

	if !configuration.Browser.ForceDarkContent {
		t.Fatal("the profile did not take in the first place")
	}

	applied(t, configuration, "browser:\n  darkMode: true\n")

	if configuration.Browser.ForceDarkContent != Default().Browser.ForceDarkContent {
		t.Error("a setting the profile stopped naming was not released to its default")
	}
	if !configuration.Browser.DarkMode {
		t.Error("a setting the profile still names was released as well")
	}
}

// Unapplying is the same operation with an empty document, and that is why an
// empty profile is a real answer rather than an absent one.
func TestAnEmptyProfileReleasesEverything(t *testing.T) {
	configuration := Default()
	configuration.Device.Name = "Reception"
	applied(t, configuration, "browser:\n  darkMode: true\n")

	applied(t, configuration, "{}")

	if configuration.Browser.DarkMode != Default().Browser.DarkMode {
		t.Error("an empty profile did not release what the device was managed by")
	}
	if len(configuration.Service.ProfileKeys) != 0 {
		t.Errorf("still claims to be managing %v", configuration.Service.ProfileKeys)
	}
	if configuration.Device.Name != "Reception" {
		t.Error("releasing took something the profile had never given")
	}
}

// The refusal list, one key at a time. Each of these would be a different kind
// of bad day: the identifier makes forty screens into one device as far as the
// service is concerned, the service section could stop a fleet polling, and the
// rest are credentials.
func TestAProfileCannotSetWhatItMustNotSet(t *testing.T) {
	for what, document := range map[string]string{
		"the device's identifier": "device:\n  identifier: 01arz3ndektsv4rrffq69g5fav\n",
		"the service address":     "service:\n  address: https://example.com\n",
		"the poll interval":       "service:\n  pollInterval: 9999s\n",
		"the admin password":      "web:\n  passwordHash: not-a-real-hash-placeholder\n",
		"the session secret":      "web:\n  sessionSecret: placeholder-for-a-test\n",
		"the interface list":      "network:\n  interfaces:\n    - name: wlan0\n      wireless:\n        ssid: elsewhere\n",
		"the paths":               "paths:\n  state: /tmp/elsewhere\n",
		"the playlist":            "playlist:\n  items:\n    - url: https://example.com/\n",
	} {
		t.Run(what, func(t *testing.T) {
			configuration := Default()
			before, err := yaml.Marshal(configuration)
			if err != nil {
				t.Fatal(err)
			}

			applied(t, configuration, document)

			after, err := yaml.Marshal(configuration)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Errorf("a profile setting %s changed the configuration", what)
			}
			if len(configuration.Service.ProfileKeys) != 0 {
				t.Errorf("a refused key entered the managed set: %v",
					configuration.Service.ProfileKeys)
			}
		})
	}
}

// A profile from a newer service naming something this version has never heard
// of is ignored, the way an unknown key in the file already is, rather than
// refusing the whole document and leaving the device unmanaged.
func TestAProfileNamingAnUnknownSettingIsIgnored(t *testing.T) {
	configuration := Default()
	applied(t, configuration, "browser:\n  darkMode: true\n  somethingFromTheFuture: 7\n")

	if !configuration.Browser.DarkMode {
		t.Error("one unknown setting made the whole profile fail to apply")
	}
}

// A list is managed whole. There is no addressing an element by a dotted path
// and there should not be: half a managed output list is not a state anybody
// can reason about.
func TestAListIsManagedWhole(t *testing.T) {
	configuration := Default()
	applied(t, configuration, `
display:
  outputs:
    - name: HDMI-1
      mode: 1920x1080
`)

	if len(configuration.Display.Outputs) != 1 {
		t.Fatalf("took %d output(s); the profile named one list", len(configuration.Display.Outputs))
	}
	if configuration.Display.Outputs[0].Name != "HDMI-1" {
		t.Errorf("took %q", configuration.Display.Outputs[0].Name)
	}
	if !contains(configuration.Service.ProfileKeys, "display.outputs") {
		t.Errorf("recorded %v; the whole list is one key", configuration.Service.ProfileKeys)
	}
}

// Applying the same profile twice must be quiet. The poller writes only when
// something changed, and a merge that reported a change every time would
// rewrite the file on every poll -- moving the configuration's version, and so
// making conditional writes from the web interface fail against a document
// nobody edited.
func TestApplyingTheSameProfileTwiceChangesNothing(t *testing.T) {
	configuration := Default()
	applied(t, configuration, "browser:\n  darkMode: true\n")

	changed, err := configuration.ApplyProfile(profileOf(t, "browser:\n  darkMode: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("the same profile applied twice reported a change the second time")
	}
}

// The network settings that are safe to share, as against the one that is not.
// A profile carrying manage, onboarding, lostAfter or the reconcile interval is
// saying the same sentence on every machine, which is what a profile is for.
func TestAProfileCanSetTheNetworkSettingsThatAreNotCredentials(t *testing.T) {
	configuration := Default()
	configuration.Network.Manage = false

	applied(t, configuration, "network:\n  manage: true\n  lostAfter: 900s\n")

	if !configuration.Network.Manage {
		t.Error("a profile could not turn network management on")
	}
	if configuration.Network.LostAfter.Duration() != 900*time.Second {
		t.Errorf("lostAfter is %s", configuration.Network.LostAfter.Duration())
	}
}

// The one that would take a fleet off the network. A profile naming the
// interface list replaces it whole, and a wireless passphrase lives inside an
// interface and never travels -- so every device it touched would be left with
// an SSID it cannot join, and the way to fix that remotely is the network it
// has just lost. Applied to forty screens it is forty site visits.
func TestAProfileCannotReplaceTheInterfaceList(t *testing.T) {
	configuration := Default()
	configuration.Network.Interfaces = []Interface{{
		Name:     "wlan0",
		Wireless: &Wireless{SSID: "office", Passphrase: Secret("a-test-passphrase")},
	}}

	applied(t, configuration, `
network:
  manage: true
  interfaces:
    - name: wlan0
      wireless:
        ssid: office-new
`)

	if len(configuration.Network.Interfaces) != 1 {
		t.Fatalf("the device has %d interface(s); the profile must not change the list",
			len(configuration.Network.Interfaces))
	}
	wireless := configuration.Network.Interfaces[0].Wireless
	if wireless == nil || wireless.SSID != "office" {
		t.Error("a profile moved this device to another network")
	}
	if wireless == nil || !wireless.Passphrase.IsSet() {
		t.Error("a profile removed this device's passphrase, which takes it off the network")
	}
	if !configuration.Network.Manage {
		t.Error("refusing the interface list also refused the rest of the section")
	}
	if contains(configuration.Service.ProfileKeys, "network.interfaces") {
		t.Error("the refused list entered the managed set")
	}
}
