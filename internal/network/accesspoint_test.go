package network

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ziyan/cue/internal/config"
)

func newTestStore(t *testing.T) *config.Store {
	t.Helper()
	configuration := config.Default()
	configuration.Paths.State = t.TempDir()
	configuration.Paths.Runtime = t.TempDir()
	configuration.Normalize()
	return config.OpenWith(filepath.Join(t.TempDir(), "cue.yaml"), configuration)
}

// The configuration handed to wpa_supplicant has to put it in access point
// mode and offer WPA2 and nothing weaker, because some phones now refuse to
// join a network that offers the older ciphers at all.
func TestTheAccessPointConfigurationAsksForWPA2AndNothingOlder(t *testing.T) {
	store := newTestStore(t)
	point := NewAccessPoint(store, "wlan0", Credentials{SSID: "cue-4k2p9x", Passphrase: "hd7Rk2m9Qw4x"})

	if err := point.writeConfiguration(); err != nil {
		t.Fatalf("writing the configuration: %s", err)
	}
	content, err := os.ReadFile(point.configurationFilename())
	if err != nil {
		t.Fatal(err)
	}
	written := string(content)

	for _, wanted := range []string{
		"mode=2",        // an access point, not a client
		"proto=RSN",     // WPA2
		"pairwise=CCMP", // and its cipher
		"group=CCMP",
		"key_mgmt=WPA-PSK",
		`ssid="cue-4k2p9x"`,
		`psk="hd7Rk2m9Qw4x"`,
		"frequency=2437",
	} {
		if !strings.Contains(written, wanted) {
			t.Errorf("the configuration does not contain %q:\n%s", wanted, written)
		}
	}
	for _, unwanted := range []string{"WPA-EAP", "TKIP", "proto=WPA "} {
		if strings.Contains(written, unwanted) {
			t.Errorf("the configuration offers %q, which it should not:\n%s", unwanted, written)
		}
	}
}

// The passphrase must not outlive the network it belongs to.
func TestStoppingTheAccessPointTakesThePassphraseOffTheDisk(t *testing.T) {
	store := newTestStore(t)
	point := NewAccessPoint(store, "wlan0", Credentials{SSID: "cue-4k2p9x", Passphrase: "hd7Rk2m9Qw4x"})
	if err := point.writeConfiguration(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(point.configurationFilename()); err != nil {
		t.Fatalf("the configuration was not written: %s", err)
	}

	point.Stop(context.Background())

	if _, err := os.Stat(point.configurationFilename()); !os.IsNotExist(err) {
		t.Error("the configuration, and the passphrase in it, is still on the disk " +
			"after the network was taken down")
	}
}

// The file holds the only copy of a live secret while the network is up.
func TestTheConfigurationIsNotReadableByAnybodyElse(t *testing.T) {
	store := newTestStore(t)
	point := NewAccessPoint(store, "wlan0", Credentials{SSID: "cue-4k2p9x", Passphrase: "hd7Rk2m9Qw4x"})
	if err := point.writeConfiguration(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(point.configurationFilename())
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the file holding the passphrase is mode %o, want 600", mode)
	}
}

// Bringing a real network up on real hardware.
//
// This needs a wireless interface that nothing else is using, and the
// privileges to drive it, so it runs only when an interface is named:
//
//	CUE_ACCESS_POINT_INTERFACE=wlp4s0 go test ./internal/network/ -run TestTheSetupNetworkGoesOnTheAir
//
// Set CUE_ACCESS_POINT_HOLD to a number of seconds to leave the network up
// afterwards, which is how it is checked from another machine: the point is
// not that wpa_supplicant started but that a different radio can see the
// network, and only a second machine can tell you that.
func TestTheSetupNetworkGoesOnTheAir(t *testing.T) {
	interfaceName := os.Getenv("CUE_ACCESS_POINT_INTERFACE")
	if interfaceName == "" {
		t.Skip("set CUE_ACCESS_POINT_INTERFACE to a free wireless interface to run this")
	}

	credentials, err := NewCredentials()
	if err != nil {
		t.Fatalf("inventing credentials: %s", err)
	}
	if fixed := os.Getenv("CUE_ACCESS_POINT_SSID"); fixed != "" {
		credentials.SSID = fixed
	}

	store := newTestStore(t)
	point := NewAccessPoint(store, interfaceName, credentials)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	defer point.Stop(context.Background())

	if err := point.Start(ctx); err != nil {
		t.Fatalf("bringing %q up on %s: %s", credentials.SSID, interfaceName, err)
	}
	if !point.Running() {
		t.Fatal("the access point reports it is not running immediately after starting")
	}

	t.Logf("advertising %q on %s with passphrase %s", credentials.SSID, interfaceName, credentials.Passphrase)

	if hold := os.Getenv("CUE_ACCESS_POINT_HOLD"); hold != "" {
		seconds, err := strconv.Atoi(hold)
		if err != nil {
			t.Fatalf("CUE_ACCESS_POINT_HOLD is %q, which is not a number of seconds", hold)
		}
		t.Logf("holding it up for %d seconds so another machine can look for it", seconds)
		time.Sleep(time.Duration(seconds) * time.Second)
	}
}

// The setup network's own address must not count as having joined something.
//
// This is the check that failed on a real device. The address the daemon gives
// itself for its own setup network stayed on the interface after the access
// point came down, and everything downstream read it as "this device has a
// network": the DHCP client skips an interface that already has a usable
// address, so it never asked for a real one, and the join reported success
// three quarters of a second after starting. The device put its playlist back
// on the screen and reached nothing.
func TestTheSetupAddressIsNotMistakenForHavingJoinedSomething(t *testing.T) {
	// A machine's own loopback stands in for an interface carrying only an
	// address this daemon put there: nothing else to find.
	if HasUsableAddressOtherThan("lo", net.IPv4(192, 168, 216, 1)) {
		t.Error("the loopback is reported as reaching something")
	}

	// And an interface that really is on a network still reports it.
	interfaces, err := Interfaces()
	if err != nil {
		t.Skip("cannot list interfaces here")
	}
	found := false
	for _, one := range interfaces {
		if one.Physical && HasUsableAddress(one.Name) {
			if !HasUsableAddressOtherThan(one.Name, net.IPv4(192, 168, 216, 1)) {
				t.Errorf("%s has a real address but is reported as having none once "+
					"the setup address is discounted", one.Name)
			}
			found = true
			break
		}
	}
	if !found {
		t.Skip("no interface here has an address, so there is nothing to check against")
	}
}
