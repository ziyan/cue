# Changelog

All notable changes to this project are recorded here, in the categories of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). This project follows
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
