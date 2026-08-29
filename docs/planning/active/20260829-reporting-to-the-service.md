# Reporting to the service

## Purpose

A screen that is linked to an account should be visible from that account
without anybody walking up to it. Today linking ends with the device holding a
credential and doing nothing with it: the account can see that a device exists
and nothing about what it is showing.

After this work, a linked device holds a connection open to the service and
sends it a picture of the screen every so often. Somebody signed in to the
service sees what is on the wall, and sees it go stale rather than seeing
nothing when the device stops.

To see it working: link a device, wait a minute, and look at the device in the
service's own interface. The picture there should be what is on the screen.
Unlink, and it should stop being updated.

## Definitions

**The service** is the hosted side, at `https://cue.sh` unless `cue.yaml` says
otherwise. It is a different program in a different repository; this plan only
describes what this device sends it.

**The credential** is the string the service issued when this device was
linked. It lives in `cue.yaml` under `service.secret`, is never shown in the
web interface, and is what the device presents to prove who it is.

**The tunnel** is a websocket the device opens *outward* to the service and
holds open. It exists because a screen is usually behind a router with no way
in: nothing can connect to the device, so the device connects out and keeps the
line open. Everything the device sends the service, and anything the service
ever asks of the device, travels over it.

**A stream** is one conversation multiplexed over that tunnel. The tunnel
carries many, told apart by a **stream identifier** the opener chooses.

**A control frame** is a websocket *text* message carrying JSON that opens,
accepts, refuses or closes a stream. **A data frame** is a websocket *binary*
message carrying bytes for one stream. The message type is what tells them
apart -- there is no field saying which.

## What the service expects

Taken from the service's own code rather than guessed, and confirmed by the
people who wrote it.

The device opens `GET /api/v1/device/websocket` with the credential as a
bearer token. Nothing else in the tunnel is authenticated: the connection
carries the identity, and a bearer token on a request inside it is ignored.

Control frames are JSON text messages:

    {"stream":"...","kind":"open","host":"cue","port":80}
    {"stream":"...","kind":"opened"}
    {"stream":"...","kind":"failed","error":"..."}
    {"stream":"...","kind":"close"}

A data frame is a binary message: a big-endian `uint16` giving the length of
the stream identifier, then the identifier, then the payload. The payload has
no length of its own -- the websocket message boundary is the length.

The device chooses its own stream identifier and it must be unique within the
session. The only thing a device may open is host `cue`, port `80`, which is
the service itself; anything else is refused.

A stream is a whole HTTP conversation rather than one request, so keep-alive
works and reusing a stream saves opening another.

Once open, the device speaks ordinary HTTP/1.1 over the stream:

    POST /api/v1/device/screenshot   Content-Type: image/jpeg
    POST /api/v1/device/state        Content-Type: application/json
    GET  /api/v1/device/self

## Sizes and timing, which are the parts that bite

The service closes a stream that has been idle for **two minutes**, and gives
a request **30 seconds** to send its headers once it starts. A reporter whose
cadence is longer than two minutes will find its stream gone; this plan
reports more often than that and reopens when it finds one closed, rather than
assuming either.

A screenshot may be at most **4 MB** and must be `image/png`, `image/jpeg` or
`image/webp`. A lossless picture of a 4K screen measured 5.6 MB on the first
real device this ran on, which is over the limit as well as wasteful, so
reports are JPEG at the same reduced size the web interface already asks for.

The service's websocket accepts messages up to **1 MB**, and enforces it by
closing the connection rather than by failing the write. Go's HTTP client
writes a body through a 4 KB buffer, so this device will not produce a message
near the limit -- but nothing here may write a whole image in one call.

## Milestones

**Milestone 1: the connection.** A linked device dials the service, holds the
tunnel open, and reconnects with backoff when it drops. An unlinked device
does nothing at all. Verify by linking a device and watching the log say it
attached; unplug the network and watch it say it is retrying rather than
giving up; plug it back in and watch it attach again.

**Milestone 2: one request over the tunnel.** The device opens a stream and
asks `GET /api/v1/device/self` through it, confirming the answer is about
itself. Verify from the log: it should name the device the service thinks it
is, which is the same identifier the Service page shows.

**Milestone 3: screenshots.** The device sends a picture every so often, and
the service's interface shows it. Verify by looking at the device in the
service's interface and seeing what is on the screen.

**Milestone 4: state.** The device reports what it is showing, so the account
can see the playlist item and the version without a picture. Left last on
purpose: it is the least useful of the three and the easiest to add once the
stream works.

## Decision log

**2026-08-29 — Over the tunnel, not a public endpoint.** The service could
have exposed the screenshot report publicly, authenticated by the credential,
exactly as it does `/api/v1/device/self`. That would have worked within a day
and been the wrong shape: a public endpoint accepting megabyte uploads is a
much larger surface than a read of the caller's own identity, and it would
have existed only because the tunnel was unbuilt -- which is the kind of thing
nothing ever deletes once something depends on it. The tunnel is also needed
for the service's actual purpose, so it is not extra work, only earlier work.

**2026-08-29 — Inbound streams are not handled yet.** The service can open a
stream *to* a device, which is what the whole product is for. This plan
ignores those frames. It is safe today because nothing dials devices yet, and
a device that ignores an inbound open only makes that dial time out. It is
strictly additive to build later, and doing it now would triple this plan for
a caller that does not exist.

**2026-08-29 — Reporting is what being linked means.** No separate setting.
An unlinked device reports nothing because it has no credential; a linked one
reports because that is what somebody chose when they linked it. A switch
would be a second thing to check when the picture is missing.

## Progress

- [x] Milestone 1: the connection — *done 2026-08-29*
- [x] Milestone 2: one request over the tunnel — *done 2026-08-29*
- [x] Milestone 3: screenshots — *built 2026-08-29, not yet proved against
      the real service*
- [ ] Milestone 4: state

Remaining before this can be called done: a run against the real service. The
framing is proved against a stub that serves the real device routes with Go's
own HTTP server on the other end, which is a strong test of the framing and no
test at all of the service actually accepting what this sends.

## Surprises and discoveries

**2026-08-29 — The websocket read limit is enforced by closing the
connection.** The service's limit was the library default of 32 KB and has
been raised to 1 MB. It matters here because exceeding it drops the tunnel
with nothing anywhere naming a size, so a device that wrote a whole image in
one call would see its connection die and have no way to find out why. This
device writes bodies through Go's HTTP client, which buffers at 4 KB, so it
will not hit it -- but it is written down because the failure would be
baffling.
