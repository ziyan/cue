package config

import (
	"strings"
	"testing"
	"time"
)

// The one thing that must never happen: writing the file must not lose
// anything. Leaving out a value because it matches the default is only safe
// if reading the file back produces the same configuration.
func TestWritingAndReadingBackChangesNothing(t *testing.T) {
	original := Default()
	original.Device.Name = "Reception"
	original.Device.Timezone = "Europe/London"
	original.Playlist.Interval = Duration(45 * time.Second)
	original.Playlist.Items = []Item{
		{Identifier: "one", URL: "http://dashboard.example.com/", Title: "Dashboard"},
		{Identifier: "two", URL: "http://other.example.com/", Reload: true, Duration: Duration(90 * time.Second)},
	}
	original.Network.Manage = true
	original.Network.Interfaces = []Interface{
		{Name: "wlan0", Method: "dhcp", Wireless: &Wireless{SSID: "a test network", Passphrase: "a test passphrase"}},
	}
	original.Web.PasswordHash = "a test hash"
	original.Time.Enabled = false
	original.Normalize()

	content, err := original.Marshal()
	if err != nil {
		t.Fatalf("writing: %s", err)
	}

	back, err := Parse(content)
	if err != nil {
		t.Fatalf("reading back what was written: %s\n%s", err, content)
	}

	if !sameConfiguration(t, original, back) {
		t.Errorf("the configuration changed by being written and read:\n%s", content)
	}
}

// And the point of it: a device told two things has a file with two things in
// it, not a hundred.
func TestOnlyWhatSomebodyChoseIsWritten(t *testing.T) {
	configuration := Default()
	configuration.Device.Name = "Reception"
	configuration.Normalize()

	content, err := configuration.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	written := string(content)

	if !strings.Contains(written, "Reception") {
		t.Error("what was chosen is not in the file")
	}
	if !strings.Contains(written, "identifier") {
		t.Error("the device's identifier is not in the file, and it is not a default")
	}
	// Things nobody touched.
	for _, absent := range []string{"reconcileInterval", "failuresBeforeClearCache", "maximumUploadSize", "sandbox"} {
		if strings.Contains(written, absent) {
			t.Errorf("%q was written although nobody chose it:\n%s", absent, written)
		}
	}

	body := written[strings.Index(written, "\n\n")+2:]
	if lines := strings.Count(strings.TrimSpace(body), "\n") + 1; lines > 12 {
		t.Errorf("a device told one thing has a %d line configuration:\n%s", lines, body)
	}
}

// A setting somebody set back to the default disappears from the file, which
// is correct -- and it has to still read back as that value.
func TestASettingReturnedToItsDefaultIsSimplyGone(t *testing.T) {
	configuration := Default()
	configuration.Normalize()
	configuration.Network.ReconcileInterval = Duration(90 * time.Second)

	content, err := configuration.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "reconcileInterval") {
		t.Fatal("a changed setting was not written")
	}

	configuration.Network.ReconcileInterval = Default().Network.ReconcileInterval
	content, err = configuration.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "reconcileInterval") {
		t.Error("a setting put back to its default is still in the file")
	}

	back, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if back.Network.ReconcileInterval != Default().Network.ReconcileInterval {
		t.Errorf("it reads back as %s", back.Network.ReconcileInterval.Duration())
	}
}

// One changed setting must not drag its whole section into the file.
func TestOneChangedSettingBringsOnlyItself(t *testing.T) {
	configuration := Default()
	configuration.Normalize()
	configuration.Browser.Sandbox = !Default().Browser.Sandbox

	content, err := configuration.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	written := string(content)
	if !strings.Contains(written, "sandbox") {
		t.Fatal("the changed setting is missing")
	}
	if strings.Contains(written, "ephemeralCache") {
		t.Errorf("the rest of the browser section came with it:\n%s", written)
	}
}

// A password must survive, since it is not a default and losing it locks
// somebody out of their own device.
func TestSecretsAreNeverPrunedAway(t *testing.T) {
	configuration := Default()
	configuration.Web.PasswordHash = "a test hash nobody could guess"
	configuration.Normalize()

	content, err := configuration.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "a test hash nobody could guess") {
		t.Error("the password hash was left out of the file")
	}
}

// sameConfiguration compares two configurations by what they would be written
// as, which is the only comparison that matters here.
func sameConfiguration(t *testing.T, first, second *Configuration) bool {
	t.Helper()
	firstContent, err := first.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	secondContent, err := second.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(firstContent) != string(secondContent) {
		t.Logf("before:\n%s\nafter:\n%s", firstContent, secondContent)
		return false
	}
	return true
}
