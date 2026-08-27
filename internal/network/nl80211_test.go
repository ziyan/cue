package network

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The kernel's answer about a real radio has to match what the radio really
// is. There is no way to fake this: it needs wireless hardware, so it runs on
// a machine that has some and skips everywhere else.
func TestTheKernelIsAskedWhatARealRadioCanDo(t *testing.T) {
	name := someWirelessInterface(t)

	modes, err := supportedInterfaceModes(name)
	if err != nil {
		t.Fatalf("asking about %s: %s", name, err)
	}

	// Every wireless radio can be a station. A radio that reports it cannot is
	// this code misreading the answer, not a real radio.
	if !modes[2] {
		t.Errorf("%s reports it cannot be a station, so the modes are being "+
			"misread: %v", name, modes)
	}

	supported, err := radioSupportsAccessPoint(name)
	if err != nil {
		t.Fatalf("asking whether %s can be an access point: %s", name, err)
	}
	t.Logf("%s: access point %v, modes %v", name, supported, modes)
}

func TestAWiredInterfaceIsNotMistakenForARadio(t *testing.T) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		t.Skip("no /sys/class/net here")
	}
	for _, entry := range entries {
		if entry.Name() == "lo" {
			continue
		}
		if _, err := os.Stat(filepath.Join("/sys/class/net", entry.Name(), "phy80211")); err == nil {
			continue
		}
		_, err := radioSupportsAccessPoint(entry.Name())
		if err == nil {
			t.Fatalf("%s has no radio behind it but was reported on anyway", entry.Name())
		}
		if !strings.Contains(err.Error(), "not a wireless interface") {
			t.Errorf("%s is not wireless, and the reason given is %q, which does "+
				"not say so plainly", entry.Name(), err)
		}
		return
	}
	t.Skip("every interface here is wireless, so there is nothing to check")
}

func someWirelessInterface(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		t.Skip("no /sys/class/net here")
	}
	for _, entry := range entries {
		if _, err := os.Stat(filepath.Join("/sys/class/net", entry.Name(), "phy80211")); err == nil {
			return entry.Name()
		}
	}
	t.Skip("no wireless hardware on this machine; this runs where there is some")
	return ""
}

// Asking the kernel which network an interface is on has to agree with what
// the machine actually shows. This is how the daemon tells "nothing is here"
// from "something else already has this radio", and it must work from inside
// a container, where reading another program's /proc entry does not.
func TestTheKernelSaysWhichNetworkTheRadioIsOn(t *testing.T) {
	name := someWirelessInterface(t)

	joined, err := associatedNetwork(name)
	if err != nil {
		t.Fatalf("asking what %s is joined to: %s", name, err)
	}
	t.Logf("%s is joined to %q", name, joined)
}

func TestAWiredInterfaceIsJoinedToNothing(t *testing.T) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		t.Skip("no /sys/class/net here")
	}
	for _, entry := range entries {
		if entry.Name() == "lo" {
			continue
		}
		if _, err := os.Stat(filepath.Join("/sys/class/net", entry.Name(), "phy80211")); err == nil {
			continue
		}
		if joined, _ := associatedNetwork(entry.Name()); joined != "" {
			t.Errorf("%s has no radio but is reported joined to %q", entry.Name(), joined)
		}
		return
	}
	t.Skip("every interface here is wireless")
}
