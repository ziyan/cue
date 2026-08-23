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
- [x] (2026-08-23 03:30Z) Milestone 8 — audio, time synchronisation, hardware
      inventory, and touch devices found and reported (completed: detection,
      reporting, and telling the browser touch exists; remaining: mapping a
      touch device to one output on a multi-screen or rotated machine, which
      needs XInput2 and is documented as not done).
- [x] (2026-08-23 02:45Z) Milestone 9 — the distroless image, compose files,
      end-to-end smoke test. `make docker-smoke` passes.
- [x] (2026-08-23 03:20Z) Milestone 10 — optional fleet enrolment. Proved with
      a stub service in `internal/fleet`: the device enrols, the token is
      cleared, the tunnel comes up, and the service makes an HTTP request back
      down it that reaches the device's own handler.
- [x] (2026-08-23 03:00Z) Milestone 11 — documentation, decision records,
      release workflow.
- [x] (2026-08-23 14:22Z) Validation on real hardware. Deployed to the test
      machine and confirmed by a picture of the panel: UniFi Protect full
      screen at 2560x1440, signed in by the login rule, no browser chrome,
      four live camera feeds, all four programs running with no restarts.
      Five faults were found and fixed in the doing; see Surprises.
- [x] Superseded note — what follows described the wait for that machine. The
      machine set aside for it (`carbon`) went off the network partway through
      and has not come back after forty minutes of polling; it is not in this
      machine's neighbour table, so it is powered down rather than merely
      unreachable. When it returns, the whole of it is one command:

          make deploy HOST=carbon DISPLAY_MANAGER=stop \
              CONFIG=deploy/examples/kiosk-with-login.yaml

      In its absence
      the X server was run against this machine's own graphics hardware from
      inside the image, with `-novtswitch` so it could not take the console
      away from whoever was using it. It got as far as driving the card:
      it loaded the modesetting driver, enumerated the real outputs with their
      real modes (eDP-1 at 1920x1200, two DisplayPort outputs at 3840x2160)
      and initialised RANDR, before stalling where a second X server on a
      machine whose graphics device is already taken would be expected to.
      That leaves only the last step unproved — a server that actually gets
      the device — and it found two container faults on the way.

- **Milestone 12 — dark mode and network management (2026-08-23).** Chromium
  now defaults to dark mode. `internal/network` manages wired and wireless
  interfaces: it reads them from netlink, drives `wpa_supplicant` over its
  control socket to scan and join, applies static addresses or takes a DHCP
  lease, and reconciles every 30 seconds. Off by default, and only interfaces
  named in the configuration are touched, so turning it on cannot take a
  working device off the network. A Network page in the web interface scans
  and joins. It needs the host network namespace and `CAP_NET_ADMIN`, which
  the deployment already grants; where it does not have them the page says so
  instead of showing the container's own interfaces as if they were the
  machine's.

- **Milestone 13 — the review against what came before (2026-08-23).** Read
  `ziyan/monitor`, the device it runs on, `pendant` and the hypervisor's
  network service for lessons this project had not picked up, and fixed what
  was found. Verified on carbon: the dashboard is back, full screen, four live
  cameras, signed in, dark. The findings are in Surprises & Discoveries.

## Surprises & Discoveries

- Observation: 61% of a device's log was one line.
  Evidence: on carbon, `docker logs cue | grep -c dbus-core` was 523 of 862
  lines after an hour. The X server's dbus-core module cannot reach a system
  bus — there is none in this image, and nothing here needs one — and retries
  every ten seconds forever, marked (EE).
  Implication: an appliance that runs for years cannot have an unbounded
  repeating line, and not because of the disk: an operator looking for why a
  screen went blank has to read past six of these a minute, and (EE) reads as
  a fault. `supervise.Repetition` names such a line in advance, logs the first
  whole with an explanation of why it does not matter, and counts the rest.
  Nothing unrecognised is ever touched.

- Observation: nothing bounded the log at all.
  Evidence: neither `deploy/docker-compose.yml` nor the deploy tool set
  `max-size`, and Docker's json-file driver keeps everything. On the device
  this project replaces, `/var/log/monitor/chromium.log` had rotated five
  times in a day — and between 03:03 and 03:15 it turned over 10 MB every four
  minutes.
  Implication: both deployment paths now cap it. The verbose browser logging
  that caused it there (`--enable-logging --log-level=0 --v=1`) is not
  something cue passes.

- Observation: a fixed debugging port was not one bug but two, and the second
  was caused by fixing the first.
  Evidence: fixed at 9222, the daemon drove another container's Chromium.
  Changing the default to 0 fixed new devices and did nothing for carbon,
  because 9222 was written into its configuration file — and by then
  docker-proxy held the port, so the browser never came up at all.
  Implication: the setting is gone. The port is always chosen by the browser
  and always read from `DevToolsActivePort` in its own profile. A value that
  is only ever correct as one number is not a setting, and leaving it as one
  meant the fix could not reach the devices that needed it.

- Observation: removing a setting broke every device that had one.
  Evidence: the configuration loader used `KnownFields(true)`, so the removed
  `debuggingPort` made carbon refuse to start — and it logged `yaml: unmarshal
  errors:` with nothing after it, because go-yaml puts the detail in indented
  lines that `%w` does not print.
  Implication: an unknown name is now reported and skipped rather than fatal,
  and shown in the interface, because a mistyped key and a setting that does
  nothing look identical from in front of the screen. Anything else wrong with
  the file is still refused. Two requirements pulled against each other here
  and both were real; the resolution was to make it visible rather than to
  pick one.

- Observation: the wireless passphrase would have gone into the log.
  Evidence: `ask` and `mustSucceed` put the whole command into the error, and
  the command carrying the passphrase — `SET_NETWORK 0 psk "…"` — is the one
  most likely to fail. The hypervisor's own implementation has this bug in the
  same place (`log.Debugf("configuring wireless ssid %s psk %s")`), which is
  how it was noticed.
  Implication: errors now name the command by a redacted form. The log is read
  in the interface and, on an enrolled device, sent to the fleet service, so a
  passphrase reaching it has to be treated as disclosed.

- Observation: two `--enable-features` flags mean the first is discarded.
  Evidence: found while adding the overlay-scrollbar features next to the
  existing dark-mode one. Chromium keeps only the last, silently — so a
  setting that looked applied would not have been, including any an operator
  added by hand.
  Implication: features are collected and emitted once, and an operator's own
  `--enable-features` is merged rather than allowed to replace them.

- Observation: a page that opens a window covers the dashboard indefinitely.
  Evidence: spare windows were only dealt with when the playlist was applied,
  which happens on a restart or a configuration change. A screen showing one
  page — the ordinary case, and the case this project was built for — never
  reaches that.
  Implication: windows the daemon did not open are closed on the loop that was
  already waking every five seconds, after one cycle's grace so that a window
  a page opens and closes itself is left alone.

- Observation: the correct answer to a self-signed certificate was not
  available.
  Evidence: `pendant` builds an NSS database with `certutil` and trusts the
  site's own certificate; cue offered only `ignoreCertificateErrors`, which
  stops the browser checking anything, on every page, for the life of the
  process.
  Implication: `browser.certificateAuthorities` trusts a pasted certificate by
  name and leaves every other check in place.

- Observation: dark mode was on, and the screen was white.
  Evidence: three flags were passed for it. Grepping the Chromium binary in
  the image for each: `force-dark-mode` present, `force-prefers-color-scheme`
  absent, `WebUIDarkMode` absent. The two absent ones were inherited from the
  older setups this project replaces, and Chromium ignores a switch it does
  not know without a word. Evaluating `matchMedia('(prefers-color-scheme:
  dark)')` in the live page on carbon returned true — so the one real flag was
  doing its job — while `getComputedStyle(document.body).backgroundColor` was
  `rgb(255,255,255)`: the dashboard has a theme of its own and ignores the
  preference entirely.
  Implication: two things, not one. `darkMode` tells a page we prefer dark;
  `forceDarkContent` inverts a page that will not listen. And a flag list is
  not evidence: `make docker-smoke` now measures the average brightness of a
  real screenshot, which went 237.6 → 40.4 out of 255 on the same page when
  the second was turned on.

- Observation: a restart applied nothing.
  Evidence: `Settings()` computed `Arguments: self.arguments()` once, when the
  process was created; `Restart()` re-execs the stored list. Changing
  `extraArguments` on carbon logged "the change needs the browser restarted",
  restarted it, and the new command line was identical to the old one.
  Implication: every setting that is only readable from a command line —
  dark mode, the sandbox, certificate errors, the VNC listen address, the
  screen size — could be saved, reported as applied, and not be in force.
  `Settings.BuildArguments` is called before every start instead. This is the
  same failure as the debugging port and the dead dark-mode flags: something
  that reports success and does nothing.

- Observation: the "On the screen" card blinked, and cost 110 MB a minute.
  Evidence: reported by the user. Measured in a real browser driven over CDP:
  four new `<img>` elements created in ten seconds, one per refresh, because
  the page is rebuilt from scratch and a fresh `<img>` holds nothing until its
  picture arrives. The picture itself was a full-resolution lossless PNG of
  the screen — 5.6 MB on carbon — fetched every three seconds.
  Implication: the element is kept across redraws and the next picture is
  decoded before being swapped in (measured again afterwards: zero new
  elements, zero blank frames), and the interface asks for a scaled JPEG.

- Observation: the browser filled 1152x864 of a 1280x1024 screen.
  Evidence: the second machine migrated to cue. `apply.go` logged "screen
  1280x1024; VGA-1 1280x1024@60", correctly, and `layout.go` then logged "the
  screen is 1152x864" — the size read back a fifth of a second later.
  `Screen()` read the X connection setup block, which is sent once when the
  client connects and never updated, so it still held the size the server
  started at. Everything downstream was given a stale number and used it
  faithfully.
  Implication: it asks the root window now, which RandR does update. carbon
  never showed this because its X server happened to start at the size it was
  about to be set to — the bug needed a machine whose starting mode differed,
  and there was exactly one.

## Not done, and why

- **Forcing a disconnected output on.** `pendant` writes `on` to
  `/sys/class/drm/<connector>/status` so a machine with nothing plugged in
  still has a screen. Two things stopped this being copied: sysfs is read-only
  in a container that is not privileged, and the alternative — generating a
  Monitor section with `Option "Enable"` and a modeline — runs straight into
  this project's standing decision to generate no device or monitor sections,
  which is there because a generated xorg.conf is historically the reason a
  screen stays black. It is also unverifiable on carbon, whose panel is
  attached. Left as a documented recipe rather than machinery built on a guess.

- **Detaching a touchscreen from the core pointer.** `pendant` runs
  `xinput float` so that, with no window manager, Chromium is not confused by
  the legacy pointer motion the X server synthesises. The lesson is
  model-independent and real, but implementing it means XI2 `XIChangeHierarchy`
  in `internal/display`, and there is no touchscreen here to test it against.
  Shipping an untested change to input handling is worse than not shipping it.


- Observation: a fixed `--remote-debugging-port=9222` made `cue` drive a
  *different* browser.
  Evidence: on carbon, `teanode-chrome-1` publishes 9222 on the host. With
  `--network host`, the daemon's CDP client connected to that container's
  Chromium. Every call succeeded, so nothing was logged as wrong; what was
  visible was a screen that never changed, screenshots of the wrong machine,
  a certificate error for a page this browser had never been asked to load,
  and a window stuck at 800x600.
  Implication: a debugging port must never be a fixed number. `cue` now passes
  `--remote-debugging-port=0` and reads the port the browser chose out of
  `DevToolsActivePort` in its own profile directory, which cannot resolve to
  anybody else's browser. The general lesson is broader than this one flag:
  when a component addresses a peer by a well-known port on a shared network
  namespace, "it answered" is not evidence that the right thing answered.

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

- Observation: the container device list documented in
  `deploy/docker-compose.yml` was not enough for the X server, and would have
  failed on the first real device with a message naming neither the container
  nor the setting.
  Evidence: run against real graphics hardware with only `/dev/dri`,
  `/dev/tty0` and `/dev/input` passed through, the X server got as far as
  probing the card and then died with "(EE) xf86OpenConsole: Cannot open
  virtual console 3 (No such file or directory)". Left to itself it asks the
  kernel for a free console, is told a number, and opens the device for that
  number — which is not in the container. `display.virtualTerminal` now names
  the console and the compose file passes that one device.

- Observation: passing `-configdir` to a directory containing no `.conf` files
  makes the X server log "(EE) Unable to locate/open config directory", which
  is not a fault. A spurious error in the log of a machine whose screen is
  black is worse than useless, so the argument is now passed only when there
  is a configuration to read.

- Observation: the X server writes almost nothing to its output and everything
  to its own log file, so a failed start reported only "exited before it was
  ready". The supervisor now takes an OnStartFailure hook and the X server
  uses it to print the end of that log — which, on a machine with no shell, is
  otherwise unreachable.
  Evidence: a run with no graphics device now ends with the log's own last
  words, "(EE) Screen 0 deleted because of no matching config section", which
  says exactly what is wrong.

- Observation: the browser would have rendered in software on every real
  device, silently. It runs as an unprivileged account — it must, because
  Chromium refuses its own sandbox as root — and the graphics devices are
  owned by groups whose numbers come from the host: `render` is 992 on this
  machine and a different number on the next, and no such group existed in the
  image at all. The account was therefore in none of them.
  Evidence: `/dev/dri/renderD128` is `root:render` mode 660; the browser
  process's `/proc/<pid>/status` showed `Groups: 1000`. The daemon now reads
  the numbers off the device files at run time and starts the browser in them,
  and the same line reads `Groups: 44 992 1000`. Nothing would have failed
  outright — Chromium falls back to software rendering and says so only in a
  log nobody reads — which is why this had to be looked for rather than waited
  for.

- Observation: the layout ignored a written output position. The default
  configuration's wildcard entry says `0x0` and every output was laid out left
  to right regardless, so a laptop with something in its HDMI socket — which
  is exactly what the test device is — would have got a drawing surface twice
  as wide and one browser window spanning both screens.
  Evidence: `TestAWrittenPositionIsHonoured`. Every output at `0x0` now means
  they are mirrored, which is what a display appliance wants and what the
  system this replaces did.

- Observation: the default configuration did not work. Every test until now
  had set `browser.sandbox: false` for convenience, and with the default of
  true the browser does not start in a container at all: its sandbox creates
  process and network namespaces, and the default seccomp policy refuses that
  without CAP_SYS_ADMIN.
  Evidence: "Failed to move to new namespace: PID namespaces supported,
  Network namespace supported, but failed: errno = Operation not permitted",
  which names neither the container nor the setting, and sends the reader to
  look at the setuid bit on a helper binary that is perfectly correct. The
  capability is granted now in both places that start a container, seccomp
  stays on because the policy is capability-aware, and the renderer's
  `/proc/<pid>/ns/pid` confirms the sandbox is real. This is the second time
  testing the defaults rather than the convenient configuration found
  something; the lesson is now in the retrospective twice.

- Observation: a program that dies quickly took its own error message with it.
  The supervisor read the child's output in two goroutines and reported what
  it had when the process exited, so a program that printed a reason and
  exited two hundred milliseconds later was reported as "exited before it was
  ready" with nothing else — precisely the case where the reason matters.
  Evidence: the first run with the sandbox enabled logged "what it said before
  giving up:" followed by nothing at all. The pumps are now drained, with a
  two-second bound so that a leaked pipe cannot stop the supervisor
  supervising.

- Observation: a protocol command sent on a connection that had just died
  returned *success*. The reader closes every pending reply channel when the
  connection goes, and the caller read the zero value out of the closed
  channel as a reply with no error and no result — so every command that wants
  no result back, which is navigating, reloading and switching tab, reported
  that it had worked. A kiosk would sit on the wrong page with nothing in any
  log to say why.
  Evidence: `TestACallOnAClosedSessionFailsRatherThanHanging` passed the call
  and failed the assertion. A receive from a closed channel is now told apart
  from a real reply.

- Observation: a tab's protocol connection was cached and never invalidated,
  so a renderer that crashed left a dead connection behind and every rule and
  every probe on that tab failed forever — while the browser itself was
  perfectly healthy and the watchdog would eventually answer by restarting a
  browser that did not need it. A cached session is now asked whether it is
  still open before it is handed out.

- Observation: the release workflow would have failed. It publishes the image
  for amd64 and arm64, and two of the X drivers it installed — the Intel one
  and the VESA one — are built only for x86. The arm64 half of the build would
  have stopped with "Unable to locate package", after every other stage had
  already succeeded, at the moment of the first release.
  Evidence: Debian's own arm64 index has no `xserver-xorg-video-intel` and no
  `xserver-xorg-video-vesa`. They are installed only on x86 now, and
  `tools/checkpackages` reads the lists out of the Dockerfile and checks each
  package against Debian's index for each architecture — ten seconds, against
  twenty minutes to find out by building under emulation. Putting the fault
  back makes it say so: "arm64: xserver-xorg-video-intel is not built for this
  architecture; the release build would fail".

- Observation: five faults stood between "the daemon reports itself healthy"
  and "the dashboard is on the screen", and every one of them was invisible to
  the daemon's own view of the world. They were found by taking a picture of
  the screen from outside the browser and noticing it did not match what the
  browser said it was showing.

  1. The browser window was 800x600 on a 2560x1440 screen. `--kiosk` and
     `--start-fullscreen` work by asking a window manager, and there is no
     window manager in this image, so both were accepted and did nothing. The
     window is now sized over the protocol, which asks nobody.
  2. Sizing a window takes it *out* of full screen, so the first fix put a tab
     strip and an address bar on the wall. The size and the state have to be
     set in two calls, because the protocol refuses a request naming both.
  3. Chromium refused to start after any redeployment: its profile lock records
     the host name, a container gets a new one each time, and the lock is then
     read as "in use by another Chromium process on another computer". The
     daemon starts the only browser that uses that profile, so it clears the
     lock.
  4. The page would not load: `net::ERR_CERT_AUTHORITY_INVALID`, although
     `--ignore-certificate-errors` was on the command line. It turns out
     Chromium does not honour that flag in the host's network namespace.
     Public certificates work either way, so nothing looks wrong until the one
     page the screen exists for is the one that fails. The container uses
     published ports now.
  5. The daemon asked for the tab list before the browser had opened a window,
     was told there were none, and opened one of its own — which in kiosk mode
     is a second window, not full screen and not in front. The readiness check
     waits for the first window now.

  Evidence: a photograph of the panel is in the retrospective's transcript —
  four live camera feeds, full screen, signed in, on real hardware.

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

**As of 2026-08-23.** Everything in the Purpose section works except the last
step of it: a picture on a real screen, driven by Xorg on real graphics
hardware. The shipped example configuration was then rehearsed in full, with
only the two things a machine whose graphics device is already taken forces —
a virtual screen instead of Xorg — changed:

    device   : Reception | timezone Europe/London
    programs : xvfb running, chromium running, x11vnc running, chronyd running
               (no restarts)
    showing  : Dashboard | https://<the real controller>/protect/dashboard/all
    logins   : 1
    watchdog : 0 failures, 0 remedies
    clock    : synchronised, stratum 4, off by 0.3 ms
    screen   : 1920x1080

That is the browser sandboxed, in the host's hardware groups, signed into the
real dashboard, with the clock right and nothing having had to be restarted.

It was then left running for half an hour against the live dashboard, because
this is meant to run for months and had only ever been run for minutes:

    t+2m   mem=609MiB  restarts=0 watchdog=0 failures  showing the dashboard
    t+16m  mem=619MiB  restarts=0 watchdog=0 failures  showing the dashboard
    t+30m  mem=583MiB  restarts=0 watchdog=0 failures  showing the dashboard

Memory oscillates between about 560 and 620 megabytes with no trend, which is
four live camera streams being decoded and nothing accumulating: no leak in
the daemon, and none in the cached protocol connections, which was the thing
worth checking. Nothing was restarted, the watchdog never failed a probe, the
session did not expire, and the clock stayed within a fifth of a millisecond.

The remote view was then proved too, which nothing else exercises: the smoke
test opens the WebSocket the browser's viewer uses and reads
`RFB 003.008` back through it, then answers with the same version and reads
the server's reply. That single exchange can only happen if the WebSocket
upgraded, the origin check passed, the session was accepted, the bridge
dialled the VNC server, and the VNC server is attached to a running display. The machine set aside for that (`carbon`) answered at the start of
the work and went off the network an hour in; it has not come back. Everything
else is proved, and proved by running rather than by reading:

- The whole daemon runs in the distroless image against a virtual screen and
  puts a picture on it. `make docker-smoke` checks that on every change: every
  program running, a screenshot of the configured size, the playlist rotating,
  the watchdog satisfied.
- The login rule was run against the real dashboard it was built for. It
  recognised the login page, filled the fields, ticked "remember my
  credentials", waited out a submit button that starts disabled, and landed on
  the camera dashboard showing four live feeds.
- The web interface was exercised by having the daemon sign itself into its
  own interface with a login rule, which is a pleasing way to test both halves
  at once.
- The clock synchronises: stratum 3, eighty microseconds out, reported through
  the API.
- The fleet tunnel was proved against a stub service: the device enrols, the
  token is cleared, the tunnel comes up, and the service makes an HTTP request
  back down it that reaches the device's own handler.
- `cue display probe` was run against real graphics hardware and correctly
  reported the connectors, the monitors' names from their EDID, and their
  modes.

**What is left.** Real-hardware Xorg, which needs `carbon` back. The X server's
command line and the container's device access are written and reasoned about
but have never been run against a graphics card, and that is exactly the sort
of thing that is wrong the first time. Also outstanding: mapping a touch device
to one output on a multi-screen or rotated machine, which needs XInput2.

**What was learned.** Two lessons, both of which cost time and both of which
are worth repeating to whoever works on this next.

The first: test the defaults. Every early test used the development
configuration, and the first run with the daemon's own defaults broke four
different ways at once — the X server was a shell script, chronyd was not on
the PATH, chronyd wanted an account that did not exist, and chronyd refused the
directory it was given. None of those would have been found by any amount of
reading.

The second: a supervisor that discards a child's output is a supervisor that
cannot be debugged. Three separate rounds of this began with "exited before it
was ready" and nothing else, because the output was logged at DEBUG and the
level was INFO. Keeping the last twenty lines and printing them when a program
fails to start would have saved most of an hour, and now does.

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
