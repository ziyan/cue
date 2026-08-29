# Changelog

All notable changes to this project are recorded here, in the categories of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). This project follows
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-08-28

### Added

The web interface says when a newer release of cue exists and shows what changed in it. A screen that has been on a wall for a year has had no way to tell you that a fix for the thing wrong with it shipped months ago. A device that cannot reach GitHub says so, and goes on showing the last release it heard about with the date it heard — rather than showing nothing, which reads as being up to date. The check runs once a day and when the page is opened, and needs no account or token. (#4)
A device can take the upgrade itself, if it is set up to. Set `upgrade.allowApply` in `cue.yaml` and start the container with `-v /var/run/docker.sock:/var/run/docker.sock`, and the Upgrade page offers a button: it fetches the new image and replaces the container, keeping the playlist, the password, and every flag it was started with. The screen says what is happening, goes blank for about a minute, and comes back. If the new version will not start, or starts and never answers, the device puts the old one back and starts it again by itself. Both are needed and neither is the default. The socket is root on the host, and a screen in a lobby has a web interface reachable by everybody on the network. Without them the page still names the release, shows its notes, and gives you the command to run yourself. (#4)
## [0.2.0] - 2026-08-28

### Added

The image says where it came from. It carried no metadata at all, so a registry had nothing to tie it back to this repository and a person holding it had no way to find the source. It now carries the standard OCI labels, including `org.opencontainers.image.source`, which is the one a registry reads to link the package to its repository — and a linked package can take the repository's visibility instead of being published private, which is how the first release ended up with an image nobody following the README could pull. (#1)
## [0.1.0] - 2026-08-28

### Added

- The image says where it came from. It carried no metadata at all, so a
  registry had nothing to tie it back to this repository and a person holding
  it had no way to find the source. It now carries the standard OCI labels,
  including `org.opencontainers.image.source`, which is the one a registry
  reads to link the package to its repository — and a linked package can take
  the repository's visibility instead of being published private, which is how
  the first release ended up with an image that nobody following the README
  could pull.


### Added

- The image is published to Docker Hub as well as the GitHub registry, as
  `ziyan/cue:latest` and `ziyan/cue:v<version>`. A device that pulls by name
  no longer has to be told about a second registry first. Docker Hub is
  optional: without credentials the release still publishes to the GitHub
  registry rather than failing over a registry it was never going to reach.

- A picture or a video can be an item of the playlist. Upload one from the
  Content page and it becomes an item like any other: reorderable, nameable,
  skippable. It lives on the device's own disk, so a screen goes on showing it
  with no network at all.

  A video plays full screen with no controls and the screen moves on the moment
  it ends, rather than sitting on a frozen last frame for the rest of the
  rotation. A picture has no end of its own, so it stays for the ordinary
  rotation time. Anything that will not load says so on the screen for a few
  seconds and then gives way, because a screen stuck for ever is worse than one
  that skips.

  Sound is per item and off by default: a screen on a wall that starts making
  noise because somebody added a video is a bad surprise, and the person who
  added it may not be in the room.

  Files are stored under a digest of their own contents, so the same file twice
  costs one copy, and a file nothing refers to is deleted when the item that
  named it is.

### Security

- The VNC server no longer listens on every IPv6 address when it was told to
  listen on an IPv4 one. x11vnc honours `-listen` for IPv4 and then opens an
  IPv6 listener on the wildcard regardless, so a device configured for the
  loopback was in fact answering on `[::]:5900` — with no password, while
  holding a globally routable address. Nothing but an upstream firewall stood
  between its screen and anyone who cared to look.

  An IPv4 listen address now means IPv4 only. Naming an IPv6 address, or a
  bare port, still does exactly what it says.

  Closing it takes both `-no6` and `-rfbportv6 -1`. On port 5900 — x11vnc's
  default, and the port every device uses — each flag on its own is accepted
  in silence and the socket stays open; on any other port `-no6` alone is
  enough. Two releases were cut before that was understood, because the test
  written to prove the fix picked a free port and so tested the one case where
  the single flag works.

### Changed

- The on-screen menu and the setup portal ask for a password to be set on a
  device that has none, before they will do anything else. A device with no
  password used to let whoever was standing at it change the network, the
  resolution and the wireless credentials, on the reasoning that there was
  nobody to ask. That is right for a device still in its box and wrong for one
  that has been hung on a wall and never finished setting up — which is the
  state a device stays in for as long as nobody visits its web interface.

  Refusing them would leave somebody with a screen they cannot configure and
  no way to fix it but the interface they could not reach in the first place,
  which is what the on-screen menu exists to rescue. So they are asked to
  choose one, twice, and the device ends up in the state it should have been
  in already.

- The way out of the menu is an X in its top corner rather than a button at
  the foot of the panel. The panel scrolls when the network form is open, so
  the old one sat below the fold exactly when somebody most wanted it.

- The Network page no longer lists the interfaces with no hardware behind
  them. Bridges, container interfaces and tunnels were shown in a collapsed
  list on the grounds that they might explain a routing problem; on a machine
  running containers there are more of them than there are real interfaces,
  and none of them is something anybody can plug a cable into.

### Fixed

- Unlocking the on-screen menu no longer signs the screen's own browser in.
  Typing the password into the menu posted it to the ordinary sign-in
  endpoint, which sets a session cookie valid for twelve hours by default. The
  menu closed and the cookie did not, so the second person to walk up to that
  screen was already inside — on a screen in a lobby or a shop window, that is
  every passer-by for the rest of the day.

  Authority at the screen is now a pass: minted when the daemon serves the
  menu, held only in that page, and destroyed when the menu closes. Nothing is
  written to the browser at all. The setup portal works the same way, so a
  phone that joined a setup network to fix a screen does not walk away signed
  in to it.

- Cutting a release no longer fails its own tests. One test asserted that the
  changelog's Unreleased section was not empty — and cutting a release is
  precisely what empties it, by moving everything into a section named for the
  version. Since the release commit is written by the release itself, every
  release was guaranteed a red tick for a reason that had nothing to do with
  the release.

- A watchdog test no longer fails on a busy machine. It slept for a fixed
  sixty milliseconds and then asserted that a watchdog running on a ten
  millisecond interval had got round to something; on a shared runner it
  sometimes had not. It waits for the thing to happen instead, which is also
  faster when it happens quickly.

- The image is built for arm64 again. Every leg of a multi-architecture build
  read its architecture as `amd64`, so the arm64 build asked for the two X
  drivers that exist only for x86 and apt stopped it. The cause was the
  Dockerfile giving `TARGETARCH` a default value, which shadows the value
  buildx supplies per architecture — the check meant to keep x86-only packages
  off arm64 was written correctly and simply never ran there.

  This is why the first release published binaries and release notes but no
  image. The release now builds and pushes the image before announcing
  anything, so a version that exists is a version that finished.

- The Screen page showed nothing. The connection to the VNC server was
  constructed in a block of code that was removed along with the rotation
  experiment sitting next to it, leaving the rest of the page referring to a
  connection that was never made. The page threw as it drew and stopped there.

  Every page is now opened in a real browser by a test, and an exception
  nothing catches fails the build. Neither Go nor `node --check` can see this:
  a name that is used and never declared is valid syntax, so it parses clean
  and throws only when the page is drawn. That had reached a device three
  times.


### Removed

- Fleet enrolment, and with it every outbound connection a device made of its
  own accord. `internal/fleet`, the `fleet:` configuration section, the two API
  endpoints and the card in the interface are gone. A device now talks to
  nothing but what its playlist points at.

  The tunnel carried an authentication bypass — a request arriving on it was
  treated as signed in, without a password — which was sound while the tunnel
  authenticated the device first, and is exactly the sort of thing that must
  not be left behind once the feature justifying it is gone. It was removed
  with it.

  A `fleet:` section left in a configuration file is reported and ignored
  rather than refused, so a device already in service upgrades without being
  edited first.

### Fixed

- A deployment that cannot reach the machine fails instead of hanging. The
  send handed the pipe's read end to ssh but kept its own copy, so ssh dying
  did not close the pipe, `docker save` never got a broken pipe, and it blocked
  on a full buffer with nothing at the other end — the send did not fail, it
  stopped, quietly, for as long as anybody would wait. Against a host name that
  does not resolve it now fails in under a second, naming both the ssh status
  and the broken pipe, where before it sat for over two minutes.

- A deployment sends the image before it stops anything. The order was: remove
  the running container, stop the display manager, then send eight hundred
  megabytes — so a transfer that failed left the machine with no container, no
  display manager, and no image to start one from. It happened: a link between
  two sites went away mid-send and a screen was dark for seventy minutes, with
  every step that had run having succeeded. Loading an image a machine already
  has is quick, and a running container keeps its own reference to the image it
  started from, so sending first disturbs nothing and a failure now leaves the
  screen showing what it was showing.

- The sign-in box no longer stretches across the whole window. Removing a block
  from the stylesheet by cutting between two landmarks took everything between
  them, because the second landmark had moved to the end of the file — the
  sign-in box's width, the message styling and the table rules went with it.
  Nothing failed: the page rendered, every test passed, and the only way to
  notice was to look at it. `TestEveryClassTheInterfaceUsesIsStyled` now checks
  that every class the interface puts on an element has a rule, which is the
  part of "does it look right" a test can actually hold.

### Added

- The interface takes its accent from the logo. The mark is a warm gradient and
  the interface already spends amber on warnings and red on errors, so the
  accent had to be the part of that gradient which stays legible *and* stays
  distinguishable from both — measured rather than chosen by eye. On white the
  magenta end gives 6.97:1 where the orange end gives 2.75:1 and fails; on the
  dark surface it is the other way round, and every crimson and pink tried
  either dropped below 4.5:1 or came within a hair of the error red. So light
  takes the magenta end and dark the orange, both from the mark, every role
  between 6.5:1 and 7.1:1.

- The interface works on a phone. There was not a single breakpoint in the
  stylesheet, which on a phone meant a header wrapping into three rows, forms
  in columns two inches wide, and a VNC view the size of a stamp — on the
  device somebody is most likely to be holding while standing in front of the
  screen they are setting up. The tabs scroll sideways and keep the page you
  are on in view, controls are big enough for a thumb, and the screen view
  fills the window rather than a fixed fraction of it.

- A Keyboard button on the Screen page. A phone raises no keyboard for a
  canvas, so the thing most often wanted from that page — signing a dashboard
  back in — could not be done from the device in your hand.

- The monitor's own description of itself, decoded and shown on the Device
  page: maker, model, serial, year, the physical size of the panel, the mode it
  is actually made of, and the density those imply. Almost every hard question
  about one of these screens is answered by it — why a page is scaled, whether
  the mode being driven is the panel's native one, which of four sockets the
  television is on. The kernel offers the raw bytes and decodes none of them.

- The X server's log is parsed rather than dumped, and its timestamps are
  converted against `CLOCK_MONOTONIC` — the clock it actually stamps with.
  Two anchors that look right are both wrong: the wall-clock date the server
  prints once is written in the container's timezone rather than the screen's,
  which put every line four hours out on the first device tried; and
  `/proc/uptime` is `CLOCK_BOOTTIME`, which keeps counting through suspend, so
  on a laptop that has been closed a few times it was out by 1.39 days. Both
  failures look entirely plausible in the output. Checked against the daemon's
  own independent timestamp for the same event: two milliseconds apart. Its timestamps are the
  kernel's monotonic clock — seconds since the machine booted, comparable with
  nothing — and it prints a wall clock exactly once, beside one of them, which
  is enough to convert the rest. Severities come out of the middle of the text
  and become something the page can colour and filter on, and continuation
  lines are joined to the line they continue. The raw file is still one click
  away for a bug report.

- Settings with a known set of answers are dropdowns: the timezone, which is
  searchable and comes from the zones this machine actually has; the socket,
  the mode and the rotation for each output, built from what the monitors
  report; the log level. A timezone typed as "Europe/london" is a clock an hour
  wrong with nothing to say why, and a mode typed by hand that the monitor does
  not have is a black screen.

- The time client can be turned off from the interface, for a machine that
  already runs chrony or systemd-timesyncd. Two time daemons correcting one
  clock against each other is worse than neither.

- The browser tab is named after the device, and follows a rename. Somebody
  looking after several has a tab open on each, and "Cue" on all of them says
  nothing.

- `browser.forceDarkContent`, which darkens pages that ignore
  `prefers-color-scheme`. Plenty of dashboards have a theme of their own, kept
  in an account and defaulting to light, and on a wall in a dark room that page
  is the brightest thing in the room. It leaves photographs and video alone, so
  a camera dashboard keeps its pictures and loses its white chrome.

- Certificates a device trusts. An appliance on a private network that signs
  its own certificate can now be trusted by name, so the page opens with no
  warning and every other page goes on being checked — which is the difference
  between this and `ignoreCertificateErrors`, the answer that stops the browser
  checking anything at all.

- Windows the daemon did not open are closed. A page that calls `window.open`
  gets a window of its own and, with no window manager, it is stacked in front
  of the one on the wall; a screen showing a single page stayed covered by it
  until somebody walked over. A window is given a cycle to close itself first,
  and what was closed is always logged.

- Scrollbars float over the page and fade out, instead of taking a column of a
  dashboard that nobody is going to scroll.

- Network management, off by default. `cue` can hold a static address or join a
  wireless network, on interfaces it is explicitly told to manage, and reconcile
  them every half minute. The Network page in the web interface scans for
  wireless networks and joins one, so a screen carried into a room with no
  keyboard and no network can be put on it.

- Chromium defaults to dark mode. A dashboard on a wall in a dark room at
  full brightness is the thing people complain about first.

- Proved on real hardware: a laptop with its display manager stopped, driving
  its own panel at 2560x1440 through Xorg, showing a UniFi Protect dashboard
  full screen and signed in by the login rule.

- The container image builds for arm64 as well as amd64. Two of the X drivers
  are built only for x86 and are now installed only there; asking for them
  elsewhere failed the build outright, which would have broken the first
  multi-architecture release.

- Proved against a real dashboard: half an hour of continuous operation with
  four live camera streams, no restarts, no watchdog failures, and memory
  flat.

- The first version of everything. One Go daemon, shipped in a distroless
  image, that turns a headless Linux machine with a screen attached into a
  managed display: it starts and supervises an X server, Chromium in kiosk
  mode, a VNC server and a time client, and needs nothing configured on the
  host.
- A playlist of pages, rotating on a timer, each for as long as it says.
- Login rules, re-evaluated every few seconds, which keep a page signed in
  when its session expires and drops the tab back to a login form.
- Dismiss rules, which get rid of the banners and announcements that appear on
  top of a page and stay there.
- A watchdog that asks three questions a frozen display cannot answer — does
  the X server reply, does the page run JavaScript, does it reach its next
  animation frame — and escalates from reloading the page to restarting the
  graphics.
- Display arrangement over RandR, reconciled every few seconds, so that
  unplugging and replugging a monitor brings the picture back on its own.
- A web interface: first-run setup, an overview with a live screenshot and the
  machine's own numbers, a playlist editor, the screen itself over VNC in a
  browser tab, and the device's hardware and logs.
- Optional enrolment with a fleet management service. The device dials out and
  holds one connection open, over which the service reaches the same interface
  an operator would; nothing is opened on the network in front of the screen,
  and none of it is contacted until an operator turns it on.
- The browser keeps its own sandbox, which needs CAP_SYS_ADMIN on the
  container: the sandbox creates namespaces the default seccomp policy refuses
  without it. Granting it leaves seccomp on. The daemon recognises the failure
  that follows from not granting it and says which of two things to change.
- The browser is started in the groups that own the graphics and sound
  devices, read from those devices at run time, so that it can use the
  hardware. Their group numbers come from the host and differ between
  machines, so they cannot be written into the image.
- `display.virtualTerminal` names the console the X server draws on, so that a
  container needs one console device passed through rather than all of them.
- Touchscreens are found and reported, and the browser is told touch exists —
  without which Chromium in a container often renders a desktop layout with
  buttons too small for a finger.
- `cue config`, `cue display probe`, `cue health` and `cue version`.

### Changed

- The interface carries Cue's own mark. The tab icon was a placeholder drawn
  inline in the HTML — a blue rectangle on a blue square — and the header had
  no mark at all. Both now use the product's, a display panel in the brand's
  colours, and it is a file rather than a data URI so the page and the icon
  stop being one thing to edit.

### Removed

- `browser.debuggingPort`. It was a setting twice and caused a different failure
  each time. Fixed at 9222, it was not this browser's port but whichever process
  on the machine got there first, and the daemon drove another container's
  browser — every call succeeding, nothing logged, a frozen screen and a window
  that would not go full screen. Changing the default to 0 fixed new devices and
  did nothing for the one already deployed, whose file still said 9222 and where
  nothing could bind it, so the browser never came up at all. The port is now
  always chosen by the browser and always read back from `DevToolsActivePort` in
  its own profile, which cannot resolve to anybody else's browser. An old
  `debuggingPort:` in a configuration file is ignored.

### Fixed

- The Network page lists only interfaces with hardware behind them. On a
  machine running containers the page was mostly a Docker bridge, one interface
  per running container and whatever a VPN had left behind — all reported as
  "ethernet", each looking exactly like a socket on the back of the machine.
  They are collapsed into a line at the bottom now. An interface that is
  actually configured is still shown, whatever it is.

- `make docker-test` fails when a test inside the image fails. Its output was
  piped through `grep` for readability, and a pipeline exits with the status of
  its last command, so a failing test showed FAIL on the screen and returned
  success — which is what a check for silent failures must never do itself.

- Proved by migration: the machine this project was written to replace now
  runs `cue`. Its supervisor-and-bash stack is gone, along with 22 packages —
  Chromium, the X server, x11vnc, supervisor — because all of it is inside the
  image now. Two faults turned up in the doing and are fixed below.

### Added

- Shrinking a picture keeps its transparency. Every pixel came back opaque,
  which is invisible on a screenshot — a screen has no transparent pixels — and
  put a black box around the mark on the wallpaper, because the logo's
  transparent margin averaged to opaque black and was then drawn over the
  background.

- A wallpaper: the Cue mark on the root window, which is what the screen shows
  in the seconds before the browser has drawn anything and again if it goes
  away. Before it, that was whatever the X server left behind — black on most
  drivers, and on some the grey stipple pattern from 1987, which on a wall in
  front of people is indistinguishable from a machine that failed to boot.

### Fixed

- The mouse pointer appears when somebody moves it and goes away when they
  stop. It was hidden by starting the X server with `-nocursor`, which cannot
  be undone while the server runs — so a screen with a touchscreen or a mouse
  was impossible to aim, because there was no way to see where you were. The
  server keeps its cursor now and the daemon hides it, using an empty cursor on
  the root window; `display.cursor` takes `hidden`, `auto` or `always`, and
  still accepts the `true` and `false` it used to be.

- The daemon no longer dies when two things open an X connection at the same
  moment. `randr.Init` and `dpms.Init` register their extension in
  package-level maps inside the X bindings, and those maps are not guarded: two
  goroutines opening a display together write the same map, and Go stops the
  whole program with "concurrent map writes" — a fatal error, not a panic, so
  no recover catches it. Three things here open connections, and adding a
  fourth that opens on a timer turned a race that had always been there into
  one that took a display down. Opens are serialised now.

- The screenshot is read from the X server instead of from the browser, which
  fixes three things at once. It is now a picture of the screen rather than of
  what the browser believes it drew — a window that was never sized to the
  screen, a page covered by something else, or a renderer that stopped painting
  all looked perfect in the old one. It is available when the browser is the
  problem, which is exactly when somebody wants to see the screen; asking the
  browser then answered "nothing is on the screen yet", while a crashed
  Chromium still leaves its last frame on the glass. And it costs the screen
  nothing: asking Chromium for a *scaled* capture re-laid the page out while it
  took the picture, so the dashboard visibly jumped to another size and back
  every few seconds for as long as anybody had the interface open. The smaller
  picture is now made here, by averaging, which the screen cannot see at all.

- A zoom the browser remembered is cleared at every start. Chromium keeps a
  zoom per host in the profile, for ever. It takes one keystroke to set — ctrl
  and minus, or ctrl and a scroll wheel — and on a screen on a wall nobody is
  standing there to notice or undo it. The profile on the first device held
  `zoom_level: -1.5778829311823859` for one host, which is three quarters, and
  that is what put the dashboard in a corner with black down two sides: the
  window was the right size, the screen was the right size, the mode was right,
  and every flag on the command line said 1. A deliberate zoom belongs in
  `browser.deviceScaleFactor`, where it is written down.

- The page gets the pixels the screen has. The browser was scaling by the DPI
  the X server reported, which comes from the physical size the panel claims:
  on one screen that worked out to 72 DPI, so it chose a scale of 0.75 — the
  window filled the 2560x1440 screen, the page laid itself out at 3412x1918,
  and it was drawn shrunk into a corner with black down two sides. Nothing was
  broken and nothing anywhere said so. `browser.deviceScaleFactor` now defaults
  to 1 and the panel's opinion of itself is not consulted.

- The screen has a keyboard and a mouse again. The container was given
  `/dev/input` but not `/run/udev`, and the X server does not go looking in
  `/dev/input` — it asks udev what exists, and udev answers out of that
  database. It found none, and said so only as an informational line saying it
  relies on udev. Every other sign was healthy: the devices were all there, and
  the daemon's own Device page listed every one of them, because that reads the
  kernel directly. The machine's database is now mounted read only, and the
  daemon says plainly at start-up when it is missing.

- The daemon refuses to start an X server on a display something is already
  answering on. The check it had looked for a lock file, which a container
  cannot see when the server belongs to the machine outside it — and `cue` is
  deployed with the host's network, because managing the machine's network
  needs it. The abstract X socket belongs to the network namespace rather than
  the mount namespace, so in that arrangement the container shares the
  machine's: on a workstation with a desktop running, Chromium reaches for the
  real X server before the container's own. What stopped it there was the
  daemon's private cookie, which is a lock on the door and not a reason to walk
  up to it. Both sockets are now checked, unauthenticated, before anything
  starts.

- The screen size is read from the root window rather than from the X
  connection setup. The setup block is sent once, when the client connects, and
  is never updated — so after this daemon resized the screen it still reported
  the size from before, and the browser was sized to that. On the first machine
  migrated to `cue` the window came up 1152x864 on a 1280x1024 screen, with a
  black band down two sides, while every log line said the right mode had been
  set. `make docker-smoke` now asks for a screen that is deliberately not the
  size the X server would pick on its own, and checks the screenshot comes back
  the size of the screen.

- Deploying to a machine that cannot be reached says why. The first step of a
  deployment failed with "exit status 1" and nothing else, and the reasons —
  no such host, no key, the wrong account, no Docker — are all different things
  to do about them.

- Dark mode reaches the page. Of the three flags passed for it, two did not
  exist in this Chromium — `--force-prefers-color-scheme` and the
  `WebUIDarkMode` feature, both inherited from setups running an older one.
  Chromium ignores a switch it does not know without a word, so the command
  line said dark, every setting said dark, and the screen was white. Only
  `--force-dark-mode` is passed now, checked against the binary, and
  `make docker-smoke` measures the average brightness of a real screenshot,
  because nothing short of looking at the pixels catches this.

- A program's command line is rebuilt before every start. It was captured once,
  when the process was first created, so a change that asks for a restart —
  dark mode, the sandbox, certificate errors, extra arguments, the VNC listen
  address, the screen size — restarted the program into exactly the command
  line it already had. The screen blanked, every log line said the change had
  been applied, and the setting was not in force.

- The "On the screen" card no longer blinks. The page was rebuilt from scratch
  every three seconds, and with it the `<img>`: a fresh one holds nothing until
  its picture arrives, so the card emptied and refilled twenty times a minute.
  The element is now kept and the next picture is decoded before it is swapped
  in. Measured in a real browser: four new elements per ten seconds before, and
  none after.

- That picture was also the full screen, losslessly. On a 2560x1440 device it
  was 5.6 MB, fetched every three seconds — 110 MB a minute to leave a browser
  tab open on, and the card was empty for as long as each one took. The
  interface now asks for a smaller JPEG; the full-size PNG is still there for
  anyone who wants it.

- A setting the running version does not have no longer stops the daemon. Every
  device in service has the settings of the version that wrote its file, so a
  setting removed by an upgrade was in every one of those files: the daemon
  refused to start, and the upgrade was a screen that had gone black on a
  machine nobody could reach. Unknown names are now named in the log, shown in
  the interface — a mistyped key and a setting that does nothing look identical
  from in front of the screen — and dropped when the file is next written.
  Anything else wrong with the file is still refused.

- Errors from the configuration file say what was wrong with it. `go-yaml`
  puts every problem into one error whose text is a heading followed by
  indented lines, and printing it with `%w` gave the heading alone: a device
  that would not start logged `yaml: unmarshal errors:` over and over and said
  nothing whatever about its configuration.

- `make docker-test` runs the tests inside the image. Some of them need a
  program the image has and a build machine does not — `certutil`, which is
  how a pasted certificate actually comes to be trusted — and on a build
  machine those tests skip. A skip proves nothing: the point of them is that
  the image has what the code needs. CI runs it.

- Asking for the browser before it has started is an error rather than a
  panic. Everything that drives the browser runs on a timer and can arrive
  before it is up, or after one has gone; the panic was recovered by whichever
  goroutine caught it, which is a rotation loop that quietly stops rotating.

- Name servers are written even where the file cannot be replaced. In a
  container `/etc/resolv.conf` is bind-mounted, so its inode cannot be
  swapped — the atomic write failed and `cue` gave up on setting DNS at all,
  which for a device that then cannot resolve its own dashboard is a black
  screen. It now writes through to the same inode, as every DHCP client does.
  The check that decided this was also wrong: `os.IsPermission` does not
  unwrap, so the fallback would never have run.

- The wireless passphrase can no longer reach the log. The command that carries
  it is also the one most likely to fail, and its whole text went into the error
  — into `docker logs`, into the interface's log view and, on an enrolled
  device, to the fleet service.

- The X server's endless output is recognised and counted rather than repeated.
  With no D-Bus system bus to connect to — and there is none, because nothing
  in this image needs one — it reports the failure as an error and retries every
  ten seconds forever. On a device that had been up an hour, that one line was
  61% of everything in the log. The first occurrence is now logged whole,
  together with why it does not matter, and the rest are counted.

- The container's log is bounded. Docker keeps everything by default and a
  screen is a machine nobody logs in to for a year: on the device this project
  replaces, the browser's log turned over 50 MB in a day and, at its worst,
  10 MB every four minutes.

- Chromium is given every feature it needs in one `--enable-features`. Chromium
  keeps only the last one on a command line, so a second — including one an
  operator added by hand — silently discarded the first.

- Keyboard input over VNC goes through the XKEYBOARD extension. Somebody
  connecting to a screen is usually doing it to type a password into a
  dashboard that logged itself out, from a keyboard laid out differently to the
  one the X server assumes; the shifted characters arrived as something else
  and all they saw was that it was refused.

- The browser's debugging port is now chosen by the browser rather than fixed
  at 9222, and read back out of `DevToolsActivePort`. On a host already running
  another Chromium with 9222 published, `cue` connected to *that* browser and
  drove it instead of its own: the screen appeared frozen, screenshots came
  from the wrong machine and the window would not go full screen. Nothing in
  the logs said anything was wrong, because from the daemon's side everything
  it asked for succeeded.
