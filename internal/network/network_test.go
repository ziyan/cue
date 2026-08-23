package network

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeSysfs builds the part of /sys/class/net this package reads. Each entry
// names the subdirectories the kernel would have created for that interface:
// "device" for one with hardware behind it, "phy80211" for a radio.
func fakeSysfs(t *testing.T, interfaces map[string][]string) {
	t.Helper()
	root := t.TempDir()
	for name, entries := range interfaces {
		for _, entry := range entries {
			if err := os.MkdirAll(filepath.Join(root, name, entry), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if len(entries) == 0 {
			if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	previous := sysClassNet
	sysClassNet = root
	t.Cleanup(func() { sysClassNet = previous })
}

// A machine that runs containers, which is every machine this daemon is on.
func containerHost(t *testing.T) {
	t.Helper()
	fakeSysfs(t, map[string][]string{
		"enp0s31f6":     {"device"},
		"wlp2s0":        {"device", "phy80211"},
		"lo":            {},
		"docker0":       {},
		"br-1a2b3c4d5e": {},
		"veth9f2c1a":    {},
		"wg0":           {},
	})
}

func TestOnlyInterfacesWithHardwareBehindThemAreCalledPhysical(t *testing.T) {
	containerHost(t)

	for name, want := range map[string]bool{
		"enp0s31f6":     true,
		"wlp2s0":        true,
		"lo":            false,
		"docker0":       false,
		"br-1a2b3c4d5e": false,
		"veth9f2c1a":    false,
		"wg0":           false,
		"nonexistent":   false,
	} {
		if got := isPhysical(name); got != want {
			t.Errorf("isPhysical(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestADockerBridgeIsNotOfferedAsSomethingToPlugACableInto(t *testing.T) {
	containerHost(t)

	// These were all reported as "ethernet" before, which put a Docker bridge
	// and one interface per running container on the page an operator uses to
	// set up a screen — each looking exactly like a socket on the back of the
	// machine.
	for name, linkType := range map[string]string{
		"docker0":       "bridge",
		"br-1a2b3c4d5e": "bridge",
		"veth9f2c1a":    "veth",
		"wg0":           "wireguard",
	} {
		if got := kindOf(name, linkType); got != KindVirtual {
			t.Errorf("kindOf(%q, %q) = %q, want %q", name, linkType, got, KindVirtual)
		}
	}
}

func TestRealHardwareIsStillRecognised(t *testing.T) {
	containerHost(t)

	if got := kindOf("enp0s31f6", "device"); got != KindEthernet {
		t.Errorf("a network card was reported as %q", got)
	}
	// The reliable test for wireless is the directory the kernel makes, not
	// the name: "wl" is a convention and a machine may not follow it.
	if got := kindOf("wlp2s0", "device"); got != KindWireless {
		t.Errorf("a radio was reported as %q", got)
	}
	if got := kindOf("oddlynamed0", "device"); got != KindVirtual {
		t.Errorf("an interface with no hardware behind it was reported as %q", got)
	}
}

func TestAVirtualMachinesCardCountsAsHardware(t *testing.T) {
	// A virtio interface has the device link, and should: in a virtual
	// machine that card is the machine's network hardware, and it is the one
	// an operator has to configure.
	fakeSysfs(t, map[string][]string{"enp1s0": {"device"}})

	if !isPhysical("enp1s0") {
		t.Error("a virtio interface was treated as virtual, so it would be hidden")
	}
	if got := kindOf("enp1s0", "device"); got != KindEthernet {
		t.Errorf("a virtio interface was reported as %q", got)
	}
}
