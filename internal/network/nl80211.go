package network

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// This file asks the kernel what a wireless radio is capable of.
//
// It does that by speaking nl80211 -- the kernel's wireless control protocol,
// carried over generic netlink -- rather than by running "iw" and reading what
// it prints. The command line tool is a thin wrapper over these same messages,
// so parsing its output means depending on the wording of a program that is
// free to change it, in a container where the tool might not be installed at
// all. The kernel's numbers are an ABI and cannot change.
//
// The constants below are from /usr/include/linux/nl80211.h and are marked in
// that header as ABI that must not be reordered.

const (
	nl80211CommandGetWiphy = 1

	nl80211AttributeWiphy            = 1
	nl80211AttributeSupportedIftypes = 32

	// nl80211InterfaceTypeAP is the value that appears in the supported types
	// of a radio that can run a network of its own.
	nl80211InterfaceTypeAP = 3
)

// radioSupportsAccessPoint reports whether the radio behind this interface can
// run a network of its own, rather than only joining somebody else's.
//
// Most laptop and USB radios can. Some cheap ones cannot, and a device with
// one of those must never be told to try: the failure happens deep inside
// wpa_supplicant and reads as a driver error that means nothing to whoever is
// standing in front of the screen.
func radioSupportsAccessPoint(interfaceName string) (bool, error) {
	modes, err := supportedInterfaceModes(interfaceName)
	if err != nil {
		return false, err
	}
	return modes[nl80211InterfaceTypeAP], nil
}

// supportedInterfaceModes returns the set of modes the radio behind this
// interface supports, as nl80211 interface type numbers.
func supportedInterfaceModes(interfaceName string) (map[int]bool, error) {
	phy, err := phyIndexOf(interfaceName)
	if err != nil {
		return nil, err
	}

	family, err := genericFamily("nl80211")
	if err != nil {
		return nil, err
	}

	// Asking about one radio rather than dumping every one: a machine with two
	// radios would otherwise answer for both and there would be no telling
	// which answer belonged to which.
	request := nl.NewNetlinkRequest(int(family), unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	request.AddData(&nl.Genlmsg{Command: nl80211CommandGetWiphy, Version: 0})
	request.AddData(nl.NewRtAttr(nl80211AttributeWiphy, nl.Uint32Attr(uint32(phy))))

	messages, err := request.Execute(unix.NETLINK_GENERIC, 0)
	if err != nil {
		return nil, fmt.Errorf("network: asking the kernel about %s: %w", interfaceName, err)
	}

	modes := map[int]bool{}
	for _, message := range messages {
		if len(message) < nl.SizeofGenlmsg {
			continue
		}
		attributes, err := nl.ParseRouteAttr(message[nl.SizeofGenlmsg:])
		if err != nil {
			continue
		}
		for _, attribute := range attributes {
			if attribute.Attr.Type != nl80211AttributeSupportedIftypes {
				continue
			}
			// The supported types are a nested attribute where each entry's
			// *type* is the mode number and the value is empty. It is a set
			// written as a list, which is why nothing here reads a payload.
			nested, err := nl.ParseRouteAttr(attribute.Value)
			if err != nil {
				continue
			}
			for _, mode := range nested {
				modes[int(mode.Attr.Type)] = true
			}
		}
	}
	if len(modes) == 0 {
		return nil, fmt.Errorf("network: the kernel listed no modes for %s, "+
			"which means this is not a wireless interface", interfaceName)
	}
	return modes, nil
}

// genericFamily resolves a generic netlink family name to the number messages
// have to be addressed to. The numbers are assigned as modules load, so they
// differ between machines and between boots and must be asked for every time.
func genericFamily(name string) (uint16, error) {
	request := nl.NewNetlinkRequest(nl.GENL_ID_CTRL, unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	request.AddData(&nl.Genlmsg{Command: nl.GENL_CTRL_CMD_GETFAMILY, Version: nl.GENL_CTRL_VERSION})
	request.AddData(nl.NewRtAttr(nl.GENL_CTRL_ATTR_FAMILY_NAME, nl.ZeroTerminated(name)))

	messages, err := request.Execute(unix.NETLINK_GENERIC, 0)
	if err != nil {
		return 0, fmt.Errorf("network: looking up the %s netlink family: %w", name, err)
	}
	for _, message := range messages {
		if len(message) < nl.SizeofGenlmsg {
			continue
		}
		attributes, err := nl.ParseRouteAttr(message[nl.SizeofGenlmsg:])
		if err != nil {
			continue
		}
		for _, attribute := range attributes {
			if attribute.Attr.Type == nl.GENL_CTRL_ATTR_FAMILY_ID && len(attribute.Value) >= 2 {
				return native.Uint16(attribute.Value[:2]), nil
			}
		}
	}
	return 0, fmt.Errorf("network: the kernel does not know a netlink family called %s, "+
		"which on a machine with wireless hardware means the driver is not loaded", name)
}

// native is the byte order netlink uses, which is the machine's own.
var native = nl.NativeEndian()

// phyIndexOf finds which radio an interface belongs to.
//
// One radio can carry more than one interface -- that is how a machine runs an
// access point and a station at the same time -- so the capabilities belong to
// the radio and have to be asked for by its number, not by the interface's.
//
// The kernel puts that number in sysfs, and an interface with no such file is
// not a wireless interface at all, which is a useful thing to be able to say
// plainly rather than discovering it as a netlink error.
func phyIndexOf(interfaceName string) (int, error) {
	filename := filepath.Join("/sys/class/net", interfaceName, "phy80211", "index")
	content, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("network: %s is not a wireless interface", interfaceName)
		}
		return 0, fmt.Errorf("network: cannot tell which radio %s belongs to: %w", interfaceName, err)
	}
	index, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		return 0, fmt.Errorf("network: %s reports an unreadable radio number %q",
			interfaceName, strings.TrimSpace(string(content)))
	}
	return index, nil
}

const (
	nl80211CommandGetInterface = 5

	nl80211AttributeIfindex = 3
	nl80211AttributeIftype  = 5
	nl80211AttributeSSID    = 52
)

// associatedNetwork returns the network this interface is currently joined to,
// or an empty string when it is joined to nothing.
//
// This is asked of the kernel rather than of wpa_supplicant, because the whole
// point of asking is to find out whether some *other* program has the radio --
// in which case there is no control socket of ours to ask. It works from
// inside a container, where reading another program's /proc entry does not:
// the container has its own view of processes but shares the kernel's view of
// the machine's interfaces.
func associatedNetwork(interfaceName string) (string, error) {
	link, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return "", err
	}

	family, err := genericFamily("nl80211")
	if err != nil {
		return "", err
	}

	request := nl.NewNetlinkRequest(int(family), unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	request.AddData(&nl.Genlmsg{Command: nl80211CommandGetInterface, Version: 0})
	request.AddData(nl.NewRtAttr(nl80211AttributeIfindex, nl.Uint32Attr(uint32(link.Index))))

	messages, err := request.Execute(unix.NETLINK_GENERIC, 0)
	if err != nil {
		return "", err
	}
	for _, message := range messages {
		if len(message) < nl.SizeofGenlmsg {
			continue
		}
		attributes, err := nl.ParseRouteAttr(message[nl.SizeofGenlmsg:])
		if err != nil {
			continue
		}
		for _, attribute := range attributes {
			// The kernel reports a network name only while the interface is
			// actually associated with one, so its presence is the answer.
			if attribute.Attr.Type == nl80211AttributeSSID {
				return string(attribute.Value), nil
			}
		}
	}
	return "", nil
}
