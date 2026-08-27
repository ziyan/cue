package onboarding

import (
	"net"
	"testing"

	"github.com/insomniacslk/dhcp/dhcpv4"
)

func discoverFrom(t *testing.T, hardware string) *dhcpv4.DHCPv4 {
	t.Helper()
	address, err := net.ParseMAC(hardware)
	if err != nil {
		t.Fatal(err)
	}
	request, err := dhcpv4.NewDiscovery(address)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

// A phone joining has to be given an address in the range, told the device is
// its router, and told the device is its name server -- that last one is what
// makes the setup page open by itself.
func TestAPhoneJoiningIsGivenAnAddressAndPointedAtTheDevice(t *testing.T) {
	book := &leaseBook{given: map[string]net.IP{}}

	reply, err := book.answer(discoverFrom(t, "02:00:00:00:00:01"))
	if err != nil {
		t.Fatalf("answering a discovery: %s", err)
	}
	if reply.MessageType() != dhcpv4.MessageTypeOffer {
		t.Errorf("a discovery was answered with %s, want an offer", reply.MessageType())
	}

	offered := reply.YourIPAddr
	if !inRange(offered) {
		t.Errorf("the address offered is %s, outside %s to %s", offered, firstOffered, lastOffered)
	}
	if router := reply.Router(); len(router) != 1 || !router[0].Equal(DeviceAddress) {
		t.Errorf("the router offered is %v, want just %s", router, DeviceAddress)
	}
	if servers := reply.DNS(); len(servers) != 1 || !servers[0].Equal(DeviceAddress) {
		t.Errorf("the name server offered is %v, want just %s -- without that the "+
			"setup page never opens by itself", servers, DeviceAddress)
	}
	if got := reply.IPAddressLeaseTime(0); got != leaseTime {
		t.Errorf("the lease is %s, want %s", got, leaseTime)
	}
	if mask := reply.SubnetMask(); mask.String() != SubnetMask.String() {
		t.Errorf("the mask offered is %v, want %v", mask, SubnetMask)
	}
}

// A phone that asks twice must get the same address. If it changed, the page
// somebody is typing into would be lost mid-setup.
func TestTheSamePhoneIsGivenTheSameAddressTwice(t *testing.T) {
	book := &leaseBook{given: map[string]net.IP{}}

	first, err := book.answer(discoverFrom(t, "02:00:00:00:00:01"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := book.answer(discoverFrom(t, "02:00:00:00:00:01"))
	if err != nil {
		t.Fatal(err)
	}
	if !first.YourIPAddr.Equal(again.YourIPAddr) {
		t.Errorf("the same phone was offered %s and then %s", first.YourIPAddr, again.YourIPAddr)
	}
}

func TestTwoPhonesAreGivenDifferentAddresses(t *testing.T) {
	book := &leaseBook{given: map[string]net.IP{}}

	first, err := book.answer(discoverFrom(t, "02:00:00:00:00:01"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := book.answer(discoverFrom(t, "02:00:00:00:00:02"))
	if err != nil {
		t.Fatal(err)
	}
	if first.YourIPAddr.Equal(second.YourIPAddr) {
		t.Errorf("two phones were both offered %s", first.YourIPAddr)
	}
}

// The range is small on purpose, and running out has to be an error rather
// than an address outside it -- which would be an address that reaches nothing
// and a phone that appears to join and then cannot load anything.
func TestRunningOutOfAddressesIsRefusedRatherThanOverflowing(t *testing.T) {
	book := &leaseBook{given: map[string]net.IP{}}

	count := int(lastOffered.To4()[3]) - int(firstOffered.To4()[3]) + 1
	for index := 0; index < count; index++ {
		address, err := book.addressFor(net.HardwareAddr{2, 0, 0, 0, 0, byte(index)}.String())
		if err != nil {
			t.Fatalf("ran out after %d of %d addresses: %s", index, count, err)
		}
		if !inRange(address) {
			t.Fatalf("address %d is %s, outside the range", index, address)
		}
	}
	if _, err := book.addressFor("02:00:00:00:ff:ff"); err == nil {
		t.Error("a phone was given an address after the range was used up")
	}
}

// A request is answered with an acknowledgement, not another offer, or the
// phone never finishes and sits there without an address.
func TestARequestIsAcknowledged(t *testing.T) {
	book := &leaseBook{given: map[string]net.IP{}}

	discovery := discoverFrom(t, "02:00:00:00:00:01")
	request, err := dhcpv4.NewRequestFromOffer(mustOffer(t, book, discovery))
	if err != nil {
		t.Fatal(err)
	}
	reply, err := book.answer(request)
	if err != nil {
		t.Fatalf("answering a request: %s", err)
	}
	if reply.MessageType() != dhcpv4.MessageTypeAck {
		t.Errorf("a request was answered with %s, want an acknowledgement", reply.MessageType())
	}
}

func mustOffer(t *testing.T, book *leaseBook, discovery *dhcpv4.DHCPv4) *dhcpv4.DHCPv4 {
	t.Helper()
	offer, err := book.answer(discovery)
	if err != nil {
		t.Fatal(err)
	}
	return offer
}

func inRange(address net.IP) bool {
	four := address.To4()
	if four == nil {
		return false
	}
	return four[0] == 192 && four[1] == 168 && four[2] == 216 &&
		four[3] >= firstOffered.To4()[3] && four[3] <= lastOffered.To4()[3]
}
