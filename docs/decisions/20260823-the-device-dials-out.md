# A managed device dials out and serves the same interface it serves locally

- Status: reverted (2026-08-25) — the code is gone; see the note at the end
- Date: 2026-08-23
- Deciders: Ziyan Zhou

## Context

A fleet of screens is spread across buildings, behind routers nobody
administers, on networks that hand out addresses by DHCP and change them. A
management service has to reach each one — to see what it is showing, to change
it, to watch the screen when somebody says it looks wrong.

The obvious design is for the service to connect to the device. That needs a
port opened on every firewall in front of every screen, a stable address for
each, and a second authenticated interface on the device for the service to
use.

## Decision

The device dials out. It holds one WebSocket open to the service and runs a
yamux session over it — many independent streams on one socket, as HTTP/2 does.
The service opens a stream when it wants something and speaks ordinary HTTP on
it.

What answers those requests is the *same* `http.Handler` that serves the local
web interface, wrapped only to mark that the request arrived through the
tunnel. Requests so marked are treated as signed in, because the tunnel was
authenticated by the device's own credential before a byte of HTTP crossed it,
which is more than a password proves.

Enrolment is a token used once, exchanged for a credential stored under the
state directory. The token is then cleared from the configuration file.

## Consequences

- Nothing is opened on any firewall, and no port is published. A screen in a
  shop behind a domestic router is reachable without touching that router.
- The access a management service has is exactly the access documented in
  `docs/reference/api.md`. There is no second, more privileged interface to
  audit separately, and no way for the service to do something an operator
  standing in front of the screen could not.
- Unenrolling is deleting one file, and it can be done from the device. A
  device cannot be held by a service that has stopped being trusted.
- Clearing the token after use means a token that leaks — in a provisioning
  script, in a shell history — cannot be used to enrol something else claiming
  to be this device.
- The whole of it is inert unless an operator turns it on. A device that is
  never enrolled makes no outbound connection of its own accord, and a test
  asserts that.
- The service is a separate, closed-source project. What is here is the client
  half and the protocol it speaks, which is small enough to reimplement: an
  enrolment endpoint that returns a secret, and a WebSocket that carries a
  yamux session whose streams are HTTP.

## Reverted, 2026-08-25

The fleet enrolment was taken out. `internal/fleet`, the `fleet:` configuration
section, the two API endpoints and the card in the interface are all gone, and
a device now makes no outbound connection of its own accord at all.

This record is kept rather than deleted because the reasoning still holds and
the problem has not gone away: a screen behind a router nobody administers is
still hard to reach, and if that is solved again this is the shape it should
take. What went with the code is worth naming — the tunnel carried an
authentication bypass, where a request arriving on it was treated as signed in
without a password. That was sound while the tunnel authenticated the device
first, and it is exactly the sort of thing that should not be left behind as
dead code once the thing justifying it is removed. It was removed with it.
