# The web interface is plain modules with no build step

- Status: accepted
- Date: 2026-08-22
- Deciders: Ziyan Zhou

## Context

The author's other projects build their front ends with npm, TypeScript and
webpack, and compile the output into the Go binary. That is the right shape for
an application with a large interface and several people working on it.

This interface is four pages of forms and numbers, plus a VNC viewer. The VNC
viewer is noVNC, which ships as ES modules that a browser can import directly.

## Decision

No JavaScript toolchain. `internal/web/static/` holds hand-written ES modules
and one stylesheet, embedded with `go:embed`, and noVNC is vendored as the
modules it already publishes. Building this project is `go build`.

Nothing builds HTML by concatenating strings: `dom.js` has a small `h()` that
creates elements, so a device name or a page title containing markup is text
rather than markup.

## Consequences

- Building needs Go and Docker and nothing else. There is no `npm ci` that can
  fail on a machine nobody has looked at in a year, no lock file to audit, and
  no transitive dependency tree under an interface this size.
- No TypeScript, so the interface has no type checking. Accepted for four pages
  with one consumer; it would not be accepted for forty.
- No bundling and no minification. The interface is a dozen files fetched over
  a local network from the machine in the room, which is not a page-load
  budget worth optimising.
- Updating noVNC is a manual copy, recorded in
  `internal/web/static/novnc/README.md`. That is a real cost and the reason to
  reverse this decision would be needing to track a dependency that moves.
