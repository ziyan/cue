package network

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/vishvananda/netlink"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/atomicfile"
)

// Apply puts one interface into the state the configuration asks for: brought
// up, addressed either by asking a server or by being told, and with a way out
// to the rest of the world.
//
// It is written to be run repeatedly. An interface that is already right is
// left alone, which matters because it runs on a timer: a cable pulled out and
// put back, or a wireless network that came and went, is fixed without anybody
// doing anything, and an interface nobody has touched is not disturbed.
func Apply(ctx context.Context, settings *config.Interface) error {
	link, err := netlink.LinkByName(settings.Name)
	if err != nil {
		return fmt.Errorf("network: there is no interface called %q on this machine: %w", settings.Name, err)
	}

	if link.Attrs().Flags&net.FlagUp == 0 {
		if err := netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("network: cannot bring %s up: %w", settings.Name, err)
		}
		log.Noticef("brought %s up", settings.Name)
	}

	switch settings.Method {
	case config.AddressMethodStatic:
		return applyStatic(link, settings)
	case config.AddressMethodDHCP, "":
		return applyDHCP(ctx, link, settings)
	default:
		return fmt.Errorf("network: %q is not a way of getting an address", settings.Method)
	}
}

// applyStatic gives the interface exactly the address it was told, and nothing
// else. Addresses it was not told about are removed: a static configuration
// that leaves an old address behind is how a machine ends up answering on two
// addresses, one of which nobody knows about.
func applyStatic(link netlink.Link, settings *config.Interface) error {
	wanted, err := netlink.ParseAddr(settings.Address)
	if err != nil {
		return fmt.Errorf("network: %q is not an address and prefix like 192.0.2.10/24: %w", settings.Address, err)
	}

	existing, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("network: cannot read the addresses of %s: %w", settings.Name, err)
	}

	found := false
	for index := range existing {
		address := existing[index]
		if address.IPNet.String() == wanted.IPNet.String() {
			found = true
			continue
		}
		if err := netlink.AddrDel(link, &address); err != nil {
			log.Warningf("cannot remove the old address %s from %s: %s", address.IPNet, settings.Name, err)
		}
	}

	if !found {
		if err := netlink.AddrAdd(link, wanted); err != nil {
			return fmt.Errorf("network: cannot give %s the address %s: %w", settings.Name, settings.Address, err)
		}
		log.Noticef("gave %s the address %s", settings.Name, settings.Address)
	}

	if settings.Gateway != "" {
		if err := setDefaultRoute(link, settings.Gateway); err != nil {
			return err
		}
	}
	if len(settings.Nameservers) > 0 {
		if err := writeNameservers(settings.Nameservers, settings.SearchDomain); err != nil {
			return err
		}
	}
	return nil
}

// applyDHCP asks a server for an address, unless the interface already has one
// that is not a self-assigned link-local address.
//
// The lease is not renewed on a timer here. A display is on for months and a
// lease is usually hours, so this runs again when the reconciliation loop
// comes round and finds the address gone — which is what happens when a lease
// expires and nothing renewed it.
func applyDHCP(ctx context.Context, link netlink.Link, settings *config.Interface) error {
	if hasUsableAddress(link) {
		return nil
	}
	if link.Attrs().OperState != netlink.OperUp {
		// No cable, or no wireless network joined. Asking would time out.
		return nil
	}

	log.Noticef("asking for an address on %s", settings.Name)

	client, err := nclient4.New(settings.Name)
	if err != nil {
		return fmt.Errorf("network: cannot ask for an address on %s: %w", settings.Name, err)
	}
	defer func() { _ = client.Close() }()

	requestContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	lease, err := client.Request(requestContext)
	if err != nil {
		return fmt.Errorf("network: nothing answered on %s: %w", settings.Name, err)
	}

	return applyLease(link, settings, lease.ACK)
}

// applyLease puts what a server offered onto the interface.
func applyLease(link netlink.Link, settings *config.Interface, lease *dhcpv4.DHCPv4) error {
	mask := lease.SubnetMask()
	if mask == nil {
		mask = net.CIDRMask(24, 32)
	}
	address := &netlink.Addr{IPNet: &net.IPNet{IP: lease.YourIPAddr, Mask: mask}}

	if err := netlink.AddrReplace(link, address); err != nil {
		return fmt.Errorf("network: cannot give %s the address it was offered: %w", settings.Name, err)
	}
	log.Noticef("%s was given the address %s", settings.Name, address.IPNet)

	// The first router offered is the one used. A lease can name several, but
	// a screen needs one way out, and a second default route would only make
	// which one it took depend on the order the kernel happened to store them.
	if routers := lease.Router(); len(routers) > 0 {
		if err := setDefaultRoute(link, routers[0].String()); err != nil {
			log.Warningf("%s", err)
		}
	}

	var nameservers []string
	for _, server := range lease.DNS() {
		nameservers = append(nameservers, server.String())
	}
	if len(settings.Nameservers) > 0 {
		nameservers = settings.Nameservers
	}
	if len(nameservers) > 0 {
		if err := writeNameservers(nameservers, lease.DomainName()); err != nil {
			log.Warningf("%s", err)
		}
	}
	return nil
}

// hasUsableAddress reports whether the interface already has an address worth
// keeping. A 169.254 address is what a machine gives itself when nothing
// answered, so it does not count.
func hasUsableAddress(link netlink.Link) bool {
	addresses, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return false
	}
	for _, address := range addresses {
		if address.IP.IsLinkLocalUnicast() || address.IP.IsLoopback() {
			continue
		}
		return true
	}
	return false
}

// setDefaultRoute points the way out of the network through one interface.
func setDefaultRoute(link netlink.Link, gateway string) error {
	address := net.ParseIP(gateway)
	if address == nil {
		return fmt.Errorf("network: %q is not an address", gateway)
	}

	route := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Gw:        address,
		Dst:       nil,
	}
	if err := netlink.RouteReplace(route); err != nil {
		return fmt.Errorf("network: cannot route through %s: %w", gateway, err)
	}
	return nil
}

// writeNameservers rewrites the resolver configuration.
//
// It is written whole rather than edited, because the alternative is parsing a
// file that half a dozen programs believe they own. Nothing else in this image
// writes it.
func writeNameservers(nameservers []string, searchDomain string) error {
	var builder strings.Builder
	builder.WriteString("# Written by cue. Everything here comes from the network section\n")
	builder.WriteString("# of cue.yaml, or from the server that gave this machine its address.\n\n")

	if searchDomain != "" {
		fmt.Fprintf(&builder, "search %s\n", searchDomain)
	}
	for _, server := range nameservers {
		if net.ParseIP(server) == nil {
			continue
		}
		fmt.Fprintf(&builder, "nameserver %s\n", server)
	}

	if err := atomicfile.Write(resolvConfFilename, []byte(builder.String()), 0o644); err != nil {
		// A read-only /etc/resolv.conf is what a container gets when the
		// runtime bind-mounts one, and it is not worth failing over: the
		// addresses are already applied and the machine can be reached.
		if os.IsPermission(err) || strings.Contains(err.Error(), "device or resource busy") {
			return fmt.Errorf("network: the addresses are set, but %s cannot be written "+
				"(the container runtime mounted it); name resolution will use whatever it holds", resolvConfFilename)
		}
		return err
	}
	return nil
}
