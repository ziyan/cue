package network

import "testing"

// An interface name becomes a file path -- wpa_supplicant's control socket, the
// configuration written for it, the scan file. Checked at the point of use as
// well as when the configuration is read, so the reasoning is local and does
// not depend on how the name arrived.
func TestAnInterfaceNameThatCouldBeAPathIsRefused(t *testing.T) {
	for what, name := range map[string]string{
		"a traversal":      "../../etc/wpa",
		"a slash":          "wlan0/../root",
		"an absolute path": "/etc/passwd",
		"a bare dot":       ".",
		"two dots":         "..",
		"a null":           "wlan0\x00",
		"empty":            "",
		"too long":         "aaaaaaaaaaaaaaaaaaaa",
	} {
		t.Run(what, func(t *testing.T) {
			if got, err := checkedInterfaceName(name); err == nil {
				t.Errorf("%q was accepted, as %q", name, got)
			}
		})
	}

	for _, name := range []string{"eth0", "wlan0", "wlp2s0", "enx80ca5215b53f", "eno1", "wlan-1_a"} {
		got, err := checkedInterfaceName(name)
		if err != nil {
			t.Errorf("%q was refused: %s", name, err)
		}
		if got != name {
			t.Errorf("%q came back as %q", name, got)
		}
	}
}
