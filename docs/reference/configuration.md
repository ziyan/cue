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
      identifier: 8n4q1v...    # generated once, never changes
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
      wallpaper: true         # the Cue mark, until the browser has drawn
      cursor: auto            # hidden, auto, or always
      cursorIdleTimeout: 3s   # how long it stays up after it stops moving
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

`wallpaper` paints the Cue mark on the root window. It is what the screen shows
in the seconds before the browser has drawn anything, and again if the browser
goes away. Turning it off leaves whatever the X server does — black on most
drivers, and on some the grey stipple pattern from 1987, which on a wall in
front of people is indistinguishable from a machine that failed to boot.

`cursor` is what the mouse pointer does.

`auto`, the default, shows it while somebody is moving it and hides it again a
few seconds after they stop (`cursorIdleTimeout`). That is what a screen wants:
a pointer parked in the middle of a dashboard is the sort of thing people
photograph and send to you, and a screen with a touchscreen or a mouse is
impossible to aim if the pointer can never appear at all.

`hidden` starts the X server with no cursor whatsoever. It cannot be undone
while the server runs, which is why it is not the default. `always` leaves the
pointer up like any other machine.

`true` and `false` are still accepted and mean `always` and `hidden`: that is
what this setting used to be, and it is written into the file of every device
already in service.

## browser

    browser:
      binary: chromium         # a wrapper script is detected and stepped over
      user: cue                # the account the browser runs as. Must not be root
      sandbox: true            # turning it off is a real reduction in safety
      ignoreCertificateErrors: false
      darkMode: true           # a wall in a dark room at full brightness
      forceDarkContent: false  # darken pages that ignore the above
      deviceScaleFactor: 1     # the page gets the pixels the screen has
      ephemeralCache: true     # empty the disk cache at every start
      closeUnexpectedTabs: true
      extraArguments: []

There is deliberately **no setting for the DevTools port**. The daemon drives
the browser over it, always asks for port 0, and always finds the number the
browser chose in `DevToolsActivePort` inside its own profile directory — which
cannot resolve to anybody else's browser. To attach devtools by hand, read that
file.

It was a setting twice, and caused a different failure each time. Fixed at
9222, it was not this browser's port but whichever process on the machine got
there first: on the laptop this was developed against, another container
publishes 9222, and the daemon spent an afternoon driving *that* browser. Every
call succeeded, so nothing was logged as wrong; what was visible was a frozen
screen, a certificate error for a page it had never been asked to load, and a
window that would not go full screen. Changing the default to 0 then fixed new
devices and did nothing for the one already deployed, because 9222 had been
written into its configuration file and went on overriding the new default —
and by then nothing could bind it, so the browser never came up at all.

An old `debuggingPort:` left in a configuration file is ignored, and removed the
next time the file is written.

`deviceScaleFactor` is how many device pixels the browser draws for each pixel
a page asks for. Left to itself it works this out from the DPI the X server
reports, which comes from the physical size the panel claims over EDID — a
genuinely high number on a laptop panel, and often nonsense on a television.
One screen reported a size working out to 72 DPI, so the browser chose 0.75:
the window filled the screen, the page laid itself out at 3412x1918, and it was
drawn shrunk into a corner with black down two sides. Nothing was broken and
nothing said anything.

A screen on a wall has a fixed number of pixels and a dashboard designed for
pixels, so the default is 1 and the panel's opinion is not consulted. Raise it
for a screen somebody stands close to; `0` restores the old behaviour of asking
the panel.

`darkMode` tells pages this browser prefers dark, through
`prefers-color-scheme`. A page that offers a dark theme takes it, and one that
does not is left as its author drew it.

`forceDarkContent` is for the second kind. Plenty of dashboards have a theme
setting of their own — kept in an account somewhere, defaulting to light — and
take no notice of what the browser prefers; on a wall in a dark room that page
is the brightest thing in the room. This inverts its colours anyway. It leaves
photographs and video alone, so a camera dashboard keeps its pictures and loses
its white chrome. It is not as good as a page's own dark theme, which is why it
is off by default: turn it on when the screen is still bright with `darkMode`
already on.

There used to be three dark-mode flags here and two of them did not exist.
Chromium ignores a switch it does not know without a word, so
`--force-prefers-color-scheme` and the `WebUIDarkMode` feature — both inherited
from setups running an older Chromium — sat on the command line doing nothing,
and the screen stayed white while every setting said dark. `make docker-smoke`
now measures the average brightness of an actual screenshot, because nothing
short of looking at the pixels catches that.

`closeUnexpectedTabs` gets rid of windows the daemon did not open. A page that
calls `window.open` gets a window of its own, and with no window manager it is
stacked in front of the one on the wall; a screen showing a single page would
otherwise stay covered by it until somebody walked over. A window is given one
cycle — about ten seconds — to close itself first, and what was closed is
always written to the log. Turn it off if a page here signs in through a popup.

`sandbox: true` needs two things, and both are in
`deploy/docker-compose.yml`.

The first is a non-root account, which is what `user` is for: Chromium refuses
to enable its own sandbox as root, and this browser renders whatever the
network serves it.

The second is `CAP_SYS_ADMIN` on the container. The sandbox creates process and
network namespaces, and a container's default seccomp policy refuses that
without the capability — Chromium then does not start at all, and what it says
about it names neither the container nor the setting. Granting it does **not**
turn seccomp off: the policy is capability-aware, so every other syscall stays
filtered. The daemon recognises this particular failure and says what to do.

If you would rather not grant it, set `sandbox: false`. The screen then works,
with a bug in a web page one step closer to everything else in the container.

`ephemeralCache` exists because a corrupted cache produces a page that will not
load while everything else looks healthy, and it survives every restart — so it
presents as a device that has to be reimaged. Making the cache disposable
removes the whole class of fault.

### certificates

    browser:
      certificateAuthorities:
        - |
          -----BEGIN CERTIFICATE-----
          ...
          -----END CERTIFICATE-----

An appliance on a private network — a camera recorder, a building controller, a
switch — signs its own certificate and the browser refuses the page. There are
two answers here and they are not equivalent.

`ignoreCertificateErrors: true` stops the browser checking anything, on every
page, for the life of the process. A device with that switched on cannot tell
its dashboard from anything else answering on the same address.

`certificateAuthorities` trusts that one certificate and goes on checking
everything else. Paste the appliance's certificate in — the Screen page in the
web interface has a box for it — and the page opens with no warning and no loss
of protection. `cue` puts them in the NSS database Chromium reads from its own
home directory, replacing what was there, so a certificate removed from the
configuration is no longer trusted by the device.

To get the certificate from an appliance:

    openssl s_client -connect the-appliance:443 -showcerts </dev/null \
        | openssl x509 -outform pem

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

## network

Off by default. A screen plugged into a wired network gets an address without
being asked, and there is nothing here to do. Turn `manage` on to join a
wireless network or to hold a fixed address.

    network:
      manage: false
      interfaces:
        - name: wlan0
          method: dhcp         # or static
          wireless:
            ssid: Office
            passphrase: ""     # empty for an open network
        - name: enp0s31f6
          method: static
          address: 192.0.2.10/24
          gateway: 192.0.2.1
          nameservers: [192.0.2.1]
          searchDomain: example.invalid

Only the interfaces listed are touched. An interface the machine already set up
and which is not named here is left exactly as it is, so turning `manage` on
cannot take a working device off the network.

The daemon reconciles every 30 seconds: it checks each listed interface still
has a usable address and, for wireless, that the supplicant is still associated
with the network asked for. A self-assigned `169.254` address does not count as
usable, which is what makes a device that came up before the DHCP server did
retry rather than sit there.

Wireless needs `wpa_supplicant`, which the image ships. The daemon runs and
supervises one per wireless interface and writes its configuration itself;
there is no file on the host to edit. Joining a network removes every other
network from the supplicant, because a screen that quietly falls back to a
network somebody configured months ago is worse than one that fails — it works,
on the wrong network, and nothing says so.

This needs the host's network namespace and `CAP_NET_ADMIN`. `cue` says so
rather than failing obscurely: with `manage` on inside a private network
namespace, the Network page explains that the interfaces it can see are the
container's own and not the machine's.

The Network page in the web interface lists the interfaces with hardware behind
them — a socket somebody can put a cable in, or a radio — with their addresses,
signal strength if wireless, and a scan-and-join. A machine running containers
also has a Docker bridge, one interface per running container and whatever a
VPN left behind; those are collapsed into a line at the bottom of the page,
because they carry traffic and can explain a routing problem, but there is
nothing on them to configure. An interface named in `network.interfaces` is
always shown, whatever it is.

The test for hardware is the `device` link the kernel puts in `/sys/class/net`,
not the name: `docker0` and `br-1a2b3c` are only bridges by convention. A
virtual machine's virtio interface has that link and counts as hardware, which
is right — in a virtual machine it is the machine's network card. It is the one page
that has to work when nothing else does — a screen carried into a room, plugged
in and switched on, with no keyboard and no network.

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

## service

    service:
      address: https://example.com
      secret: ""
      account: ""
      deviceId: ""
      name: ""

Where this device reports to, and the credential it holds once it is attached.
All of it is empty on a device that has never been linked, which is the normal
state: a device works entirely on its own with none of this set.

`address` is the only field an operator sets. The rest are written by the
device when a link completes and are read back only to be shown: `secret` is
what it presents when it connects, and is never sent to the interface or
written to the log; `account`, `deviceId` and `name` are what the service said
this device became.

`account` arrives already masked — `s•••@example.com` — because it is
displayed on the screen itself, and a wall in a lobby is no place for
somebody's email address. `name` is what the service calls this device, which
is not always what the device calls itself: an account cannot hold two devices
of one name, so a second screen calling itself `carbon` is recorded there as
`carbon 2`. The service's name is the one that matches the two systems up, so
it is kept and shown.

Linking is started from the menu at the screen or from the Service page of the
web interface, and finished on a phone. See `docs/planning/active/`. The
address is configurable rather than compiled in so that a device can be pointed
at a staging service without a different image.
## upgrade

    upgrade:
      allowApply: false

Whether this device may replace its own container with one built from a newer
release. Off unless you turn it on, and useless on its own: the daemon also has
to be able to reach `/var/run/docker.sock`, which means starting the container
with

    -v /var/run/docker.sock:/var/run/docker.sock

Two separate acts, both required, and neither can be done from the web
interface — including this setting, which is the one thing on this page with no
control in the interface. That is deliberate. The socket is not a small
permission: the other capabilities cue asks for let it do more to the machine
it is already on, while the socket lets it become any process on that machine.
A screen in a lobby has a web interface reachable by everybody on the network,
and granting this makes that interface's password the password to the host. If
the interface could grant it, the password would be protecting nothing.

Finding out whether a newer release exists is not configurable and is always
on. It reads a public API, changes nothing, and needs no account: a screen that
cannot tell you it is out of date is a screen nobody upgrades. A device with no
route to the internet says so on the Upgrade page rather than looking current.

With this off — which is every device by default — the Upgrade page still names
the newest release and shows what changed in it, and gives you the command to
run on the machine yourself.

## When nothing is plugged in

A machine with no screen attached has no output for the X server to drive.
`cue` starts, reports every connector as disconnected, and there is nothing to
show — which is correct, and is what the Device page says.

If you want a screen anyway, so that VNC has something to look at before a
monitor is carried over, force the connector on and give it a mode. The kernel
takes the first; `display.modeline` and `display.xorgConfiguration` take the
second:

    # On the host, before starting the container:
    echo on > /sys/class/drm/card0-HDMI-A-1/status

`cue` does not do this for you. Writing to sysfs needs a container privileged
well beyond what a display needs, and generating a Monitor section to do it
inside X would mean generating device sections — which this daemon deliberately
does not do, because a generated `xorg.conf` is reliably the thing that makes a
screen stay black.

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
