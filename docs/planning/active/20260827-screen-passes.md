# A password on every device, and a pass that lasts as long as the menu

## Purpose

After this work, two things are true that are not true today.

**A device always has a password.** The on-screen menu and the captive portal
both refuse to do anything on a device that has none — not by turning the
person away, but by asking them to choose one first. Today a device with no
password lets anybody standing at the screen change its network, its
resolution, and its wireless credentials, on the reasoning that there is
nobody to ask. That reasoning is sound for a device still in its box and wrong
for one that has been hung on a wall and never finished setting up.

**Authority at the screen lasts as long as the menu is open, and no longer.**
Today, typing the password into the on-screen menu issues an ordinary session
cookie into the screen's own browser, valid for the configured session
lifetime — twelve hours by default. The menu closes; the cookie does not. The
next person to walk up to that screen opens the menu and is already inside.
After this work, unlocking the menu elevates a *pass* that exists only while
the menu is open and is destroyed when it closes.

How to see it working, on a device with no password:

    open the menu from the floating mark on the screen

The menu offers to set a password before it offers anything else. Set one,
and the menu's actions appear. Close the menu, open it again: it asks for the
password. That second ask is the whole point, and it is the thing that does
not happen today.

## Definitions

**Pass.** A random 32-byte value the daemon mints when it serves the menu page
or the captive portal page, hands to that page, and remembers in memory until
the page closes or fifteen minutes pass, whichever is first. It is carried in
the `X-Cue-Pass` header on every request that page makes. A pass is *live*
from the moment it is minted, and becomes *elevated* when somebody proves the
password through it — either by entering the existing one or by choosing the
first one.

**Elevated.** The state a pass reaches after the password has been proved
through it. Only an elevated pass may change the device. A live-but-not-
elevated pass may do the two harmless things the menu does before anybody has
typed anything: hold the playlist so the pages stop rotating under the menu,
and release it again.

**Screen action.** Anything reachable from the screen that changes the device:
joining a wireless network, configuring a wired one, changing the resolution
or orientation, restarting the browser or the X server, forgetting the
wireless credentials, changing the language.

**Origin gating.** The rule being replaced. The daemon looked at the `Origin`
header to tell a request from a page it served from a request from a page it
merely displays, because the browser on the screen spends its life showing
other people's pages and any one of them can call the loopback. See
`docs/decisions/` and the comment on `fromOurOwnPage`.

## Why a pass and not a JWT

The request that started this asked for a JWT. A JWT is a signed statement
that carries its own expiry and needs no server-side state, and that last
property is exactly the one that is wrong here. The requirement is that the
authority ends *when the menu closes*, and the menu closes when a person
clicks an X — not at a time anybody can write into a token when it is minted.
Ending a JWT early means keeping a list of the ones that have been revoked,
which is the server-side state a JWT was chosen to avoid, plus a signature.

So: a random opaque value, remembered in a map, deleted on close. Same
lifetime guarantee, less machinery, and the revocation is a `delete` rather
than a growing denylist. Nothing about this is visible to the pages, which
send a header either way; if a JWT is ever wanted for another reason, the
header does not change.

A pass is confidential for the same reason a page's own contents are: the
menu is served into an iframe on the screen's browser, and a page from
somewhere else cannot read across that boundary. It can request `/menu`, but
it cannot read the response — the same-origin policy stops it, and the daemon
sends no CORS headers that would relax that. This is the property that makes
the pass a replacement for the Origin check rather than a supplement to it.

## Milestones

### 1. Passes exist and are checked

`internal/web/pass.go` holds the mint/elevate/check/revoke logic and its
tests. `screenAction` accepts an elevated pass, and otherwise falls back to
the ordinary session rules, with no reference to `Origin`.

Verify:

    go test ./internal/web/ -run TestPass -v

Expect: a minted pass is live and not elevated; elevating it once makes it
elevated; revoking it makes it neither; a pass past its lifetime is neither;
an unknown value is neither.

### 2. The menu asks for a password, or asks for one to be set

The menu's gate has two faces. On a device with a password it asks for it. On
a device without one it asks for a new one, twice, refusing anything under
eight characters — the same rule the setup wizard already applies. Either way
the pass ends up elevated and the actions appear.

Verify, on a device with no password:

    curl -sS localhost:8080/menu | grep -c 'data-t="choose-word"'

Expect 1. And that the actions are hidden until the pass is elevated.

### 3. Closing the menu ends the authority

The menu revokes its pass when it closes, by the X, by the Escape key, or by
clicking outside it.

Verify on a device with a password: open the menu, unlock it, close it, open
it again. Expect the password to be asked for a second time.

### 4. The captive portal does the same

The portal mints a pass, asks for the password on a device that has one, and
asks for a new one on a device that does not, before it will scan or join.

Verify: with the device in setup mode, load the portal from a phone. Expect a
password step before the list of networks.

## Progress

- [x] 1. Passes exist and are checked — 2026-08-27. `internal/web/pass.go`
  and `internal/web/pass_test.go`. `screenAction` no longer mentions Origin.
- [x] 2. The menu asks for a password, or asks for one to be set — 2026-08-27.
  The gate has two faces and is never absent; the new password is asked for
  twice, with an eight character floor said both in the page and by the daemon.
  New words in all three languages.
- [x] 3. Closing the menu ends the authority — 2026-08-27. The X, the Escape
  key and a click outside all revoke the pass.
  `TestClosingTheMenuEndsTheAuthority` was checked by breaking `revoke` and
  watching it fail.
- [x] 4. The captive portal does the same — 2026-08-27. It mints a pass, gates
  on it, and no longer issues a session cookie to a phone.

Not yet done: none of this has been exercised on real hardware. Milestones 2,
3 and 4 are checked by tests and by reading the rendered page, not by standing
in front of carbon with a keyboard.

## Decision log

**2026-08-27 — An opaque pass, not a JWT.** Recorded above under "Why a pass
and not a JWT". The deciding property is revocation on close.

**2026-08-27 — Forcing a password rather than refusing service.** A device
with no password could simply refuse the menu. It does not, because the person
standing at a screen with no password is overwhelmingly likely to be the
person who owns it, and refusing them leaves them with a screen they cannot
configure and no way to fix it except the web interface they may not be able
to reach — which is the situation the on-screen menu was built to rescue.
Asking them to choose a password costs one step and ends with the device in
the state it should have been in already.

**2026-08-27 — Origin checks stay where the pass cannot reach.** The player
pages fetch their own media and tell the daemon a video has ended. They are
not the menu, they hold no pass, and nothing they do changes the device.
`localOrSession` keeps its Origin check for those, and additionally accepts a
live pass. Only `screenAction` drops Origin entirely.

## Surprises and discoveries

**2026-08-27 — The on-screen gate was issuing a full session.** The gate
posted the password to `/api/v1/session`, which is the ordinary sign-in
endpoint: it sets the `cue_session` cookie in the screen's own browser for the
configured session lifetime. So the gate added to protect a screen in a lobby
protected it exactly once — the first person to type the password left the
browser signed in behind them. This was found while reading the code to
replace the Origin check, not by testing, and it is the strongest argument for
the pass.

## Outcomes and retrospective

To be written at each milestone.
