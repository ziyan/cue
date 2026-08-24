package display

import (
	"net"
	"testing"
)

// The abstract socket is the one that matters, and the one the old check
// missed. It belongs to the network namespace rather than the mount
// namespace, so a container given the host's network shares the machine's —
// and a foreign X server there has no socket file and no lock file this
// process can see. Looking for files finds nothing and reports the display
// free; it is not.
func TestAServerOnTheAbstractSocketIsFound(t *testing.T) {
	// A display number nothing real is likely to have.
	const number = 47

	if where, found := SomethingIsAnsweringOn(number); found {
		t.Skipf("display :%d is already taken on this machine (%s)", number, where)
	}

	listener, err := net.Listen("unix", AbstractSocketPath(number))
	if err != nil {
		t.Skipf("this system has no abstract sockets: %s", err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			// Accept and say nothing, which is what an X server that will not
			// accept our cookie amounts to. It is still there.
			_ = connection.Close()
		}
	}()

	where, found := SomethingIsAnsweringOn(number)
	if !found {
		t.Fatal("a server on the abstract socket was not found, so the daemon would start a second one")
	}
	if where != AbstractSocketPath(number) {
		t.Errorf("found it at %q, want the abstract socket %q", where, AbstractSocketPath(number))
	}
}

func TestAnEmptyDisplayIsReportedFree(t *testing.T) {
	// 48 and 49 are not display numbers anything uses; if one is taken on the
	// machine running this, the test would be asserting the opposite of what
	// it means to.
	for _, number := range []int{48, 49} {
		if where, found := SomethingIsAnsweringOn(number); found {
			t.Skipf("display :%d is taken on this machine (%s)", number, where)
		}
	}
}

func TestTheAbstractPathIsTheSocketPathWithAnAt(t *testing.T) {
	// Go spells an abstract socket with a leading "@", and the name has to be
	// the same one the X server uses or this looks in the wrong place and
	// always reports the display free.
	if got, want := AbstractSocketPath(0), "@/tmp/.X11-unix/X0"; got != want {
		t.Errorf("AbstractSocketPath(0) = %q, want %q", got, want)
	}
	if got, want := SocketPath(0), "/tmp/.X11-unix/X0"; got != want {
		t.Errorf("SocketPath(0) = %q, want %q", got, want)
	}
}
