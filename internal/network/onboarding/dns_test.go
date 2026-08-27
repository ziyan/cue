package onboarding

import (
	"context"
	"net"
	"testing"
	"time"
)

// startDNS runs the responder on a free loopback port and returns where to ask.
func startDNS(t *testing.T) string {
	t.Helper()

	// Port 0 lets the kernel choose, so tests can run at the same time as a
	// real DNS server and as each other.
	probe, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on the loopback here: %s", err)
	}
	address := probe.LocalAddr().String()
	probe.Close()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ready := make(chan error, 1)
	go func() { ready <- ServeDNS(ctx, net.IPv4(192, 168, 216, 1), address) }()

	// Wait for it to be answering rather than merely started.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := ask(address, "captive.apple.com", 1); err == nil {
			return address
		}
		select {
		case err := <-ready:
			t.Fatalf("the DNS server stopped: %v", err)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the DNS server never answered")
	return ""
}

// Whatever a phone asks for, it has to be told this device, or it will not
// decide the network is captive and will never show the setup page.
func TestEveryNameAnswersWithThisDevice(t *testing.T) {
	address := startDNS(t)

	for _, name := range []string{
		"captive.apple.com",             // what an iPhone asks
		"connectivitycheck.gstatic.com", // what an Android asks
		"www.msftconnecttest.com",       // what a Windows laptop asks
		"a-name-nobody-has-registered.example",
	} {
		answer, err := ask(address, name, 1)
		if err != nil {
			t.Errorf("%s: %s", name, err)
			continue
		}
		if !answer.Equal(net.IPv4(192, 168, 216, 1)) {
			t.Errorf("%s answered %s, want 192.168.216.1", name, answer)
		}
	}
}

// A question of a kind this cannot answer must come back successful and empty.
// Refusing it outright makes some phones ask again in a loop instead of
// falling back to asking for an address.
func TestAQuestionOfAnotherKindIsAnsweredEmptyAndNotRefused(t *testing.T) {
	address := startDNS(t)

	const typeAAAA = 28
	reply, err := askRaw(address, "captive.apple.com", typeAAAA)
	if err != nil {
		t.Fatalf("asking for an IPv6 address: %s", err)
	}
	if code := reply[3] & 0x0f; code != 0 {
		t.Errorf("the reply carries error code %d; it should say there is no such record, not refuse", code)
	}
	if answers := int(reply[6])<<8 | int(reply[7]); answers != 0 {
		t.Errorf("the reply carries %d answers to a question that cannot be answered", answers)
	}
}

// The reply has to carry the identifier the question came with, or the phone
// will not match it to what it asked and will treat it as noise.
func TestTheReplyCarriesTheIdentifierItWasAskedWith(t *testing.T) {
	address := startDNS(t)

	query := buildQuery(0xbeef, "captive.apple.com", 1)
	reply, err := exchange(address, query)
	if err != nil {
		t.Fatal(err)
	}
	if got := int(reply[0])<<8 | int(reply[1]); got != 0xbeef {
		t.Errorf("the reply carries identifier %#x, want %#x", got, 0xbeef)
	}
	if reply[2]&0x80 == 0 {
		t.Error("the reply is not marked as a response")
	}
}

// A reply arriving as if it were a question must be ignored: answering one
// with a forged source address is how two servers are made to talk to each
// other for ever.
func TestAResponseIsNotAnswered(t *testing.T) {
	address := startDNS(t)

	query := buildQuery(0x1234, "captive.apple.com", 1)
	query[2] |= 0x80 // mark it as a response

	connection, err := net.Dial("udp4", address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Write(query); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buffer := make([]byte, 512)
	if _, err := connection.Read(buffer); err == nil {
		t.Error("a response was answered, which is a way to start a loop between two servers")
	}
}

func ask(address, name string, questionType int) (net.IP, error) {
	reply, err := askRaw(address, name, questionType)
	if err != nil {
		return nil, err
	}
	if len(reply) < 4 {
		return nil, errShort
	}
	return net.IPv4(reply[len(reply)-4], reply[len(reply)-3], reply[len(reply)-2], reply[len(reply)-1]), nil
}

func askRaw(address, name string, questionType int) ([]byte, error) {
	return exchange(address, buildQuery(0x4242, name, questionType))
}

func exchange(address string, query []byte) ([]byte, error) {
	connection, err := net.Dial("udp4", address)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	if _, err := connection.Write(query); err != nil {
		return nil, err
	}
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	buffer := make([]byte, 512)
	read, err := connection.Read(buffer)
	if err != nil {
		return nil, err
	}
	return buffer[:read], nil
}

// buildQuery writes the question a phone would ask.
func buildQuery(identifier int, name string, questionType int) []byte {
	query := []byte{byte(identifier >> 8), byte(identifier), 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, label := range splitName(name) {
		query = append(query, byte(len(label)))
		query = append(query, label...)
	}
	query = append(query, 0)
	return append(query, byte(questionType>>8), byte(questionType), 0, 1)
}

func splitName(name string) []string {
	var labels []string
	start := 0
	for index := 0; index <= len(name); index++ {
		if index == len(name) || name[index] == '.' {
			if index > start {
				labels = append(labels, name[start:index])
			}
			start = index + 1
		}
	}
	return labels
}

var errShort = net.UnknownNetworkError("the reply is too short to hold an address")
