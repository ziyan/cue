# Security notes

What this program is exposed to, what protects it, and what is deliberately
not protected. Written for somebody deciding whether to put it on a network.

## What it is

A device on a local network that renders arbitrary web pages full screen, holds
credentials for the systems it displays, and offers a remote view and control
of its screen.

## The trust boundary

Everything inside the container trusts everything else inside it. In
particular, the browser's DevTools port and the VNC server both listen on the
loopback address with no authentication, and anything already running on that
machine can reach them. That is accepted: anything on that machine can also
read `/etc/cue/cue.yaml`, which holds the same credentials.

The boundary that matters is the network. Two things cross it:

- **The web interface**, on port 8080, behind a session cookie and an argon2id
  password hash.
- **The VNC server**, which by default does not cross it at all: it listens on
  `127.0.0.1` and the interface bridges it over an authenticated WebSocket. An
  operator can move it onto the network, and the daemon logs a warning at every
  start if they do so without a password.

## What protects the interface

- **argon2id**, with the RFC 9106 second recommended parameters scaled for a
  device with little memory (64 MB, two passes). A wrong password costs the
  same as a right one and both take about a tenth of a second, which makes
  guessing over a network impractical without a rate limiter to maintain.
- **A signed session cookie**, `HttpOnly` and `SameSite=Lax`, carrying only an
  issue time and an HMAC over it. There is no session store, because a device
  that reboots would lose one and there is exactly one account.
- **An origin check on the VNC WebSocket.** Without it, a page anywhere on the
  internet, open in a browser that holds a session cookie for the device, could
  watch and drive the screen. The check allows the request's own host and
  anything in `web.trustedOrigins`.

`Secure` is deliberately not set on the cookie. These devices are reached over
plain HTTP on a local network and no certificate authority will issue for an
address on one; setting it would stop the interface working entirely. Anybody
positioned to read that traffic is already on the network the screen is on.

## The browser

Chromium runs as an unprivileged account with its own sandbox enabled. This is
the reason `browser.user` exists: Chromium refuses to enable the sandbox as
root, and a kiosk renders whatever the network serves it. Turning the sandbox
off is a real reduction in safety and is spelled out as its own setting rather
than hidden in a list of arguments.

The sandbox also needs `CAP_SYS_ADMIN` on the container, and that deserves an
honest word because it looks like the opposite of a security measure.

Chromium's sandbox puts each renderer in its own process and network
namespace. A container's default seccomp policy refuses to create those unless
the container holds that capability, so without it the browser does not start.
Granting it does not disable seccomp — the policy is capability-aware and
every other syscall stays filtered — and it can be confirmed from outside:
`/proc/<renderer>/ns/pid` and `ns/net` differ from the container's.

The trade is worth making here. This container is already root, with the
graphics device, a console, several capabilities and host networking; that is
what driving a screen costs, and none of it is exposed to the internet. The
browser is the one component that processes anything from the internet, so it
is the one worth isolating. Refusing the capability and keeping
`browser.sandbox: true` gets a screen that shows nothing at all, which is why
the daemon recognises that exact failure and says which of the two things to
change.

`browser.ignoreCertificateErrors` is off by default. Turning it on removes the
protection TLS was there to give, and it exists because appliances on private
networks have self-signed certificates.

## Credentials for other systems

A login rule stores a working password for another system. It is kept in
`/etc/cue/cue.yaml`, mode 0600. The `config.Secret` type renders as a
placeholder in every log line and every JSON response, so it does not leak into
a support bundle or a screenshot of the interface. `make check-secrets` refuses
to let one into the repository, and runs in CI.

Give the device an account with as little access as the display needs — a
read-only one, if the system has such a thing.

## The container's privileges

The X server needs the graphics device, a virtual console, and the capability
to switch it; chronyd needs the capability to set the clock.
`deploy/docker-compose.yml` grants exactly those. `--privileged` will also work
and is worth trying once to find out whether a problem is a permission at all,
but a device left that way can reach everything on the host.

## What is not protected

- **No rate limiting on sign-in.** The hash is the defence.
- **No audit log.** Signing in and being refused are logged; changing the
  configuration is not attributed.
- **No transport security.** Put a reverse proxy in front of it if the network
  is not one you trust, and add its origin to `web.trustedOrigins`.
- **No second factor**, and none is planned.
