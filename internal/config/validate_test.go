package config

import "testing"

// An interface name is joined into file paths -- the wpa_supplicant control
// socket, its configuration, the scan file -- so a name containing a separator
// would put those somewhere else entirely. The kernel would refuse such a name
// anyway, which is what makes this a validation rather than a restriction.
func TestAnInterfaceNameCannotBeAPath(t *testing.T) {
	for what, name := range map[string]string{
		"a traversal":      "../../etc/wpa",
		"a slash":          "wlan0/../../root",
		"an absolute path": "/etc/passwd",
		"whitespace":       "wlan 0",
		"far too long":     "aaaaaaaaaaaaaaaaaaaaaaaa",
		"a null":           "wlan0\x00",
	} {
		t.Run(what, func(t *testing.T) {
			configuration := Default()
			configuration.Network.Manage = true
			configuration.Network.Interfaces = []Interface{{Name: name, Method: AddressMethodDHCP}}

			if err := configuration.Validate(); err == nil {
				t.Errorf("%q was accepted as an interface name", name)
			}
		})
	}

	// And the ordinary ones still work, or this has broken every device.
	for _, name := range []string{"eth0", "wlan0", "wlp2s0", "enx80ca5215b53f", "eno1"} {
		configuration := Default()
		configuration.Network.Manage = true
		configuration.Network.Interfaces = []Interface{{Name: name, Method: AddressMethodDHCP}}
		if err := configuration.Validate(); err != nil {
			t.Errorf("%q was refused: %s", name, err)
		}
	}
}
