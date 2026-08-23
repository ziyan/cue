// Package network reports and configures the machine's network interfaces:
// which ones there are, what addresses they have, which wireless networks are
// within reach, and which one to join.
//
// It exists because of where these screens go. A display is carried to a room,
// plugged in, and switched on, and at that moment it has no network and no
// keyboard — so the one thing it must be able to do is join the wireless
// network of the room it is in. Until it can, nothing else about it can be
// configured at all.
//
// Everything here needs the machine's own network namespace, which is why the
// container runs in it. In any other namespace this package can see a
// container's private interfaces and nothing about the machine, and it says so
// rather than pretending.
package network

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	"github.com/op/go-logging"
	"github.com/vishvananda/netlink"
)

var log = logging.MustGetLogger("network")

// Interface is one network interface as the daemon reports it.
type Interface struct {
	Name string `json:"name"`

	// Kind is "ethernet", "wireless" or "other". It decides what the
	// interface page offers: only a wireless one can be asked to join a
	// network.
	Kind string `json:"kind"`

	MAC string `json:"mac"`
	MTU int    `json:"mtu"`

	// Up is whether the interface is administratively enabled; Carrier is
	// whether a cable is in it, or a wireless network is joined. Both matter:
	// "up but no carrier" is an unplugged cable, and is the single most
	// common reason a screen has no network.
	Up      bool `json:"up"`
	Carrier bool `json:"carrier"`

	// Addresses are in the usual form, for example "192.0.2.10/24".
	Addresses []string `json:"addresses"`

	Gateway     string   `json:"gateway"`
	Nameservers []string `json:"nameservers"`

	// Wireless is present only for a wireless interface.
	Wireless *WirelessStatus `json:"wireless,omitempty"`

	// Statistics are what the interface has carried since it came up.
	ReceivedBytes    uint64 `json:"receivedBytes"`
	TransmittedBytes uint64 `json:"transmittedBytes"`
}

// WirelessStatus is what a wireless interface is doing.
type WirelessStatus struct {
	// State is wpa_supplicant's own word for it: COMPLETED when joined,
	// SCANNING, DISCONNECTED, and so on.
	State string `json:"state"`

	SSID  string `json:"ssid"`
	BSSID string `json:"bssid"`

	// SignalStrength is in decibel-milliwatts, so about -30 next to the
	// access point and about -90 at the edge of usable.
	SignalStrength int `json:"signalStrength"`

	Frequency int `json:"frequency"`
}

// WirelessNetwork is one network found by a scan.
type WirelessNetwork struct {
	SSID           string `json:"ssid"`
	BSSID          string `json:"bssid"`
	SignalStrength int    `json:"signalStrength"`
	Frequency      int    `json:"frequency"`

	// Security is what joining it needs: "open", "wpa-psk", or "enterprise"
	// for the ones this cannot join.
	Security string `json:"security"`
}

// Manageable reports whether this process can see and change the machine's own
// interfaces, rather than a container's private ones.
//
// The test is deliberately about what is visible rather than about
// capabilities: a container in its own network namespace has interfaces, and
// they are real, and they are not the machine's. Reporting those as the
// device's network would be worse than reporting nothing.
func Manageable() (bool, string) {
	links, err := netlink.LinkList()
	if err != nil {
		return false, fmt.Sprintf("the network interfaces cannot be listed: %s", err)
	}

	for _, link := range links {
		name := link.Attrs().Name
		if name == "lo" {
			continue
		}
		// A veth is one end of a pair, and the far end is the host. It is the
		// signature of a container with a network namespace of its own.
		if link.Type() == "veth" {
			return false, "this container has a network namespace of its own, so it can only see its own " +
				"interfaces; run it with the host's network to manage the machine's"
		}
		return true, ""
	}
	return false, "this machine reports no network interfaces at all"
}

// Interfaces lists the machine's interfaces, with their addresses and state.
func Interfaces() ([]Interface, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("network: cannot list the interfaces: %w", err)
	}

	gateways := defaultGateways()
	nameservers := readNameservers()

	interfaces := make([]Interface, 0, len(links))
	for _, link := range links {
		attributes := link.Attrs()
		if attributes.Name == "lo" {
			continue
		}

		current := Interface{
			Name:             attributes.Name,
			Kind:             kindOf(attributes.Name, link.Type()),
			MAC:              attributes.HardwareAddr.String(),
			MTU:              attributes.MTU,
			Up:               attributes.Flags&net.FlagUp != 0,
			Carrier:          attributes.OperState == netlink.OperUp,
			Gateway:          gateways[attributes.Name],
			Nameservers:      nameservers,
			ReceivedBytes:    statistic(attributes, true),
			TransmittedBytes: statistic(attributes, false),
		}

		addresses, err := netlink.AddrList(link, netlink.FAMILY_V4)
		if err == nil {
			for _, address := range addresses {
				current.Addresses = append(current.Addresses, address.IPNet.String())
			}
		}

		if current.Kind == KindWireless {
			if status, err := wirelessStatus(attributes.Name); err == nil {
				current.Wireless = status
			}
		}

		interfaces = append(interfaces, current)
	}

	sort.Slice(interfaces, func(first, second int) bool {
		return interfaces[first].Name < interfaces[second].Name
	})
	return interfaces, nil
}

// The kinds an interface can be.
const (
	KindEthernet = "ethernet"
	KindWireless = "wireless"
	KindOther    = "other"
)

// kindOf decides what an interface is. The reliable test for wireless is the
// directory the kernel creates for it, not the name: a name beginning with
// "wl" is a convention and a machine may not follow it.
func kindOf(name, linkType string) string {
	if _, err := os.Stat("/sys/class/net/" + name + "/wireless"); err == nil {
		return KindWireless
	}
	if _, err := os.Stat("/sys/class/net/" + name + "/phy80211"); err == nil {
		return KindWireless
	}
	switch linkType {
	case "device", "veth", "bridge":
		return KindEthernet
	}
	if strings.HasPrefix(name, "en") || strings.HasPrefix(name, "eth") {
		return KindEthernet
	}
	return KindOther
}

func statistic(attributes *netlink.LinkAttrs, received bool) uint64 {
	if attributes.Statistics == nil {
		return 0
	}
	if received {
		return attributes.Statistics.RxBytes
	}
	return attributes.Statistics.TxBytes
}

// defaultGateways maps an interface name to the router it reaches the rest of
// the world through, which is the thing an operator actually wants to see.
func defaultGateways() map[string]string {
	gateways := map[string]string{}

	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return gateways
	}
	for _, route := range routes {
		if route.Dst != nil && !route.Dst.IP.IsUnspecified() {
			continue
		}
		if route.Gw == nil {
			continue
		}
		link, err := netlink.LinkByIndex(route.LinkIndex)
		if err != nil {
			continue
		}
		gateways[link.Attrs().Name] = route.Gw.String()
	}
	return gateways
}

// readNameservers reads the resolver configuration. It is machine-wide rather
// than per-interface, which is a simplification the resolver itself makes.
func readNameservers() []string {
	content, err := os.ReadFile(resolvConfFilename)
	if err != nil {
		return nil
	}

	var nameservers []string
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			nameservers = append(nameservers, fields[1])
		}
	}
	return nameservers
}

const resolvConfFilename = "/etc/resolv.conf"
