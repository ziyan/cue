# Build Cue: a single Go daemon that turns a bare headless Linux box into a managed display

This ExecPlan is a living document. The sections `Progress`, `Surprises &
Discoveries`, `Decision Log`, and `Outcomes & Retrospective` must be kept up to
date as work proceeds. It is maintained in accordance with the ExecPlan rules
described in `docs/coding/execplans.md`.

## Purpose / Big Picture

Today, putting a web page or a video on a TV that is plugged into a small
headless computer means configuring the host: installing an X server, a browser,
a VNC server and a process supervisor, creating users, writing
`/etc/default/...` files, and repeating all of it on every machine. That is what
the author's previous project (`monitor`) does, and it is why every new screen
takes an afternoon.

After this work, the entire system is one container image and one command. A
fresh Debian install with nothing on it but Docker becomes a managed display:

    docker run -d --name cue --network host --privileged \
      -v /var/lib/cue:/var/lib/cue ghcr.io/ziyan/cue:latest

Within about fifteen seconds the attached HDMI screen lights up showing a
first-run page served by the daemon itself. Pointing a laptop browser at
`http://<device-ip>:8080/` shows the same page, where an operator sets an
administrator password, names the device, and types in the URLs to display.
From then on the screen cycles those URLs full screen with no window
decorations, no mouse cursor and no browser chrome. The same web interface
shows CPU, memory, disk and temperature graphs, lists the HDMI outputs,
sound cards and cameras the machine has, lets the operator watch and control
the screen remotely in the browser over VNC, and can optionally enrol the
device into a fleet-management service (`cue.sh`) so that many screens can be
driven from one place.

Everything inside the container is started, watched, restarted and stopped by
one Go program. There is no shell, no init system, no supervisor daemon and no
package manager in the running image.

**How you will see it working.** After `make docker` and a `docker run` on a
machine with a screen attached, the screen shows content. `curl
http://127.0.0.1:8080/healthz` returns `200 OK`. `curl
http://127.0.0.1:8080/api/v1/screenshot.png > shot.png` writes a PNG of exactly
what is on the screen. Unplugging the HDMI cable and plugging it back in brings
the picture back without restarting anything, and the daemon's log says so.

## Definitions

These terms appear throughout. Each is defined once here, in plain language,
because the rest of the plan relies on them.

**Daemon.** A long-running program with no user interface of its own. Here it
is the single Go binary `cue`, which runs as process number 1 inside the
container.

**Distroless image.** A container image that contains application files and the
shared libraries they need, but no shell, no package manager and no init
system. Google publishes base images for this at `gcr.io/distroless/...`. Cue's
image is built by installing Debian packages in a throwaway builder stage,
collecting exactly the files those packages own, and copying that collection
onto `gcr.io/distroless/base-debian13`. See `docs/decisions/` for why.

**X server / Xorg.** The program that talks to the graphics hardware and puts
pixels on the screen on Linux. Applications connect to it over a Unix socket at
`/tmp/.X11-unix/X0` and it authenticates them with a cookie stored in a file
usually called `.Xauthority`. `Xorg` is the real one that drives hardware;
`Xvfb` is a fake one that renders into memory, used in tests.

**RandR.** An extension of the X protocol ("Resize and Rotate") through which a
client can ask the X server which physical outputs exist (HDMI-1, DP-2, …),
which display modes each supports, and can then set a mode, position and
rotation. The command line tool `xrandr` is a thin wrapper over it. Cue speaks
RandR directly from Go rather than shelling out, because there is no shell in
the image.

**DRM.** The kernel's Direct Rendering Manager. Its device nodes are
`/dev/dri/card0`, `/dev/dri/renderD128`, and it reports connector state under
`/sys/class/drm/`. Cue reads that directory to know whether an HDMI cable is
plugged in, without needing the X server.

**Kiosk mode.** Chromium started with `--kiosk`: full screen, no tabs visible,
no address bar, no way for a passer-by to leave the page.

**CDP (Chrome DevTools Protocol).** The JSON/WebSocket protocol Chromium
exposes on a local port when started with `--remote-debugging-port`. It is the
same protocol the browser's own developer tools use. Cue uses it to open tabs,
switch between them, fill in login forms and capture screenshots, which is why
Cue does not need a browser extension the way `monitor` did.

**VNC / RFB.** A protocol for viewing and controlling a remote screen. `x11vnc`
is a server that exports an existing X display over it. `noVNC` is a JavaScript
client that speaks it over a WebSocket, which is how Cue's web interface shows
the screen in a browser tab.

**PulseAudio system mode.** PulseAudio normally runs once per logged-in user.
With `--system` it runs once for the whole machine, which is what a container
with no user session needs. It is what gives Chromium sound and what lets the
operator pick which HDMI or USB output the sound comes from.

**chronyd.** An NTP client that keeps the clock correct. A browser cannot
validate a TLS certificate if the clock is wrong, so a display that boots with a
dead battery shows an error page until the clock is fixed. Cue runs chronyd for
this reason.

**Fleet enrolment.** The optional act of registering a device with `cue.sh`, a
separate, closed-source service, so that it can be managed remotely. The daemon
keeps one outbound WebSocket connection to that service and multiplexes streams
over it (using yamux), so the device is reachable without opening any inbound
port or changing any firewall.

**yamux.** A library that carries many independent bidirectional streams over a
single connection, the way HTTP/2 does. Used for the fleet tunnel.

**uevent.** A message the Linux kernel broadcasts when a device appears or
disappears. Cue watches for these to notice a monitor, USB sound card or camera
being plugged in — with a `/sys` poll as the always-available fallback.

## Progress

- [x] (2026-08-22 21:30Z) Read `monitor`, `teanode`, `cue.sh` and the portal
      VNC bridge; settled naming, module path, and the conventions this repo
      inherits.
- [x] (2026-08-22 21:45Z) Confirmed with the author: kiosk-page auto-login (not
      an auto-login for Cue's own admin interface), ship chrony and a sound
      server, build the full plan in order.
- [x] (2026-08-22 21:50Z) Milestone 1 — repository skeleton, `cue version`, CI green.
- [x] (2026-08-22 22:00Z) Milestone 2 — configuration file: types, defaults,
      validation, `cue config`.
- [x] (2026-08-22 22:10Z) Milestone 3 — process supervision and PID 1 duties.
- [x] (2026-08-22 22:35Z) Milestone 4 — X server lifecycle and RandR display
      management, with `cue display probe` proved against real hardware.
- [x] (2026-08-23 02:10Z) Milestone 5 — Chromium lifecycle, CDP client,
      playlist, login rules, freeze watchdog and cache recovery.
- [x] (2026-08-23 02:12Z) Milestone 6 — VNC server and the WebSocket bridge.
- [x] (2026-08-23 02:30Z) Milestone 7 — web interface: API, sessions,
      onboarding, monitoring, noVNC. Proved by having the daemon sign itself
      into its own interface with a login rule.
- [ ] Milestone 8 — audio and time synchronisation done; hardware inventory
      done; touch input mapped to the right output remains.
- [x] (2026-08-23 02:45Z) Milestone 9 — the distroless image, compose files,
      end-to-end smoke test. `make docker-smoke` passes.
- [ ] Milestone 10 — optional fleet enrolment into cue.sh.
- [x] (2026-08-23 03:00Z) Milestone 11 — documentation, decision records,
      release workflow.
- [ ] Validation on real hardware. The machine set aside for it (`carbon`) went
      off the network partway through and has not come back; everything so far
      is proved against a virtual screen in the image, plus `cue display probe`
      against real connectors.

## Surprises & Discoveries

- Observation: kernel uevent netlink sockets are scoped to a network
  namespace, so a container on Docker's default bridge network receives no
  hotplug events at all.
  Evidence: `NETLINK_KOBJECT_UEVENT` delivery is filtered by netns in
  `lib/kobject_uevent.c`; the practical consequence is that hotplug only works
  with `--network host`. Cue therefore treats a `/sys` poll as the primary
  mechanism and netlink as an accelerator, and the compose file uses host
  networking anyway because the web interface and VNC should be reachable on
  the machine's own address.

- Observation: the escalation ladder was not a ladder. The heaviest applicable
  remedy fired again on every probe with only a fixed cooldown between
  attempts, so a page that took forty seconds to load was reloaded every
  fifteen and never finished, and the heavier steps that might have fixed it
  were never reached.
  Evidence: `TestOneRungIsNotHammeredWhileTheLadderIsUnclimbed` counted
  fourteen reloads in three hundred milliseconds. A step is now applied once
  per episode — an episode being the run of failures since the display last
  answered — and each repeat waits twice as long as the last.

- Observation: connecting to an X server had no deadline on the *handshake*,
  only on the connection. An X server that accepts a connection and then never
  finishes authenticating would block the caller forever — and the caller is
  the watchdog, whose whole job is to notice that the X server has stopped
  answering.
  Evidence: a web test connected to the developer's own desktop X server,
  which refused the cookie without replying, and `go test` timed out after a
  minute inside `xgb.postConnect`. `display.Open` now takes a context and sets
  a deadline on the socket across the handshake.

- Observation: running the daemon with its *own defaults* — rather than with
  the development settings every earlier test had used — found three faults at
  once, all of which would have made a real device fail on its first boot.
  `/usr/bin/Xorg` is a shell script like `/usr/bin/chromium`, so the X server
  never started; `chronyd` lives in `/usr/sbin`, which was not on the image's
  PATH; and `chronyd` drops privileges to an account that did not exist in the
  image, refuses to open a command socket in a directory anybody can write to,
  and writes a pid file into a directory it does not create.
  Evidence: three rounds of "exited before it was ready" with nothing else in
  the log, which is also what prompted the supervisor to start reporting a
  failed program's last few lines of output at a level somebody will see.
  The lesson: test the defaults, not the configuration that is convenient.

- Observation: the X server, the VNC server and the time client each held a
  pointer to the configuration taken when the daemon started, so a change made
  through the interface never reached them. Changing `display.server` was
  accepted and then did nothing at all.
  Evidence: switching from xorg to xvfb through the API left the daemon
  restarting Xorg in a loop. All three now read through the store, and the
  daemon restarts the X server for the settings that are fixed when it is
  executed.

- Observation: the login rule works against the real thing. Pointed at a UniFi
  Protect controller it recognised the login page, filled `#login-username`
  and `#login-password`, ticked "remember my credentials", clicked a submit
  button that starts out disabled, and landed on `/protect/dashboard/all`
  showing four live camera feeds.
  Evidence: `logins: 1` in the status response, and a screenshot of the
  dashboard. The disabled-button retry earned its place immediately.

## Decision Log

- Decision: the module path is `github.com/ziyan/cue`, the binary is `cue`, the
  configuration file is `/etc/cue/cue.yaml`, state lives in `/var/lib/cue` and
  runtime files in `/run/cue`.
  Rationale: matches the repository the author created and the conventions in
  `teanode`.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: Cue drives Chromium over the Chrome DevTools Protocol instead of
  loading a browser extension.
  Rationale: `monitor` rotated tabs with a Manifest V3 extension whose
  configuration had to be written to a JSON file next to it, which meant the
  supervisor generated JavaScript's input at start-up and the two could drift.
  CDP puts the whole rotation, reload and login policy in Go where it can be
  changed at runtime through the API, tested, and reported on. It also gives
  screenshots for free.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: keep `x11vnc` and bridge it to the browser over a WebSocket in Go,
  rather than implementing an RFB server in Go.
  Rationale: x11vnc's damage tracking and encodings are tuned and correct; a
  first-cut Go RFB server would be slower and buggier for no gain the operator
  can see. The Go side owns the part that actually needs to be ours — the
  authenticated WebSocket bridge — which is the same shape as the portal's
  `backend/api/apivnc`.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: PulseAudio in system mode, not PipeWire.
  Rationale: PipeWire expects a D-Bus session and WirePlumber; PulseAudio's
  `--system` mode is designed for exactly the case of a machine with no logged
  in user, and Chromium speaks to it through libpulse without extra work.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: the runtime image is assembled by collecting the file lists of the
  Debian packages installed in a builder stage, rather than by copying whole
  directory trees.
  Rationale: it yields an exact, auditable manifest of what is in the image
  (written to `/usr/share/cue/packages.txt`), and it keeps the image from
  quietly accumulating whatever else the builder happened to contain.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: Chromium runs as an unprivileged user inside the container while
  the daemon and the X server run as root.
  Rationale: Chromium refuses to enable its own sandbox when running as root,
  and `--no-sandbox` on a machine that renders arbitrary web pages is a real
  risk. The daemon starts Chromium with a different uid, so the setuid sandbox
  helper works and the browser keeps its process isolation.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: auto-login is a set of rules evaluated continuously, not a one-shot
  action at start-up.
  Rationale: the case that motivated it is a UniFi OS dashboard which expires
  its session every few hours and redirects the tab to `/login?redirect=...`.
  A login performed once when the tab opens fixes nothing; what is needed is a
  rule that notices the tab has landed on the login page again and re-submits
  the credentials. Evidence from the machine that has this problem today: its
  Chromium log repeats `RTCLocalSignalingError: timed out` against
  `https://<host>/login?redirect=%2Fprotect%2Fdashboard%2F...` every twenty
  seconds, which is the dashboard failing to open a video stream because it is
  not logged in.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: liveness is probed over CDP by the daemon, not reported by a
  content script over HTTP.
  Rationale: an earlier system by the same author (`pendant`) had the page post
  a heartbeat to the daemon through a browser extension, and restarted Chromium
  when it stopped arriving. That works but requires an extension in every page,
  and it only proves that one page's JavaScript timers run. Asking the browser
  a question over CDP and requiring an answer within a deadline proves the same
  thing with nothing installed in the page, and distinguishes three failures
  the heartbeat could not tell apart: the browser process is gone, the browser
  answers but the renderer is wedged, and the renderer runs but never paints.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: the browser's disk cache is ephemeral by default, in a directory
  wiped at every start.
  Rationale: a corrupted Chromium cache produces a page that will not load
  while everything else looks healthy, and it survives restarts, so it presents
  as a machine that has to be reimaged. Making the cache disposable removes a
  whole class of "it is broken and nobody knows why". The cost is that a
  restart re-downloads page assets, which for a dashboard on a local network
  is nothing.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: no real credential, host name or address from any deployment ever
  enters this repository, and `make check-secrets` fails the build if one does.
  Rationale: the project is going to be published. The example that prompted
  the auto-login feature came with a working username and password for a
  device on a private network; that pair must exist only in that device's own
  `/etc/cue/cue.yaml`. Documentation and tests use `example.com` and obvious
  placeholders.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: nothing in this project is a shell script, at build time or at
  run time. The image contains the daemon, chronyd, Xorg, x11vnc and Chromium,
  and nothing else that executes.
  Rationale: the system this replaces was eight bash scripts, a supervisor
  configuration template and an `/etc/default` file, and the interesting
  failures were all in the seams between them. Where a helper is genuinely
  needed — checking that no credential has been committed, driving the
  end-to-end smoke test — it is a Go program under `tools/`, so it is built,
  vetted and linted with everything else.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: sound goes to ALSA directly by default; a sound server is
  available but is not part of the default runtime.
  Rationale: the runtime is meant to be exactly the daemon, chronyd, Xorg,
  x11vnc and Chromium. Chromium can open an ALSA device by name, and the
  daemon already enumerates the machine's sound cards for the interface, so
  choosing an output needs no extra process. `audio.server: pulseaudio` remains
  for the cases ALSA cannot serve — two programs wanting the card at once, or
  per-application volume — and it is off unless asked for.
  Date/Author: 2026-08-22, Ziyan Zhou

- Decision: touchscreen support is limited to what is true of every touch
  device: finding them, and pointing each one at the right screen.
  Rationale: an earlier project of the author's carried firmware upgrades and
  a glove mode for two specific panels. That belongs with those panels. What
  generalises is the part that is wrong on every multi-screen or rotated
  machine until somebody fixes it: a touch device reports coordinates across
  the whole framebuffer, so on a rotated screen, or a machine with two
  outputs, touches land in the wrong place. The daemon sets each touch
  device's coordinate transformation matrix from the geometry and rotation of
  the output it belongs to, which is what makes a portrait screen in a lobby
  respond where it is touched.
  Date/Author: 2026-08-22, Ziyan Zhou

## Outcomes & Retrospective

To be written at the end of each milestone.

## Context and Orientation

This repository is empty apart from `.git`. Everything described below is new.
The conventions it follows are the author's, taken from `teanode`: an
`AGENTS.md` that orients a newcomer, a `CONTRIBUTING.md` that states the
naming rules, decision records under `docs/decisions/`, a `Makefile` that is
the only entry point anybody needs to remember, vendored dependencies, and
`self` as the receiver name on every method.

The finished layout is:

    main.go                 command line entry point
    cmd/                    one file per subcommand
    internal/
      version/              build stamp
      config/               cue.yaml: types, defaults, validation, atomic writes
      supervise/            starting, watching and stopping child processes
      xserver/              Xorg and Xvfb lifecycle, Xauthority, generated config
      display/              the RandR client: outputs, modes, framebuffer, DPMS
      browser/              Chromium lifecycle and everything driven over CDP
      vncserver/            x11vnc lifecycle
      audio/                PulseAudio lifecycle and sound device inventory
      timesync/             chronyd lifecycle and clock status
      hardware/             machine inventory and metrics
      input/                touch devices, mapped to the output they belong to
      watchdog/             liveness probing and the recovery ladder
      fleet/                optional enrolment into cue.sh and the tunnel
      web/                  HTTP server, sessions, API, embedded interface
      daemon/               composition root: wires the above together
      util/                 small, independently testable pieces
    web/                    the browser interface source (React, TypeScript)
    deploy/                 Dockerfile, compose files, image assembly script
    docs/                   decisions, reference, coding conventions, planning

## Plan of Work

### Milestone 1 — the skeleton

Create `go.mod` for `github.com/ziyan/cue`, a `Makefile` whose targets are
`build`, `test`, `format`, `check`, `lint`, `lint-ci`, `docker` and `clean`, a
`.golangci.yml` and a `mulint.yaml` copied in spirit from `teanode`, the MIT
`LICENSE`, `README.md`, `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`,
`CHANGELOG.md`, and the GitHub Actions workflow that runs build, vet, lint and
test. Add `internal/version` with `version` and `commit` variables set by
`-ldflags`, and `main.go` with `cue version`.

At the end of this milestone `make build && ./build/cue version` prints
something like `cue 0.1.0 (abcdef…)`, and `make lint-ci test` passes.

### Milestone 2 — configuration

`internal/config` defines the whole of `cue.yaml`, validates it reporting every
problem at once with the YAML path of each, applies defaults, and writes the
file atomically. It is the source of truth for everything an operator can set,
and both a hand edit plus `SIGHUP` and a change made through the web interface
go through it.

The shape, in full, is the reference document `docs/reference/configuration.md`
generates from. Sketched:

    device:
      name: living-room
      timezone: Asia/Tokyo
    display:
      cursor: false
      blankAfter: 0s
      framebuffer: ""
      outputs:
        - name: "*"
          mode: preferred
          position: 0x0
          rotate: normal
    playlist:
      interval: 30s
      items:
        - url: https://example.com/
          duration: 60s
          reload: true
          login:
            whenUrlMatches: "^https://[^/]+/login"
            usernameSelector: "input[name=username]"
            passwordSelector: "input[name=password]"
            submitSelector: "button[type=submit]"
            username: display
            password: "..."
            expectUrlMatches: "/protect/dashboard"
    watchdog:
      enabled: true
      interval: 15s
      timeout: 10s
      failuresBeforeReload: 2
      failuresBeforeRestart: 6
    browser:
      sandbox: true
      extraArguments: []
    vnc:
      enabled: true
      listen: 127.0.0.1:5900
      password: ""
      viewOnly: false
    web:
      listen: :8080
    audio:
      enabled: true
      sink: ""
    time:
      enabled: true
      servers: [pool.ntp.org]
    fleet:
      enabled: false
      url: https://cue.sh
      enrollmentToken: ""

Acceptance: `cue config init` writes a commented default file; `cue config
show` prints the effective configuration with secrets redacted; a file with a
bad duration fails with a message naming the field.

### Milestone 3 — supervision and PID 1

`internal/supervise` starts a child process from a `Spec` (argv, environment,
uid/gid, working directory), pipes its standard output and error into the
daemon's logger line by line with a prefix, restarts it with exponential
backoff when it exits, and stops it on demand with SIGTERM followed by SIGKILL
after a grace period. A `Group` starts several in a defined order and stops
them in reverse.

Because the daemon is process 1, it must also reap orphaned children — Chromium
leaves several behind whenever a renderer crashes, and a container whose
process 1 does not reap fills with zombies. `internal/util/reaper` runs a
`wait4` loop for that.

Acceptance: unit tests that supervise a process which exits immediately and
observe the backoff growing; a test that a stopped group's processes are gone;
a test that the reaper collects a child it did not start.

### Milestone 4 — the X server and the display

`internal/xserver` writes an Xauthority file with a fresh random cookie (the
format is simple enough to write from Go and is implemented in
`internal/util/xauth`), generates a minimal `xorg.conf.d` fragment, starts
either `Xorg` or `Xvfb` depending on configuration, and waits for
`/tmp/.X11-unix/X0` to accept a connection before reporting ready.

`internal/display` connects to the X server as an ordinary client and does what
`monitor` used `xrandr` and `xset` for: enumerate outputs, create a custom mode
from a modeline if one is configured, pick each output's mode, set the
framebuffer size, position the outputs, turn off DPMS and the screen saver, and
hide the cursor. It then keeps watching: when `/sys/class/drm` says a connector
changed, it reapplies the configuration.

Acceptance: `cue display probe` run against an `Xvfb` started by hand prints
the screen size and the outputs. On real hardware the attached screen changes
resolution when `display.framebuffer` changes and the daemon is reloaded.

### Milestone 5 — the browser, the login rules and the watchdog

`internal/util/cdp` is a small client for the Chrome DevTools Protocol: the
HTTP endpoints (`/json/version`, `/json/list`, `/json/new`, `/json/activate`,
`/json/close`) and a WebSocket connection for the commands that need one
(`Page.navigate`, `Page.reload`, `Page.captureScreenshot`,
`Runtime.evaluate`, `Network.clearBrowserCache`).

`internal/browser` starts Chromium as an unprivileged user with the kiosk
flags, waits for the debugging port, opens one tab per playlist item, and
rotates between them on a timer. The Chromium process is started with its disk
cache in a directory under `/run/cue` that the daemon empties first, so a
corrupted cache cannot survive a restart and cannot become a permanent fault.

**Login rules.** Each playlist item may carry a `login` block. A rule is
evaluated every few seconds against the tab, not once when the tab opens,
because the case it exists for is a dashboard whose session expires and which
then redirects itself back to a login page. A rule says how to recognise that
the page needs logging in (a URL pattern such as `^https://[^/]+/login`, or a
CSS selector that only the login form has), what to type where (a selector for
the user field, one for the password field), what to click, and how to know it
worked (a URL pattern or selector that should appear afterwards). The daemon
performs it with `Runtime.evaluate`, dispatching real `input` and `change`
events so that frameworks which track their own state notice the typing.

Credentials are written in the configuration file. They are redacted in every
log line, in the API and in the web interface, and are never included in a
support bundle.

**The watchdog.** A display that has frozen looks exactly like a display that
is working, so the daemon has to ask. Every interval it runs a probe with a
deadline: a round trip to the X server, then a `Runtime.evaluate` on the
active tab, then a promise that resolves on the next animation frame — the
three together separate a dead browser from a wedged renderer from a renderer
that runs but never paints. Failures escalate on a ladder, each step with its
own cooldown so a slow page is not restarted into a loop: reload the tab, then
recreate it, then clear the browser cache and reload, then restart Chromium,
then restart the X server as well. Every step is logged with what was observed
and what was done, and the counts are shown in the web interface, because the
useful question later is not "is it up" but "how often does it have to be
rescued".

The probe is suspended whenever the browser is deliberately stopped, so a
planned restart never counts as a failure.

Acceptance: with the daemon running against Xvfb, the screenshot endpoint
returns a PNG whose dimensions match the framebuffer, and its content changes
after the rotation interval. A test that suspends the renderer with
`Debugger.pause` and observes the watchdog escalate through the ladder. A test
against a local page that behaves like the motivating dashboard — it redirects
to a login form, accepts one username and password, and expires the session
after a few seconds — which the login rule keeps logged in for a minute
without help.

### Milestone 6 — VNC

`internal/vncserver` supervises `x11vnc` bound by default to localhost only,
writing a password file when one is configured. `internal/web` bridges an
authenticated WebSocket to it, in the same shape as the portal's
`backend/api/apivnc/bridge.go`: a read pump, a write pump, a ping ticker, a
mutex around the writes, and an origin check.

Acceptance: the web interface's screen tab shows the display and a click moves
the mouse pointer on the real screen.

### Milestone 7 — the web interface

`internal/web` serves: `/healthz`, a JSON API under `/api/v1/`, the WebSocket
bridge, and a single-page interface built from `web/` and compiled into the
binary with `go:embed`. Authentication is a session cookie backed by an
argon2id password hash kept in the configuration; the first run has no password
and every page redirects to an onboarding wizard that sets one, names the
device, picks a timezone and collects the first playlist.

The interface has four screens: **Overview** (status of every managed process,
CPU, memory, disk, temperature, uptime, current URL, a live screenshot),
**Content** (edit the playlist, including per-item login), **Screen** (noVNC),
and **Device** (outputs, sound devices, cameras, clock, and the fleet enrolment
form).

Acceptance: from a laptop on the same network, opening the device's address
walks through onboarding, and afterwards the overview updates every few
seconds without reloading.

### Milestone 8 — audio, time, hardware

`internal/audio` supervises PulseAudio in system mode and lists sinks and
sources. `internal/timesync` supervises chronyd and reports the clock's state.
`internal/hardware` gathers the inventory and metrics the overview shows,
reading `/proc` and `/sys` directly with no external dependency, and detects
cameras with the V4L2 `VIDIOC_QUERYCAP` ioctl.

Acceptance: plugging in a USB sound card makes it appear in the Device screen
within a few seconds and it can be chosen as the output.

### Milestone 9 — the image

`deploy/Dockerfile` builds the interface, builds the binary, assembles a root
filesystem from Debian packages with `deploy/collect-rootfs.bash`, and copies
that plus the binary onto `gcr.io/distroless/base-debian13`.
`deploy/docker-compose.yml` runs it with host networking, the devices it needs
and a persistent state volume.

Acceptance: `make docker` succeeds; `docker run --rm cue:dev version` prints
the version; and a CI job runs the whole daemon inside the image against Xvfb
and asserts that `/healthz` is healthy and the screenshot endpoint returns a
1280x720 PNG.

### Milestone 10 — fleet enrolment

`internal/fleet` posts the enrolment token to `cue.sh`, stores the credential
it gets back under `/var/lib/cue`, then maintains one outbound WebSocket
carrying a yamux session. Streams the service opens on it are handed to the
same HTTP handler and VNC bridge the local interface uses, so the service gets
exactly the access an operator standing in front of the machine would have, and
nothing more.

Acceptance: with a stub server in a test, enrolment stores a credential, the
tunnel reconnects after the server drops it, and an HTTP request made through
the tunnel reaches the daemon's API.

### Milestone 11 — documentation

`README.md` for someone who has never seen the project, `docs/reference/` for
configuration, the API and local development, `docs/decisions/` for the
choices above, and the release workflow that publishes binaries and a
multi-architecture image to `ghcr.io`.

## Validation and Acceptance

Every milestone states its own acceptance above. The overall acceptance is the
transcript in the Purpose section: a bare Debian machine with Docker, one
`docker run`, a lit screen, and a working web interface on port 8080.

## Idempotence and Recovery

Every step here is additive; nothing removes or migrates existing state.
`cue config init` refuses to overwrite an existing file unless `--force` is
given. The daemon creates `/var/lib/cue` and `/run/cue` if they are missing and
tolerates them already existing. Re-running `make docker` rebuilds from cache.

## Interfaces and Dependencies

Third-party libraries, and why each is there:

- `github.com/urfave/cli/v3` — the command line, as in `teanode`.
- `github.com/op/go-logging` — logging, as in `teanode`.
- `gopkg.in/yaml.v3` — the configuration file.
- `github.com/jezek/xgb` — the X protocol from Go, including the RandR, DPMS,
  XFIXES and XTEST extensions. Chosen over shelling out to `xrandr` because
  the image has no shell.
- `github.com/gorilla/mux` and `github.com/gorilla/websocket` — the HTTP router
  and the WebSocket bridge, matching the portal's VNC bridge.
- `github.com/hashicorp/yamux` — stream multiplexing for the fleet tunnel.
- `golang.org/x/crypto` — argon2id for the administrator password.
- `@novnc/novnc` — the browser-side VNC client, as in the portal.

The interfaces that must exist at the end:

In `internal/supervise`:

    type Process interface {
        Start()
        Stop(ctx context.Context) error
        State() State
    }

In `internal/display`:

    type Display interface {
        Apply(configuration *config.Display) error
        Outputs() ([]Output, error)
        Close() error
    }

In `internal/browser`:

    type Browser interface {
        Show(ctx context.Context, url string) error
        Screenshot(ctx context.Context) ([]byte, error)
        State() State
    }

## Artifacts and Notes

To be filled in as milestones land.
