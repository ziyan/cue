# Working in this repository

Entry point for anyone — human or coding agent — making a change here. Read
`CONTRIBUTING.md` for the conventions; this file is about orientation.

## What this program is

One Go binary that runs as process 1 inside a container and turns a headless
Linux machine with a screen attached into a managed display. It starts and
watches an X server, Chromium in kiosk mode, a VNC server and a time client;
it arranges the screen; it decides what is shown and keeps those pages usable
without anybody standing in front of them; and it serves a web interface for
configuring and watching all of it.

It is **not** a general-purpose digital signage platform. There is no schedule,
no media library and no template system. It shows web pages, in order, and
tries very hard to keep doing so unattended.

## Where things are

    Dockerfile              the image: builds the daemon, then assembles a
                            root filesystem onto distroless
    main.go                 command line entry point, and the /bin/sh disguise
    cmd/                    one file per subcommand: run, config, display,
                            health, version
    internal/
      config/               cue.yaml: types, validation, atomic writes. The
                            source of truth for everything an operator sets
      daemon/               the composition root. Start here to see how the
                            pieces fit together
      supervise/            starting, watching and stopping child processes
      xserver/              Xorg and Xvfb lifecycle, the authority cookie
      display/             the RandR client: outputs, modes, framebuffer, DPMS
      browser/              Chromium's lifecycle and everything driven over the
                            DevTools protocol: the playlist, the login rules,
                            the dismiss rules, screenshots, the health probes
      watchdog/             deciding the display has frozen, and the ladder of
                            things to try about it
      vncserver/            x11vnc's lifecycle
      timesync/             chronyd's lifecycle and the clock's state
      audio/                sound cards, and which one the browser opens
      hardware/             the machine's own numbers, read from /proc and /sys
      web/                  the HTTP server, the API, the VNC bridge, and the
                            interface itself under static/
      minishell/            a one-command /bin/sh, and the long comment saying
                            why that is not as bad as it sounds
      util/                 small independently testable pieces: cdp, xauth,
                            drm, atomicfile, security, reaper, deferutil
    deploy/                 compose files and deployment bits
    tools/                  build-time programs: checksecrets, smoke, release,
                            changelog
    docs/                   see docs/decisions/ for why things are as they are

## How a page gets onto the screen

1. `cmd.runDaemon` opens `internal/config`, writing the defaults if there is
   no file, and hands the store to `internal/daemon`.
2. `daemon.Run` starts the web interface first — so that a device whose X
   server will not start is still reachable to say so — then the time client,
   then the X server.
3. When the X server answers, `daemon.applyLayout` connects as an ordinary X
   client and arranges the outputs over RandR, then tells the browser how
   large its window should be.
4. `internal/browser` starts Chromium as an unprivileged account, waits for its
   debugging port, and opens one tab per playlist item.
5. Two loops then run forever: one rotates the tabs, and one re-evaluates each
   item's login and dismiss rules every few seconds.
6. `internal/watchdog` asks three questions on a timer and escalates when they
   are not answered.

Reading `daemon/daemon.go`, `browser/playlist.go`, `browser/rules.go` and
`watchdog/watchdog.go` in that order explains most of the system.

## Ground rules

The invariants in `CONTRIBUTING.md` are the ones that bite. The short version:

- Never commit a credential, a private address, or anything from a real
  deployment. `make check-secrets` fails the build if you do.
- Everything an operator can set lives in `internal/config` and nowhere else.
- Nothing in this project is a shell script.
- A supervised program is stopped by signalling its process group, not its
  process.
- The watchdog is suspended around every deliberate restart.
- The web interface never sends a secret out, and `RestoreSecrets` is what
  stops saving a form from erasing one.

## Current state

The plan this was built from, including what is deliberately left for later,
is `docs/planning/active/20260822-cue-kiosk-daemon.md`. Read it before starting
anything substantial: it says what is done, what is not, and why several things
are the way they are.
