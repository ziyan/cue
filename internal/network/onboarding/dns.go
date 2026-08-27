// Package onboarding serves the two things a phone needs the moment it joins
// the temporary network a device runs for its own setup: an address, and an
// answer to every name it looks up.
//
// Both are deliberately small and deliberately wrong. The DHCP server hands
// out a handful of addresses from one fixed range. The DNS server answers
// every question with this device, whatever was asked, which is what makes a
// phone decide the network is "captive" and open the setup page by itself.
// Neither is a general purpose server and neither should ever be reachable
// from a real network -- they bind to the setup interface's address only, and
// they stop when setup stops.
package onboarding

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("onboarding")

// ServeDNS answers every question with self, until the context is cancelled.
//
// A phone that has just joined a network immediately looks up a name belonging
// to its own vendor to find out whether the network really reaches the
// internet. Answering "that name is here" is step one of making it show the
// setup page; step two is the web server answering that probe with a redirect
// instead of what the phone expected.
//
// This answers A questions -- names to IPv4 addresses -- with self, and every
// other kind of question with an empty but successful answer. Refusing the
// others outright makes some phones retry in a loop; saying "that name exists
// and has no address of the kind you asked for" makes them move on and ask
// for an A instead.
func ServeDNS(ctx context.Context, self net.IP, address string) error {
	packets, err := net.ListenPacket("udp4", address)
	if err != nil {
		return fmt.Errorf("onboarding: cannot answer names on %s: %w", address, err)
	}
	log.Noticef("answering every name with %s on %s, for setting this device up", self, address)

	go func() {
		<-ctx.Done()
		_ = packets.Close()
	}()
	defer packets.Close()

	buffer := make([]byte, 512)
	for {
		read, from, err := packets.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		reply := answerWith(buffer[:read], self)
		if reply == nil {
			continue
		}
		if _, err := packets.WriteTo(reply, from); err != nil && ctx.Err() == nil {
			log.Debugf("cannot answer %s: %s", from, err)
		}
	}
}

// A DNS message begins with a twelve byte header: a request identifier, a
// field of flags, and four counts saying how many questions, answers,
// authority records and additional records follow.
const dnsHeaderSize = 12

// answerWith builds a reply saying that whatever was asked about lives at
// self. It returns nil for anything it cannot make sense of, because a
// malformed question deserves silence rather than a guess.
func answerWith(query []byte, self net.IP) []byte {
	if len(query) < dnsHeaderSize {
		return nil
	}
	flags := binary.BigEndian.Uint16(query[2:4])
	// The top bit set means this is itself a response. Answering a response
	// would be talking to ourselves, and with a forged source address it would
	// be a way to make two servers talk to each other for ever.
	if flags&0x8000 != 0 {
		return nil
	}
	if binary.BigEndian.Uint16(query[4:6]) != 1 {
		// Exactly one question. Nothing asks more, and handling the general
		// case here would be code that never runs.
		return nil
	}

	// The question is a name, then two bytes of type and two of class. The
	// name is a series of length-prefixed labels ending in a zero length.
	end := dnsHeaderSize
	for end < len(query) {
		length := int(query[end])
		if length == 0 {
			end++
			break
		}
		// The top two bits set marks a compression pointer. A question should
		// never contain one, and following it would mean parsing arbitrary
		// offsets, which is where DNS parsers go wrong.
		if length&0xc0 != 0 {
			return nil
		}
		end += length + 1
	}
	if end+4 > len(query) {
		return nil
	}
	questionType := binary.BigEndian.Uint16(query[end : end+2])
	end += 4
	question := query[dnsHeaderSize:end]

	reply := make([]byte, 0, len(query)+16)
	reply = append(reply, query[0:2]...) // the same identifier, so it is matched to the question
	// Flags: response, same opcode as the question, authoritative, recursion
	// available if it was asked for, no error.
	answerFlags := uint16(0x8000) | (flags & 0x7800) | 0x0400
	if flags&0x0100 != 0 {
		answerFlags |= 0x0180
	}
	reply = binary.BigEndian.AppendUint16(reply, answerFlags)
	reply = binary.BigEndian.AppendUint16(reply, 1) // one question, echoed back

	const typeA = 1
	if questionType != typeA {
		// It exists, but not with an address of the kind that was asked for.
		reply = binary.BigEndian.AppendUint16(reply, 0) // no answers
		reply = binary.BigEndian.AppendUint16(reply, 0) // no authority
		reply = binary.BigEndian.AppendUint16(reply, 0) // no additional
		return append(reply, question...)
	}

	reply = binary.BigEndian.AppendUint16(reply, 1) // one answer
	reply = binary.BigEndian.AppendUint16(reply, 0)
	reply = binary.BigEndian.AppendUint16(reply, 0)
	reply = append(reply, question...)

	// The answer names the same thing as the question, which is written as a
	// compression pointer to where the question's name already is: the two
	// high bits set, then the offset, which is always just past the header.
	reply = binary.BigEndian.AppendUint16(reply, 0xc000|dnsHeaderSize)
	reply = binary.BigEndian.AppendUint16(reply, typeA)
	reply = binary.BigEndian.AppendUint16(reply, 1)  // class IN
	reply = binary.BigEndian.AppendUint32(reply, 30) // a short life: this answer is a lie that must not outlive setup
	reply = binary.BigEndian.AppendUint16(reply, 4)  // four bytes of address
	return append(reply, self.To4()...)
}
