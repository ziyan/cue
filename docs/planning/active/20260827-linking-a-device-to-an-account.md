# Linking a device to a hosted account

## Purpose

After this work, somebody standing in front of a screen can point their phone
at it and end up with that screen attached to their account on the hosted
service, without typing an address, a code, or a credential into the device.

They open the menu, prove the device password, and choose to link it. The
screen shows a QR code. Scanning it opens a page on the hosted service, which
asks them to sign in and shows what they are about to attach: this device's
name and identifier. They press authorise. Within a few seconds the screen says
it is linked, and the device is holding a credential it can use to hold open a
connection to the service from then on.

The same is available from the web interface, for somebody at a desk rather
than in the room, and looks the same: a code to scan or a link to follow.

To see it working: open the menu on a device, unlock it, choose **Link to the
service**, and watch the page change from a code to a confirmation once the
link is authorised from a phone. `curl` the status endpoint and see the account
it reports.

## Definitions

**The service.** The hosted side this device attaches to. It is a separate
program in a separate repository; this plan concerns only what runs on the
device, and the two endpoints it calls.

**Linking.** Attaching this device to somebody's account on the service, once,
so that the device can afterwards prove which account it belongs to.

**Ticket.** A short-lived, public identifier for one attempt at linking. It
travels in the QR code and in the URL, and is safe to be seen: it is not enough
on its own to complete a link.

**Verifier.** The secret half of a ticket. It never leaves the device — not in
the QR code, not in the URL, not in the log. The ticket is derived from it by
hashing, so the service can check that whoever is asking to complete a link is
the same device that started it.

**Device secret.** What the service hands back when a link is authorised. It is
the credential the device presents when it connects to the service afterwards.
It is stored in `cue.yaml` and never leaves the device again.

## The shape of it

The interesting decision is how a QR code on a wall can be safe. Anybody who
can see the screen can photograph the code, so whatever is in it must not be
enough to take the device over.

So the code carries a ticket and not a secret, and the ticket is derived from
the secret rather than paired with it:

    verifier = 32 random bytes
    ticket   = base64url(sha256(verifier))

The QR code is `https://<service>/link/<ticket>`. The device keeps the verifier
in memory and never sends it anywhere except the exchange call, which goes
directly to the service over TLS. Somebody who photographs the screen has the
ticket, which lets them open the authorisation page and nothing else — they
cannot complete the exchange, because they cannot produce a verifier that
hashes to that ticket.

This is the shape of PKCE, and for the same reason: one party has to start a
flow that another party finishes, over a channel that can be observed.

Deriving the ticket from the verifier rather than generating two independent
values is what removes the need for the device to register the ticket with the
service before showing it. The service can check the pairing when the exchange
arrives, from the exchange alone. That matters here because the screen should
be able to show a code before anybody has proved the device can reach the
service at all.

**What this does not defend against.** Somebody who photographs the code while
it is displayed can open the page and authorise the device into *their* account
instead of the operator's. Nothing in the protocol prevents that, because from
the service's point of view both people are holding the same ticket. What
prevents it in practice is that the code is only shown to somebody who has
already proved the device password, and only for a few minutes. A screen does
not sit there displaying a linking code to a room full of strangers. The device
also shows which account it ended up attached to, so a link to the wrong one is
visible rather than silent.

**The exchange is two calls, not one.** The poll asks whether anybody has
authorised the attempt; a second call redeems it. The split exists because the
verifier is the half that redeems, and the poll runs every two seconds for ten
minutes -- three hundred times per attempt, all but one of them redeeming
nothing. Sending the secret on every one of them pushes it through every proxy
and access log in front of the service to accomplish nothing. PKCE, which this
is shaped after, sends the verifier exactly once, at redemption; this now does
the same.

    POST .../device/link/exchange   {ticket, name, identifier}
      204  unknown, or not authorised yet. Keep asking.
      202  authorised, or already redeemed. Go and collect it.
      403  refused: a person decided against it. The attempt ends.
      404  not heard of. Keep asking.
      ---  anything else is a fault worth retrying on the next tick.

    POST .../device/link/redeem     {ticket, verifier}
      200  {secret, account, deviceId}
      403  the verifier does not hash to the ticket, or it was never
           authorised. The attempt ends.
      ---  transient; asked again on the next tick.

A 404 was originally read as a refusal, on the reasoning that a ticket the
service has never heard of is one it will never hear of. That is backwards. The
device shows its code *before* it has spoken to the service -- the whole reason
the ticket is derived rather than issued -- so the service does not learn a
ticket until somebody opens the link on their phone. A 404 is therefore the
answer to the first poll of every attempt.

Two rules that look like details and are not:

*Authorised and redeemed answer the same 202.* If redemption answered once and
then refused, a lost response would leave the device unable to ask again and
the link dead, with somebody standing in front of the screen. Redemption is
idempotent until the ticket expires, and the poll cannot distinguish the two
states, so a lost answer is simply retried. Returning the same credential twice
grants nothing: redeeming requires the verifier, and the only party holding the
verifier is the device. Burning the ticket would punish exactly the party that
is not an attacker.

*Only the first poll says what the device is.* Taking the verifier out of the
poll removed the only thing that gated registration, so without this anybody
who photographed the code could poll with a name of their choosing and rewrite
what the authorisation page shows the person deciding. First write wins, and
the legitimate values are always first because the device polls before the code
has been rendered. The cost is that a device renamed mid-attempt keeps the old
name on the page until the code is shown again.

**A link is not a link until the credential has been used.** Redemption proves
somebody authorised something; it does not prove that what came back works. So
the device spends the credential once, asking the service who it is, and only
an answer makes it a link -- the identity call must return this device and must
not say it has been revoked. Until then the interface says it is checking
rather than claiming to be linked. A screen on a wall reporting itself linked
on the strength of an unexamined string is the kind of mistake nobody discovers
until much later, in a room with no keyboard.

**Where the secret lives.** In `cue.yaml`, in a new section, as a `Secret` —
the same type the VNC password uses, which renders as a placeholder everywhere
outside the file and is restored by `RestoreSecrets` when a form is posted
back. Nothing else in this project has a place for a credential, and inventing
a second one would be worse than the section.

**What is deliberately not here.** The connection itself. This plan ends with
the device holding a credential and knowing which account it belongs to. Using
that credential to hold open a connection to the service, and reporting
anything over it, is the next piece of work and is large enough to be its own
plan. Ending here is deliberate: linking is independently useful and
independently verifiable, and the connection has nothing to attach to until it
exists.

## Proved against the service

Run on 2026-08-29 against the real service rather than a stub. A device on a
wall reached it over a reverse tunnel, so the service's loopback was the
device's loopback -- which also exercised the exemption that lets a plain
address through only there.

Observed, in order, by the two programs rather than by one program and a
stand-in: the device derived a ticket and showed it; the service answered 204
to a ticket it had never heard of and recorded the attempt from that first
poll; a mismatched verifier was refused with 403; somebody signed in and
authorised the page; the next poll returned the credential; the device stored
it; and that credential, read back out of `cue.yaml`, opened the service's
device websocket with 101 where a fabricated one got 401.

The device row the service then created reads `name=carbon` and
`description=t6ny2v00xad86aj0` -- this device's own name and identifier,
carried through the exchange, the signed payload and back. Lengths checked
rather than looked at: 6 and 16 bytes, no truncation.

Two things learned that are worth keeping:

The wire contract was written down only after this. A 404 had been read as a
refusal, which would have killed every attempt on its first poll; see the
answers table above. The service now answers 204 for both "unknown" and "not
yet", so the device's 404 handling is only robustness for a service that has
not deployed the endpoint.

And a note for anyone repeating this: over a reverse tunnel every device looks
to the service as though it connected from the tunnel's own address, so the
"where did this device connect from" fields are meaningless in this setup.
Test that part without the tunnel or it will look broken when it is not.

## Progress

- [x] Milestone 1: the ticket, and the exchange — *done 2026-08-27*
- [x] Milestone 2: linking from the menu — *done 2026-08-27*
- [x] Milestone 3: linking from the web interface — *done 2026-08-27*
- [ ] Remaining: the connection itself, which is its own plan. This work ends
      with a device holding a credential and knowing which account it belongs
      to. Nothing yet uses it.
- [ ] Remaining: the service side. The two endpoints this calls do not exist
      yet in the other repository. Until they do, the flow can be exercised
      against a stub — `internal/link`'s tests are one — but not end to end.

## Milestones

### Milestone 1: the ticket, and the exchange

`internal/link` mints a ticket and its verifier, builds the URL a phone should
open, and exchanges the pair for a device secret once the service says the link
was authorised. One attempt at a time: starting a second abandons the first,
because two codes on one screen is a question nobody should have to answer.

The exchange runs in the background on a timer and stops when the link
completes, when the ticket expires, or when a caller abandons it. A failure to
reach the service is not a failure of the attempt — a device on a network that
comes and goes should still link when the network comes back — so only an
explicit refusal from the service ends it.

Configuration gains a section holding the service's address and the device
secret. The secret is a `Secret`, added to `RestoreSecrets`.

**Acceptance.** `go test ./internal/link/...` passes, including: a ticket whose
hash matches its verifier; an exchange that returns the secret once the service
authorises; an exchange that keeps trying through a network error and succeeds
afterwards; an exchange that stops when the service refuses; and an attempt
that expires. `curl -s localhost:8080/api/v1/status | jq .link` reports
`"linked": false` on a device that has not linked.

### Milestone 2: linking from the menu

The menu gains a **Link to the service** action, behind the same elevated pass
the other device-changing actions are behind, so the device password has been
proved before a code appears. Choosing it shows the QR code, the URL as text
for somebody who would rather type it, and what happens next. The page polls
its own daemon and changes to a confirmation, naming the account, when the link
completes.

**Acceptance.** With a device that has a password set: opening the menu, typing
the password, and choosing the action shows a code. `make docker-smoke` still
passes. The action is refused with 403 when the pass has not been elevated.

### Milestone 3: linking from the web interface

The same, on a page of the web interface, behind a session. It shows the code
for a phone and the link as something clickable for the person at the desk.
Once linked it shows the account and offers to unlink, which forgets the secret
and nothing else.

**Acceptance.** Signing in to the interface and opening the page shows a code;
the page becomes a confirmation once the link is authorised; unlinking returns
it to offering a code. The configuration endpoint never returns the secret.

## Decision log

**2026-08-27 — The ticket is the hash of the verifier, not a second random
value.** A pair of independent values would mean the device has to tell the
service about the ticket before showing it, which puts a network call between
somebody pressing a button and a code appearing on a screen. Hashing removes
that call: the service can check the pairing from the exchange alone. The cost
is that the ticket cannot carry anything the service needs to know early, and
nothing needs to be known early.

**2026-08-27 — The device secret goes in `cue.yaml`, not a file beside it.**
Every other thing an operator can set is in that file and the ground rules in
`AGENTS.md` say so. A credential in a second place would be the exception that
makes somebody look in two places forever.

**2026-08-27 — Linking is gated on the device password, not on being physically
present.** A pass proves somebody is at the screen; elevating it proves they
know the password. Physical presence alone is enough to *see* a device, and on
a wall in a public room that is a low bar, so it is not enough to give one
away.

**2026-08-27 — The exchange keeps trying through network errors and stops on a
refusal.** A device that links only when the network happens to be up at the
moment somebody presses authorise would be infuriating on exactly the networks
this program is built for. A refusal is different: it means the service has
decided, and repeating the question will not change the answer.

## Surprises and discoveries

**2026-08-27 — The repository's own tests decided most of the design.** Three
of them failed before anything was wrong with the feature, and each was right:
`TestEverySettingIsReachableFromTheInterface` refused a new configuration
section with no control for it, which is what turned "store a credential" into
"build the Service page"; `TestEveryClassTheInterfaceUsesIsStyled` caught a
class name with no rule behind it; and
`TestTheMenuChangesTheNetworkThePictureAndNothingElse` refused the menu's new
calls until the allow-list said, in prose, why linking belongs at the screen.
That last one is the most useful guard in the repository: it makes widening
what the menu may do a deliberate act with a written reason.

**2026-08-27 — The first exchange waited a full interval.** The loop asked the
service on a ticker, so a link authorised the instant the code appeared still
took two seconds to be noticed, and the test that proved it had to wait longer
than it should have. It now asks once before the first tick. The test was what
found it: a one-second wait for the first call failed against a two-second
interval.

**2026-08-27 — `deferutil` here is not the one in the service's repository.**
It has `Recover` and nothing else, so the deferred-close idiom is the local
`defer func() { _ = thing.Close() }()`. Worth knowing before copying a line
across.

## Outcomes and retrospective

**2026-08-27, all three milestones.** A device can be linked from the screen or
from the interface. `internal/link` mints the pair, builds the URL, and
exchanges it in the background; the credential lands in `cue.yaml` and is never
sent out again. Nine tests cover the package, including the two behaviours that
are easy to get wrong and invisible when wrong: an attempt that survives a
network failure and completes afterwards, and one that ends when the service
refuses.

What is not proved is the other half. The service endpoints do not exist yet,
so everything here has been exercised against a stub that implements the
protocol as this plan describes it. The first thing to do when the service side
lands is to run the two against each other and find out which of the two
descriptions was wrong.

The security argument rests on something outside the protocol: the code is only
shown to somebody who has proved the device password. That is what makes it
acceptable that anybody who sees the code could authorise the device into their
own account. It is written down here rather than left implicit because it is
the sort of thing a later change could quietly remove — offering the code
before the password, say, to save a step.
