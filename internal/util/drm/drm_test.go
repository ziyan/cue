package drm

import "testing"

func TestConnectorNameMatchesWhatTheXServerCallsIt(t *testing.T) {
	// The kernel and the X server spell these differently, and a
	// configuration written against one does not match the other. Every case
	// here is a real name seen on a real machine.
	cases := map[string]string{
		"card0-HDMI-A-1":  "HDMI-1",
		"card0-HDMI-A-2":  "HDMI-2",
		"card0-HDMI-B-1":  "HDMI-1",
		"card0-DP-1":      "DP-1",
		"card0-DP-2":      "DP-2",
		"card0-eDP-1":     "eDP-1",
		"card1-VGA-1":     "VGA-1",
		"card0-Virtual-1": "Virtual-1",
	}
	for kernelName, expected := range cases {
		if actual := ConnectorName(kernelName); actual != expected {
			t.Errorf("%s became %q, want %q", kernelName, actual, expected)
		}
	}
}

func TestMonitorNameIsReadFromTheEdid(t *testing.T) {
	edid := make([]byte, 128)
	// A monitor name descriptor: two zero bytes, a zero, the 0xfc tag, a
	// zero, then the text padded with a newline.
	descriptor := []byte{0x00, 0x00, 0x00, 0xfc, 0x00}
	descriptor = append(descriptor, []byte("DELL U2720Q\n   ")...)
	copy(edid[54:], descriptor)

	if name := MonitorName(edid); name != "DELL U2720Q" {
		t.Errorf("the monitor name is %q, want %q", name, "DELL U2720Q")
	}
}

func TestMonitorNameIsEmptyWhenTheEdidDoesNotSay(t *testing.T) {
	if name := MonitorName(make([]byte, 128)); name != "" {
		t.Errorf("the monitor name is %q, want empty", name)
	}
	if name := MonitorName(nil); name != "" {
		t.Errorf("the monitor name is %q, want empty", name)
	}
}

func TestFingerprintChangesWhenACableIsMoved(t *testing.T) {
	before := Fingerprint([]Connector{
		{Name: "HDMI-1", Connected: true, Modes: []string{"1920x1080"}},
		{Name: "DP-1", Connected: false},
	})
	after := Fingerprint([]Connector{
		{Name: "HDMI-1", Connected: false},
		{Name: "DP-1", Connected: true, Modes: []string{"1920x1080"}},
	})
	if before == after {
		t.Error("moving the cable from one socket to the other did not change the fingerprint")
	}
}
