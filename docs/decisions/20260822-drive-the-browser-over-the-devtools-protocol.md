# The daemon drives the browser over the DevTools protocol, not with an extension

- Status: accepted
- Date: 2026-08-22
- Deciders: Ziyan Zhou

## Context

The system this replaces rotated tabs with a Manifest V3 browser extension.
Its configuration was a JSON file the supervisor generated next to it at every
start, which meant the shell script and the JavaScript each held half the
policy and could disagree. Changing the list of pages meant regenerating the
file and restarting the browser. The extension could only see what a page
allowed it to see, and its service worker had to be evicted by deleting a
cache directory before code changes took effect.

## Decision

The daemon speaks the Chrome DevTools Protocol — the same one the browser's own
developer tools use, exposed on a loopback port by
`--remote-debugging-port`. `internal/util/cdp` is a small client for it:
the HTTP endpoints for listing tabs, and a WebSocket per tab for navigating,
evaluating JavaScript, capturing screenshots and clearing the cache.

Everything the extension did, and several things it could not, now live in Go:
which page is shown, when a page is reloaded, how a page is signed in, what is
dismissed from on top of it, whether the renderer is still painting, and what
the screen looks like this second.

## Consequences

- The playlist can change while the browser keeps running, so editing it does
  not blank the screen for several seconds.
- Screenshots come free, which turns out to be the single most useful thing in
  the web interface: it answers "what is it showing" without a VNC connection.
- The health probes can ask the page questions a heartbeat could not — in
  particular whether it reaches its next animation frame, which is the only
  way to catch a renderer that answers JavaScript but has stopped painting.
- The debugging port is an unauthenticated interface to the browser. It is
  bound to the loopback address, and the container's network is the machine's,
  so anything already on that machine can reach it. That is accepted: anything
  on that machine can also read the configuration file.
- Chromium's protocol is not versioned in a way that promises stability. The
  five commands used here — `Page.navigate`, `Page.reload`,
  `Page.captureScreenshot`, `Runtime.evaluate`, `Network.clearBrowserCache` —
  are the oldest and most used in it, so the risk is small, and `make
  docker-smoke` would catch a break.
