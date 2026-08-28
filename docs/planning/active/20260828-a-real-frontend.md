# A built frontend: webpack, TypeScript, React, MUI

## Purpose

After this work the management interface is a real frontend project — webpack,
TypeScript, React and MUI in `web/` — built to a bundle that is embedded in the
Go binary, so the daemon still ships as one static executable inside a
distroless image and still has no runtime dependency on Node.

What that buys: components that already work rather than controls drawn by
hand. The interface has spent this week acquiring a switch drawn to Material's
proportions, a menu that hangs off a button, a chevron for a select, a list
with signal bars — every one of them a thing MUI ships, tested, with keyboard
handling and accessibility done.

How to see it working:

    make web && make docker-smoke

and the interface at `http://<device>:8080` is served from the bundle.

## What is there now, and why this is a change of direction

`internal/web/static/` is hand-written ES modules with no build step. The
stylesheet opens with "One stylesheet, no framework. The interface is four
pages of forms and numbers, and a framework would be larger than all of it."

That was a decision made in the code and not one handed down. It was reasonable
when the interface was four pages of forms; it is eleven pages now, and the
week's work has been reimplementing component-library behaviour by hand and
finding the bugs that come with that — a switch invisible in the dark because
the thumb took the surface colour, a top bar 20 pixels tall because two things
were called `.bar`, a select whose only styling was the platform's.

## Definitions

**The bundle.** What webpack emits: one JavaScript file, one stylesheet, an
`index.html`, and any assets, written to `internal/web/dist`. Go's `embed`
cannot reach outside the package directory, so the build writes there rather
than into `web/`.

**Embedded.** Compiled into the binary by `go:embed`, exactly as
`internal/web/static` is today. Nothing about how the daemon is shipped
changes: still one executable, still no Node on the device.

## Milestones

### 1. The toolchain exists and produces a bundle

`web/` with `package.json`, `tsconfig.json`, `webpack.config.js` and a `src`
that renders something. `make web` builds it into `internal/web/dist`.

Verify:

    make web && ls -la internal/web/dist

Expect an `index.html` and a fingerprinted bundle.

### 2. The daemon serves it

`internal/web` embeds `dist` and serves it, with the old static tree still
present but no longer routed. A page that does not exist falls through to
`index.html`, because the router is in the browser.

Verify: `curl -sS localhost:8080/ | grep -o 'bundle[^"]*js'`

### 3. The shell, in React and MUI

Sidebar, top bar, breadcrumb, theme switcher, language switcher, user menu —
the shape that is already agreed, built from MUI components rather than drawn.

### 4. The pages

Overview, Content, Screen, Network, and the six settings pages, ported one at
a time. The old module for a page is deleted only when its replacement works.

### 5. The image builds it

A Node stage in the Dockerfile, and CI running the frontend's own checks. The
device never sees Node; the builder does.

## Progress

- [x] 1. The toolchain exists and produces a bundle — 2026-08-28. `web/` with
  Vite, React 19, MUI 7, TypeScript. `make web` writes `internal/web/dist`;
  508 kB, 161 kB gzipped.
- [x] 2. The daemon serves it — 2026-08-28, at `/next` while the pages are
  moved across, so the interface people are using goes on working at `/`.
  Anything the bundle does not have is answered with `index.html`, because the
  routing is in the browser.
- [x] 3. The shell, in React and MUI — 2026-08-28. Sidebar with the same three
  groups, permanent above `md` and a drawer below it; top bar with breadcrumb,
  language, appearance and account; the appearance in three states; the sign-in
  gate. Running on carbon.
- [ ] 4. The pages
- [ ] 5. The image builds it

## Decision log

**2026-08-28 — Vite, with MUI.** Two sibling projects and they disagree:
`~/mujin/dev/portal` is webpack with MUI, `../cue.sh` is Vite with Tailwind.
Vite from cue.sh, because it is the simpler build and the closer sibling; MUI
because that is what this interface has spent the week reimplementing by hand.

**2026-08-28 — The bundle is committed to the image, not to git.** `dist` is
built in CI and in the Docker build and is not checked in. The Go build alone
therefore cannot produce a working binary from a clean checkout without
running the frontend build first, which `make` handles and which the Dockerfile
does in its own stage.

## Surprises and discoveries

**2026-08-28 — The no-framework rule was self-imposed.** It reads in the source
as though it were a requirement. It was a choice made once and then quoted back
as a constraint, including by me, in this session — as a reason not to do this
work. Worth writing down: a comment explaining a decision is not the same as a
decision that is still right.

## Outcomes and retrospective

To be written at each milestone.
