package network

import "testing"

func TestKindIsGuessedFromTheLinkAndTheName(t *testing.T) {
	// kindOf looks in /sys first, which says nothing on a machine with no
	// such interface, so these fall through to the link type and the name.
	for _, one := range []struct {
		name     string
		linkType string
		want     string
	}{
		{"eth0", "device", KindEthernet},
		{"enp0s31f6", "device", KindEthernet},
		{"enp0s31f6", "", KindEthernet},
		{"br0", "bridge", KindEthernet},
		{"tun0", "tun", KindOther},
		{"wg0", "wireguard", KindOther},
	} {
		if got := kindOf(one.name, one.linkType); got != one.want {
			t.Errorf("kindOf(%q, %q) = %q, want %q", one.name, one.linkType, got, one.want)
		}
	}
}
