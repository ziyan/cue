# Configuration

Everything an operator can set lives in one file, `/etc/cue/cue.yaml`. It is
written both by hand and by the web interface, and it is the only place any of
this is configured — there is no second store and no command line flag that
sets anything not here.

After editing it by hand, tell the daemon:

    docker kill --signal HUP cue

A file that no longer validates is refused and the configuration already in
force is kept, so a mistyped duration over a slow connection does not turn the
screen off.

The file holds the passwords the screen signs in with. It is written with mode
0600.

## device

    device:
      name: Reception          # shown in the interface and on the screen's own page
      identifier: 8n4q1v...    # generated once, never changes. The fleet keys on it
      location: Ground floor   # free text
      timezone: Europe/London  # empty means UTC

## log

    log:
      level: INFO              # DEBUG, INFO, NOTICE, WARNING, ERROR, CRITICAL
      browserOutput: false     # raise the browser's own output from DEBUG to INFO

The browser's output is always captured — it is the only place the reason it
would not start ever appears — but logged at DEBUG so it stays out of the way.

## paths

    paths:
      state: /var/lib/cue      # survives a restart: browser profile, credentials
      runtime: /run/cue        # does not: X socket, authority cookie, disk cache

## display

    display:
      server: xorg             # or xvfb, which draws into memory
      number: 0                # the X display number
      virtualTerminal: 2       # the console to draw on: 2 means /dev/tty2
      cursor: false            # a kiosk with a pointer parked in it looks broken
      framebuffer: ""          # force a size, e.g. 1920x1080, for a television that lies
      modeline: ""             # add a mode the monitor did not offer
      modeName: cue            # what to call that mode
      blankAfter: 0s           # 0 never blanks
      reconcileInterval: 5s    # how often the layout is compared with reality
      xorgConfiguration: ""    # written verbatim into the X server's config directory
      extraArguments: []
      outputs:
        - name: "*"            # a socket name like HDMI-1, or * for anything unnamed
          mode: preferred      # preferred, off, or 1920x1080
          rate: 0              # hertz, when several modes share a size
          position: 0x0        # where it sits; every output at 0x0 is mirrored
          rotate: normal       # normal, left, right, inverted
          primary: false

The default puts every output at `0x0`, which means every screen shows the same
top-left corner of the drawing surface — they are mirrored. That is what a
display appliance almost always wants, and it is what a laptop with an external
screen needs: the alternative lays the two out side by side, giving a surface
twice as wide and one browser window spanning both.

Leave `position` out entirely to get that side-by-side layout, or give each
output an explicit position for a video wall:

    outputs:
      - name: HDMI-1
        position: 0x0
      - name: HDMI-2
        position: 1920x0

`virtualTerminal` is worth a word, because getting it wrong produces a black
screen and a message that names neither the setting nor the container. Left to
itself, the X server asks the kernel for a free console, is told a number, and
opens `/dev/tty<number>` — which inside a container is only there if that exact
device was passed through. So the console is named here instead, and the
container passes that one device. The two have to agree; `deploy/docker-compose.yml`
sets both. Zero lets the server choose, which needs the whole of `/dev`.

`cue display probe` lists the sockets this machine has and what is plugged into
them, with or without an X server running.

The reconcile interval is what makes replugging a cable work: every few seconds
the daemon compares the kernel's view of the connectors with what it last
applied, and reapplies the layout when they differ.

## browser

    browser:
      binary: chromium         # a wrapper script is detected and stepped over
      user: cue                # the account the browser runs as. Must not be root
      sandbox: true            # turning it off is a real reduction in safety
      ignoreCertificateErrors: false
      ephemeralCache: true     # empty the disk cache at every start
      debuggingPort: 9222      # loopback only
      extraArguments: []

`sandbox: true` needs the browser to run as a non-root account, which is what
`user` is for. Chromium refuses to enable its own sandbox as root, and this
browser renders whatever the network serves it.

`ephemeralCache` exists because a corrupted cache produces a page that will not
load while everything else looks healthy, and it survives every restart — so it
presents as a device that has to be reimaged. Making the cache disposable
removes the whole class of fault.

## playlist

    playlist:
      interval: 30s            # 0 shows the first page and never moves on
      items:
        - identifier: 3k9...   # generated once
          url: https://example.com/
          title: Sales         # optional; the page's own title is used otherwise
          duration: 60s        # overrides the interval for this item
          reload: true         # fetch it again each time it comes round
          disabled: false      # keep it configured but out of the rotation
          login: …             # see below
          dismiss: …           # see below

### login

Re-evaluated every few seconds, not performed once when the tab opens. That is
the point: it exists for dashboards whose session expires and which then
redirect the tab back to a login form.

    login:
      whenUrlMatches: "/login"              # a regular expression against the address
      whenSelectorExists: ""                # or a selector only the login page has
      usernameSelector: "input[name=user]"  # may be empty for a password-only form
      passwordSelector: "input[name=pass]"
      submitSelector: "button[type=submit]" # empty presses Enter instead
      username: display
      password: "..."
      expectUrlMatches: "/dashboard"        # how to tell it worked
      minimumInterval: 60s                  # never attempt more often than this

`minimumInterval` matters: a wrong password submitted in a loop locks the
account out, which is much worse than a screen showing a login page.

Set `expectUrlMatches` if you can. Without it the daemon only knows that it
typed something in; with it, a rule that fires but does not work says so.

### dismiss

Anything that appears on top of the page and stays there.

    dismiss:
      - selector: "button"
        whenTextMatches: "Got it|Dismiss"   # optional, to aim a broad selector
        hide: false                         # click it, or give it display:none

`hide` is for things that cannot be closed. It is blunter, and because it does
not tell the page the notice was seen, the notice usually comes back on the
next load.

## watchdog

    watchdog:
      enabled: true
      interval: 15s
      timeout: 10s             # must be shorter than the interval
      failuresBeforeReload: 2
      failuresBeforeRecreate: 4
      failuresBeforeClearCache: 6
      failuresBeforeRestart: 8
      failuresBeforeRestartDisplay: 16

Each threshold is a number of consecutive failed probes and each must be larger
than the one before it. See
`docs/decisions/` and `internal/watchdog` for what the probes are.

## vnc

    vnc:
      enabled: true
      listen: 127.0.0.1:5900   # the loopback address keeps it behind the interface
      password: ""             # only needed if you move it off the loopback address
      viewOnly: false

The default keeps the VNC server on the loopback address; the web interface
bridges it over an authenticated WebSocket, so watching the screen needs the
same password as changing what is on it. Moving it onto the network without a
password puts an unauthenticated view *and control* of the screen on the LAN,
and the daemon says so at every start.

## web

    web:
      listen: :8080
      passwordHash: ""         # argon2id, set by the first-run wizard
      sessionSecret: ""        # generated once
      sessionLifetime: 30d
      trustedOrigins: []       # extra origins allowed to open the VNC WebSocket

## audio

    audio:
      enabled: true
      sink: ""                 # an ALSA device like plughw:HDMI. Empty lets ALSA choose
      source: ""
      volume: 70

`cue`'s Device page lists the sound cards the machine has and the names to use.

## time

    time:
      enabled: true
      servers: [pool.ntp.org]

Correcting the clock needs `CAP_SYS_TIME`. Without it chronyd runs, finds a
server, works out that the clock is wrong, and cannot do anything about it —
which produces a device showing certificate errors with a healthy-looking time
client on it.

## fleet

    fleet:
      enabled: false
      url: https://cue.sh
      enrollmentToken: ""

Off by default, and inert until turned on: the daemon makes no outbound
connection of its own accord.

## Touchscreens

There is nothing to configure. The daemon reads the kernel's input listing at
start-up, and if the machine has a device a finger is put directly on — a
touchscreen rather than a touchpad, which the kernel distinguishes with a
property bit — it tells Chromium that touch is available. Without that,
Chromium inside a container often decides there is no touch and a dashboard
renders its desktop layout, with buttons too small for a finger.

The Device page lists what the kernel can see, which is the first thing worth
knowing when a touchscreen "does not work" and the hardest to find out on a
machine with no shell.

**What is not done yet.** On a machine with more than one screen, or with a
rotated one, a touch device reports coordinates across the whole drawing
surface, so a touch lands in the wrong place. Correcting it means setting each
device's coordinate transformation matrix from the geometry of the output it
belongs to — what `xinput map-to-output` does — and that needs the XInput2
extension, which the X library this project uses does not implement. A single
screen in its normal orientation, which is nearly every display, is unaffected.
