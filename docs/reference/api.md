# The HTTP interface

Everything the web interface does, it does through this. It is also how a
monitoring system or a script reaches a device.

The interface listens on `web.listen`, which is `:8080` by default.

## Signing in

Authentication is a session cookie. There is one account and its password is
set by the first-run wizard.

    curl -c cookies -X POST http://device:8080/api/v1/session \
      -H 'Content-Type: application/json' -d '{"password":"..."}'

Then pass `-b cookies` on everything else. A session lasts
`web.sessionLifetime`, thirty days by default.

## Without a session

    GET  /healthz            200 when the browser is running, 503 when it is not
    GET  /welcome            the page the device shows on its own screen
    GET  /api/v1/setup       whether this device has been set up yet
    POST /api/v1/setup       set the name and password. Works exactly once
    POST /api/v1/session     sign in

`/healthz` is deliberately generous: a display whose browser is restarting is
having a bad minute, not a bad day, and a health check that failed during a
planned restart would have an orchestrator kill the container in the middle of
recovering.

## With a session

    GET    /api/v1/status            everything the overview page shows
    GET    /api/v1/configuration     the whole configuration, secrets redacted
    PUT    /api/v1/configuration     replace it
    GET    /api/v1/screenshot.png    a picture of the screen, this moment
    POST   /api/v1/show/{item}       put one playlist item on the screen now
    POST   /api/v1/navigate          show an address once, without editing the playlist
    POST   /api/v1/restart/{program} chromium | display | vnc | time
    GET    /api/v1/logs/xorg         the end of the X server's own log
    GET    /api/v1/vnc               the WebSocket the VNC viewer connects to
    GET    /api/v1/upgrade           whether a newer release exists, and its notes
    POST   /api/v1/upgrade           take it, on a device set up to allow that
    DELETE /api/v1/session           sign out

### Status

One response holds the lot, because a page making eight requests every three
seconds is eight times as likely to show a mixture of two moments. It contains:

- `device` — name, identifier, version, uptime
- `programs` — one entry per supervised program: state, process id, restarts,
  last error
- `browser` — whether it is ready, what is on the screen, every open tab, and
  how many times each has been signed in or had something dismissed
- `watchdog` — consecutive failures, total failures, what was last tried
- `machine` — processor, memory, disks, temperatures, load, uptime
- `connectors` — the machine's display sockets and what is plugged in
- `outputs` and `screen` — what the X server is actually driving
- `clock` — whether the time is synchronised and how far out it is
- `sound` — the machine's sound cards

### Upgrading

`GET /api/v1/upgrade` answers with the version this device is running, the
newest published release, that release's notes as Markdown, and whether this
device can install it:

    {
      "running": "0.1.0",
      "latest": "0.2.0",
      "notes": "### Fixed\n\n- ...",
      "publishedAt": "2026-08-28T00:35:50Z",
      "url": "https://github.com/ziyan/cue/releases/tag/v0.2.0",
      "newer": true,
      "checkedAt": "2026-08-28T01:37:51Z",
      "canApply": false,
      "whyNot": "upgrade.allowApply is not set in cue.yaml",
      "image": "ghcr.io/ziyan/cue:0.2.0"
    }

`trouble` appears instead of a fresh `checkedAt` when the last check failed —
a device with no route out says so and goes on reporting the release it last
heard about, rather than reporting nothing, which reads as being up to date.

`POST /api/v1/upgrade` answers `202` and replaces this container with one built
from `image`. It refuses with `403` unless both `upgrade.allowApply` is set and
`/var/run/docker.sock` is mounted — see `docs/reference/configuration.md` for
why that is two deliberate acts — and with `409` when there is nothing newer or
an upgrade is already under way.

The screen shows what is happening, goes blank for about a minute, and comes
back. If the new version does not answer, the device puts the old one back and
starts it again.

### Secrets

The configuration comes back with every password replaced by `********`.
Sending it back unchanged keeps the real values: the daemon matches playlist
items by identifier and restores the ones that are still the placeholder. This
is what stops opening the settings page and saving it from erasing every
credential on the device.

### The VNC WebSocket

`/api/v1/vnc` is a binary WebSocket carrying the RFB protocol, bridged to the
VNC server on the loopback address. noVNC connects to it directly. The origin
is checked against the request's own host and `web.trustedOrigins`, so a page
elsewhere cannot use a browser's session cookie to watch the screen.
