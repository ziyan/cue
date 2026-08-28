# Telling somebody there is a newer cue, and letting them take it

## Purpose

After this work, somebody who opens a screen's web interface is told when a
newer release exists, can read what changed in it, and — on a device set up to
allow it — can take the upgrade by pressing a button, without an SSH session or
a laptop that knows the docker command.

Today a screen runs whatever image was put on it, for ever. Nothing tells
anybody that a fix exists, so the fixes are on GitHub and the screens are on
walls and the two never meet. That is the whole problem.

How to see it working, on a device running an older release:

    open http://<the device>:8080/upgrade

The page names the version it is running, the newer one, and shows that
release's notes. On a device with `upgrade.allowApply` set and the Docker
socket mounted, there is a button. Press it, and the screen goes dark for
about half a minute and comes back running the new version, with its playlist
and its password intact.

## Definitions

**Release.** A published GitHub release of this repository: a tag like
`v0.2.0`, a body of Markdown that the release workflow copied out of
`CHANGELOG.md`, and a date. This is what the daemon asks about.

**Newer.** Compared as three numbers, major then minor then patch. A build
reporting `0.0.0-dev` — which is what a plain `go build` produces — is never
told it is out of date, because a development build is not on the ladder and
telling somebody to "upgrade" from it to a release would be wrong as often as
right.

**Apply.** Pulling the new image and replacing this container with one made
from it. Distinct from *check*, which only reads. Check is always on; apply is
off unless deliberately turned on.

**The Docker socket.** `/var/run/docker.sock`, the Unix socket the Docker
daemon listens on. Anything that can write to it can start a container that
mounts the host's root filesystem, so it is not a small permission: it is
root on the machine, by another name.

**The swap.** The three-container dance that replaces a running cue with a
newer one. Described under milestone 3, because it is the part that can leave
a screen dark and deserves its own section.

## What this costs, and why it is off by default

Cue's containers already ask for a lot: `NET_ADMIN`, `SYS_ADMIN`, `SYS_TIME`,
`SYS_RAWIO`, the host network namespace. Every one of those is there because
some specific thing does not work without it, and each is written down in
`tools/deploy/main.go` next to the flag that grants it.

The Docker socket is different in kind. The others let the daemon do more to
the machine it is already on; the socket lets it become any process on that
machine. A screen in a lobby, a waiting room or a shop window is a device
strangers stand in front of, and one whose web interface is reachable by
everybody on the network. Handing that interface the socket means the password
on the web interface is now the password to the host.

So: the daemon never assumes it. Apply is possible only when both are true —
the socket is mounted **and** `upgrade.allowApply` is set in `cue.yaml`. Either
alone does nothing. A device with neither still gets the whole of the checking
half, and the page shows the two commands to run by hand instead of a button.

The upgrade button is in the web interface only. It is deliberately not in the
on-screen menu: the menu is for somebody standing at the screen, and "replace
the software on this machine" is not a thing proximity should authorise. See
`docs/planning/active/20260827-screen-passes.md` for the reasoning that
settled where that line sits.

## Milestones

### 1. The daemon knows what the newest release is

`internal/upgrade` asks the GitHub API for the latest release of this
repository, once a day and whenever somebody asks, and remembers the answer.
It compares that version with its own and says whether one is newer. A device
with no route to the internet says so and does not retry in a tight loop.

Verify:

    go test ./internal/upgrade/ -v

Expect: `0.1.0` is older than `0.2.0`; `0.2.0` is not older than `0.2.0` or
`0.1.9`; `0.0.0-dev` is never older than anything; `10.0.0` is newer than
`9.9.9`, which is the test that catches comparing as strings.

### 2. The web interface shows it

`GET /api/v1/upgrade` answers with the running version, the newest one, its
notes and its date, and whether applying is possible on this device — and if
not, which of the two reasons. A page renders it, with the notes as Markdown.

Verify:

    curl -sS -b <session> http://<device>:8080/api/v1/upgrade | jq

Expect the running version to match `cue version`, and `canApply` to be false
on a device with no socket, with a reason naming the socket.

### 3. Pressing the button replaces the container

`POST /api/v1/upgrade` pulls the image and performs the swap. It is refused
unless applying is possible, and it needs a session — the ordinary password,
not a pass.

The swap, in order, and the order is the point:

1. Read this container's own configuration from the Docker API, by inspecting
   the container whose id is this process's own. Everything is taken from
   there rather than rebuilt from a template, so a device started with an
   unusual flag keeps it.
2. Pull the new image. If this fails, nothing has changed yet and the answer
   is an error on the page.
3. Start a helper container **from the new image**, with the socket, `--rm`,
   running `cue upgrade swap`. The new image is used deliberately: the code
   that performs the swap is the code being upgraded to, so a bug fixed in the
   new release is fixed for the upgrade that installs it.
4. The helper waits for the old container to stop, removes it, creates the new
   one from the saved configuration with the new image, and starts it.
5. If the new container does not come up healthy inside two minutes, the
   helper puts the old one back from the saved configuration and leaves the
   old image in place. A screen that fails to upgrade must still be a screen.

Verify, on carbon:

    curl -sS -X POST -b <session> http://<device>:8080/api/v1/upgrade

Expect: the screen goes dark, comes back within a minute, `cue version` reports
the new version, and the playlist and password are unchanged. Then verify the
rollback by pointing it at an image tag that exists but cannot start, and
expect the screen to come back on the old version.

### 4. It says so on the screen it is about to interrupt

An upgrade blanks the screen for the better part of a minute. Before it
starts, the screen itself says what is happening, for the sake of whoever is
standing in front of it wondering why the lobby display just died.

## Progress

- [x] 1. The daemon knows what the newest release is — 2026-08-28.
  `internal/upgrade/version.go` compares, `release.go` asks GitHub, and
  `upgrade.go` keeps the answer and refreshes it daily. Tested against a
  stand-in server rather than the real API, so the tests prove something
  without a network.
- [x] 2. The web interface shows it — 2026-08-28. `GET /api/v1/upgrade`
  behind the session, an Upgrade page, and `upgrade.allowApply` in the
  configuration so the page can say whether the button will be possible before
  the button exists.
- [x] 3. Pressing the button replaces the container — 2026-08-28. A minimal
  Docker client over the socket, a helper container built from the new image,
  and a rollback that puts the old container back under its own name if the new
  one does not answer. Tested against a fake daemon on a real Unix socket, and
  the ordering was checked by breaking it on purpose. **Not yet run against a
  real Docker daemon on hardware.**
- [x] 4. It says so on the screen it is about to interrupt — 2026-08-28. The
  browser is sent to `/upgrading` before anything is touched, and the playlist
  is held so the message is not rotated away. The page fetches nothing, because
  the daemon serving it is about to stop.

## Decision log

**2026-08-28 — The Docker socket, opt-in, rather than a helper on the host.**
Asked and answered. The alternative was a systemd unit on the host watching
for a request file, which keeps the container exactly as privileged as it is
now. It was not taken because cue's promise is that a machine needs nothing
but Docker, and a unit that has to be installed and then kept in step with the
container's run arguments is host configuration by another name — the thing
this project exists to avoid. The cost is real and is written down above; it
is paid only by devices that opt in.

**2026-08-28 — The swap runs from the new image, not the old.** A helper built
from the image being replaced would carry that image's bugs into the operation
that replaces it, which is exactly backwards: the older the release somebody
is upgrading from, the more likely its upgrade code is the broken part.

**2026-08-28 — GitHub, daily, and on demand.** Asked and answered. The
unauthenticated API allows sixty requests an hour per address, which is ample
for one check a day; the notes come back as Markdown already written for a
person, because the release workflow copies them out of `CHANGELOG.md`.

**2026-08-28 — No upgrading from the on-screen menu.** Standing in front of a
screen authorises changing what it shows and how it reaches the network. It
does not authorise replacing the software on the machine.

**2026-08-28 — Reversed: the menu does offer it, behind the password.** Asked
for, and right. The objection above was to *proximity* authorising an upgrade,
and proximity stopped being the authorisation when the menu started asking for
this device's password — see
`docs/planning/active/20260827-screen-passes.md`. Somebody who has typed the
password into the menu has proved exactly what they would have proved by
signing in to the web interface, and the interface is not always reachable,
which is the situation the menu exists for. It is offered only when the device
could actually do it, and only through a pass that has been through the gate.

## Surprises and discoveries

**2026-08-28 — teanode's updater does not transfer.** The request pointed at
`~/projects/teanode/teanode` as the example. Its `internal/updater` replaces
the running executable in place and restarts it, which is right for a program
installed as a binary and impossible for this one: cue's binary lives inside a
read-only image layer, and a replacement would be discarded the next time
Docker restarted the container from that image. What transfers is the shape of
the checking half — ask GitHub, compare, remember — and none of the applying
half.

**2026-08-28 — A failed check must not forget what it knew.** The first
version cleared what it had found when a check failed, which is wrong in a way
that reads as reassurance: a device that saw 0.2.0 yesterday and cannot reach
GitHub today would show nothing at all, and a page showing nothing looks
exactly like a page saying you are up to date. It now keeps the last answer,
with the date it was obtained and a line saying the most recent attempt failed.

**2026-08-28 — The page test had a written list of pages.**
`TestEveryPageRendersWithoutFaulting` opens every page in a real browser and
fails on an uncaught exception, which is the test that exists because a page
was once shipped throwing `ReferenceError` on load. It carried a hand-written
list of the five pages, so adding a sixth left it passing over a page nobody
had opened — the same shape as the fault it was written for. It now reads the
list out of `static/app.js`. Confirmed by breaking the new page on purpose and
watching it fail by name.

**2026-08-28 — `allowApply` must not be settable from the interface.**
`TestEverySettingIsReachableFromTheInterface` asks for a control for every
setting, and this is the one that has to be argued out of it rather than given
one: the interface is the thing the setting grants power over. Anybody who
reached the web interface could otherwise turn on its own access to the Docker
socket. It takes two deliberate acts from somebody with a shell on the machine.

**2026-08-28 — The menu could not be opened on a real screen at all.** Found by
running it on carbon, not by any test here. The menu was an iframe laid over
whatever page the screen was showing, which made it a subresource request from
that page's origin to an address on the local network — and Chrome asks the
viewer to approve those. A wall display put up "do you want to allow
https://example.com to access local network", a question with nobody in front
of it to answer, and the menu never appeared.

The same design had a second fault visible at the same time: the frame lived
inside a tab the playlist rotates, so opening the menu and then having the
rotation move on left the menu drawn over a page that had gone.

Both are gone now that the mark opens the menu as a tab of its own. A top-level
navigation is not a subresource request, so no permission is involved, and
everything inside the tab is same-origin with the daemon. It also turned up a
third thing: the browser closes any window this daemon did not open, so the
first version of that tab was swept away a cycle after it appeared. The sweep
now knows the menu's own address.

**2026-08-28 — What a review round found that the tests did not.** Six things,
and two of them would have taken a screen down.

*Two upgrades at once was a dead device.* Starting a second helper
force-removes the first, and the first may be between stopping the old
container and starting the new one — so what is left is a machine with no cue
on it. Pressing the button twice, or pressing it on the page and then in the
on-screen menu, is an ordinary thing to do. There is now one claim on the
upgrade at a time, and it is given up again if nothing comes of it.

*The hold expires and an upgrade takes longer than a hold.* The screen is told
it is updating and the playlist is held so the message stays — but the hold
lapses after ninety seconds without a heartbeat, and the page shown during an
upgrade deliberately fetches nothing. The daemon renews it, and goes on
renewing past the point where the helper takes over.

*Reloading the playlist raced the tab going back.* Closing the menu asks the
daemon to reload the pages and navigates this tab back to its own page in the
same breath. The tab showing the menu was in that list, so a reload could win
the race and load the menu again. It is skipped: it is already loading the page
it came from.

*A ten-minute HTTP timeout on the Docker client.* Fine for an inspect, not
enough for a gigabyte and a half over the connection some buildings have, and
it silently overrode the caller's thirty minutes. The deadline belongs to the
caller.

*Two comments that had stopped being true* — one saying the upgrade is
deliberately not reachable from the on-screen menu, written before that was
reversed, and one saying the page refreshes a day-old answer where the code
says an hour. And `/api/v1/playlist/release` answered `{"held": true}`.

## Outcomes and retrospective

To be written at each milestone.
