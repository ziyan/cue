# Project structure

What each package is for and which way the dependencies point. For how a page
gets onto the screen, see `AGENTS.md`.

## Layout

    Dockerfile              the image
    main.go                 command line entry point
    cmd/                    subcommand implementations
    internal/               all real code
    deploy/                 compose files, deployment bits
    tools/                  build-time programs, run by make and by CI
    docs/                   decisions, reference, plans
    vendor/                 vendored dependencies, committed

## Packages

**`internal/config`** — `cue.yaml`. Typed structs, a validator that reports
every problem at once with the YAML path of each, and a `Store` that hands out
immutable snapshots and rewrites the file atomically. Everything an operator
can set lives here. Depends on nothing but utilities, so any package may import
it.

**`internal/daemon`** — the composition root, and the only package that knows
about more than one of the others. It builds everything from one configuration,
starts it in the order it depends on itself, keeps the display arranged, and
stops it in reverse.

**`internal/supervise`** — starting, watching and stopping child processes.
Four things: a process group per child so stopping one stops its tree, output
read line by line into the log, a restart backoff that grows and resets, and a
stop that asks before it insists.

**`internal/xserver`** — Xorg and Xvfb: the command lines, the authority
cookie, the stale lock file a server killed by a power cut leaves behind, and
a readiness check that connects as a client rather than merely looking for a
process.

**`internal/display`** — the RandR client. Enumerates outputs, plans a layout,
compares it with what the server is already doing, and applies only the
difference — which is what makes it cheap enough to run every few seconds.

**`internal/browser`** — Chromium's lifecycle and everything driven over the
DevTools protocol:

    browser.go     the process, the profile, the crash flag
    arguments.go   the command line, with a comment per flag saying which
                   failure it is answering
    binary.go      finding the real executable behind Debian's wrapper script
    playlist.go    one tab per item, and the rotation
    rules.go       signing pages in, and getting rid of what covers them
    health.go      the probes the watchdog asks, and the remedies it applies
    session.go     the cached protocol connections
    state.go       what the interface is told

**`internal/watchdog`** — deciding the display has stopped working, and the
ladder of things to try about it. Knows nothing about browsers: it is given
probes and remedies.

**`internal/vncserver`**, **`internal/timesync`** — x11vnc's and chronyd's
lifecycles, and in the second case the clock's state.

**`internal/audio`** — the machine's sound cards, and which one the browser
opens. No sound server; see the package comment.

**`internal/hardware`** — the machine's own numbers, read straight from `/proc`
and `/sys`.

**`internal/web`** — the HTTP server, the session, the API, the VNC bridge, and
the interface itself under `static/`. It reaches the rest of the daemon through
a `Device` interface, so the dependency points one way.

**`internal/minishell`** — a `/bin/sh` that runs one simple command and refuses
every shell feature. See `docs/decisions/20260822-the-binary-answers-to-sh.md`.

**`internal/util`** — small, independently testable pieces, each free of
project-specific assumptions:

    cdp          the Chrome DevTools Protocol client
    xauth        writing the X authority file
    drm          the kernel's view of the display connectors, and EDID
    atomicfile   write-to-temp-then-rename
    security     identifiers, password hashing, constant-time comparison
    reaper       collecting orphaned children, which process 1 must do
    deferutil    the recover every goroutine defers

## Dependency direction

`cmd` depends on `daemon`. `daemon` depends on everything else. `web` depends
on the packages whose state it reports but not on `daemon`. Nothing in `util`
imports anything outside `util`: those packages are meant to be liftable into a
separate library.
