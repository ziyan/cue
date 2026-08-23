# One Go process supervises everything, in a distroless image

- Status: accepted
- Date: 2026-08-22
- Deciders: Ziyan Zhou

## Context

The system this replaces was a set of shell scripts on the host — one to start
the X server, one for the browser, one for the VNC server, an installer to
generate a supervisor configuration from a template, and an `/etc/default` file
holding the settings. Putting up a new screen meant installing seven Debian
packages, creating a user, running the installer, and editing two files. Every
machine drifted, and the interesting failures were all in the seams: the
browser starting before the display had been configured, the supervisor
restarting a script whose environment had changed, a stale X lock file after a
power cut that nothing knew to clear.

## Decision

Everything is one Go binary, running as process 1 in a container, supervising
four child programs: the X server, Chromium, x11vnc and chronyd. Nothing is
installed on the host and nothing is configured there. The image is built on
`gcr.io/distroless/base-debian13` and contains no shell, no package manager and
no init system.

The daemon does what the scripts did, in one process where it can be ordered
and tested: it waits for the X server to *answer* rather than merely to have
been executed, it arranges the display before the browser starts so the window
is the right size, it clears a stale lock file itself, and it restarts a child
with a backoff that resets once the child has stayed up.

## Consequences

- A new screen is one `docker run`. Nothing on the host has to be right except
  Docker and the absence of a display manager.
- Container privileges become the interesting part. The X server needs the
  graphics device, a virtual console and the ability to switch it; chronyd
  needs permission to set the clock. `deploy/docker-compose.yml` lists the
  smallest set that works and says what to do when it does not.
- The image is about a gigabyte, most of it Chromium and Mesa. That is the
  price of the browser, not of this decision.
- Being process 1 brings a duty: orphaned children have to be reaped, or the
  process table fills over a few weeks and the screen freezes for no visible
  reason. `internal/util/reaper` does it.
- One thing genuinely needs a shell: the X server compiles its keyboard map by
  running xkbcomp through `popen`, which is `/bin/sh -c`. See
  `20260822-the-binary-answers-to-sh.md`.
