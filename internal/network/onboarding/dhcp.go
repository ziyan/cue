package onboarding

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"
)

// The address the device gives itself on the setup network, and the range it
// hands out.
//
// 192.168.216.0/24 is chosen to be unlikely to collide with the network the
// person is about to join. A phone that ends up on the same subnet on both
// sides of the switch-over gets confused about which one to route to, so the
// ranges consumer routers hand out by default are the ones to stay away from.
// No consumer router picks this one.
var (
	// DeviceAddress is where the device answers on the setup network: DHCP,
	// DNS and the setup page are all here.
	DeviceAddress = net.IPv4(192, 168, 216, 1)

	// SubnetMask covers the range below.
	SubnetMask = net.IPv4Mask(255, 255, 255, 0)

	firstOffered = net.IPv4(192, 168, 216, 10)
	lastOffered  = net.IPv4(192, 168, 216, 60)
)

// leaseTime is deliberately short. This network exists for minutes, and a
// phone holding an hour-long lease for a network that has gone is a phone that
// takes longer to settle on the real one afterwards.
const leaseTime = 10 * time.Minute

// ServeDHCP hands addresses to whatever joins the setup network, until the
// context is cancelled.
//
// It is not a general purpose DHCP server and must never face a real network:
// it answers every request it hears, which on somebody's office network would
// fight their real server and break other people's machines. It binds to the
// setup interface alone, and it stops when setup stops.
func ServeDHCP(ctx context.Context, interfaceName string) error {
	leases := &leaseBook{given: map[string]net.IP{}}

	server, err := server4.NewServer(interfaceName, nil, func(connection net.PacketConn, peer net.Addr, request *dhcpv4.DHCPv4) {
		reply, err := leases.answer(request)
		if err != nil {
			log.Debugf("cannot answer a DHCP request from %s: %s", request.ClientHWAddr, err)
			return
		}
		if reply == nil {
			return
		}
		if _, err := connection.WriteTo(reply.ToBytes(), peer); err != nil {
			log.Debugf("cannot send a DHCP reply to %s: %s", peer, err)
		}
	})
	if err != nil {
		return fmt.Errorf("onboarding: cannot hand out addresses on %s: %w", interfaceName, err)
	}
	log.Noticef("handing out addresses from %s to %s on %s, for setting this device up",
		firstOffered, lastOffered, interfaceName)

	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	if err := server.Serve(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// leaseBook remembers which address each phone was given, so that a phone
// asking twice gets the same one. Without that, a phone that renews mid-setup
// changes address and loses the page it was filling in.
type leaseBook struct {
	mutex sync.Mutex
	given map[string]net.IP
	next  int
}

func (self *leaseBook) answer(request *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, error) {
	reply, err := dhcpv4.NewReplyFromRequest(request)
	if err != nil {
		return nil, err
	}

	address, err := self.addressFor(request.ClientHWAddr.String())
	if err != nil {
		return nil, err
	}

	reply.YourIPAddr = address
	reply.ServerIPAddr = DeviceAddress
	reply.UpdateOption(dhcpv4.OptServerIdentifier(DeviceAddress))
	reply.UpdateOption(dhcpv4.OptSubnetMask(SubnetMask))
	reply.UpdateOption(dhcpv4.OptIPAddressLeaseTime(leaseTime))
	// The device is the router and the name server. Being the name server is
	// what lets it answer every lookup with itself, which is what makes the
	// phone open the setup page by itself.
	reply.UpdateOption(dhcpv4.OptRouter(DeviceAddress))
	reply.UpdateOption(dhcpv4.OptDNS(DeviceAddress))

	switch request.MessageType() {
	case dhcpv4.MessageTypeDiscover:
		reply.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeOffer))
	case dhcpv4.MessageTypeRequest:
		reply.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeAck))
	case dhcpv4.MessageTypeRelease, dhcpv4.MessageTypeDecline:
		self.forget(request.ClientHWAddr.String())
		return nil, nil
	default:
		// Nothing else is part of getting a phone onto this network for a few
		// minutes, and answering messages this does not understand is how a
		// small server starts behaving strangely.
		return nil, nil
	}
	return reply, nil
}

func (self *leaseBook) addressFor(client string) (net.IP, error) {
	self.mutex.Lock()
	defer self.mutex.Unlock()

	if address, found := self.given[client]; found {
		return address, nil
	}

	first := int(firstOffered.To4()[3])
	last := int(lastOffered.To4()[3])
	if first+self.next > last {
		return nil, fmt.Errorf("onboarding: all %d setup addresses are taken", last-first+1)
	}
	address := net.IPv4(192, 168, 216, byte(first+self.next))
	self.next++
	self.given[client] = address
	return address, nil
}

func (self *leaseBook) forget(client string) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	delete(self.given, client)
}
