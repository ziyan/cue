package fleet

import "net"

// netConn names the interface the test's dialler returns, so that the
// signature reads as what it is rather than as a package-qualified mouthful.
type netConn = net.Conn
