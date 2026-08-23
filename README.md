# Cue

Turn a headless Linux machine with a screen attached into a managed display.

One container image, one command, no host configuration. A fresh Debian
install with nothing on it but Docker becomes a screen showing whatever you
point it at — a dashboard, a camera wall, a video — and a web interface for
running it from your desk.

    docker run -d --name cue --network host --shm-size 1g \
      --device /dev/dri --device /dev/tty0 --device /dev/tty2 --device /dev/input \
      --cap-add SYS_TTY_CONFIG --cap-add SYS_TIME --cap-add SYS_ADMIN \
      -v /etc/cue:/etc/cue -v /var/lib/cue:/var/lib/cue \
      ghcr.io/ziyan/cue:latest

The screen lights up within about fifteen seconds showing its own address.
Open that from a laptop, set a password, and type in the pages you want shown.

Two of those flags are not obvious and both produce a black screen if they are
wrong. `/dev/tty2` is the console the X server draws on and has to match
`display.virtualTerminal`; left to itself the server picks a console that is
not in the container. `SYS_ADMIN` is what lets the browser keep its own
sandbox — it creates namespaces the default seccomp policy refuses without it
— and granting it leaves seccomp on for everything else.
`deploy/docker-compose.yml` says all of this at length, and
`make deploy HOST=...` does it for you.

## What it does

- **Shows pages full screen.** Chromium in kiosk mode: no tab strip, no
  address bar, no cursor, no way for a passer-by to leave the page. Several
  pages rotate on a timer, each for as long as you say.
- **Keeps pages signed in.** The dashboard this was built for expires its
  session every few hours and drops the tab back to a login form, where it
  sits on a wall until somebody walks over with a keyboard. Cue notices and
  signs it back in — checked every few seconds, not once when the tab opened.
- **Gets rid of what covers the page.** Cookie banners, "what's new"
  announcements, survey invitations. On a screen nobody touches, one of those
  hides the dashboard for weeks.
- **Notices when the screen has frozen.** A frozen display looks exactly like
  a working one, so the daemon asks: does the X server answer, does the page
  run JavaScript, does it reach its next animation frame? Failures escalate
  from reloading the page to restarting the graphics.
- **Follows the cables.** Unplug the HDMI lead and plug it into another
  socket; the picture comes back on its own, at the right resolution.
- **Lets you watch and drive it from a browser.** VNC, over an authenticated
  WebSocket, in a tab.
- **Reports what the machine is doing.** Processor, memory, disk,
  temperature, which sockets have monitors in them, what the clock is doing,
  and a live screenshot of the screen.
- **Keeps the clock right.** A browser cannot validate a certificate with a
  wrong clock, so a device whose battery has died would otherwise show a
  certificate error forever.

## What is in the image

Five programs, and nothing else that runs:

    cue         this daemon, as process 1
    Xorg        the X server (Xvfb for a machine with no screen)
    chromium    the browser
    x11vnc      the remote view
    chronyd     the clock

There is no shell, no package manager and no init system. The image is
assembled by installing Debian packages in a throwaway stage, collecting
exactly the files those packages own, and copying that onto
`gcr.io/distroless/base-debian13`. What is in it is listed in
`/usr/share/cue/packages.txt`.

## Getting started

`deploy/docker-compose.yml` is the same thing with the reasoning written down,
including what to pass through and what to do when the screen stays black. Or,
from a checkout:

    make deploy HOST=display-1 DISPLAY_MANAGER=stop

which stops whatever is holding the graphics device, sends this build over
ssh, starts it with the flags above, waits for it to report itself healthy,
and prints what the machine ended up driving.

Two things about the host, and they are the only two:

- **No display manager.** If the machine runs gdm, lightdm or sddm, it already
  holds the graphics device. `systemctl disable --now gdm3`.
- **A laptop needs its lid ignored.** In `/etc/systemd/logind.conf`, set
  `HandleLidSwitch=ignore`, or closing it turns the screen off.

Then open `http://<the machine>:8080/`.

## Configuration

Everything lives in one file, `/etc/cue/cue.yaml`, written both by hand and by
the web interface. After editing it by hand, `docker kill --signal HUP cue`.

    device:
      name: Reception
      timezone: Europe/London

    playlist:
      interval: 30s
      items:
        - url: https://dashboard.example.com/
          title: Sales
          reload: true

        - url: https://cameras.example.com/protect/dashboard
          title: Cameras
          login:
            whenUrlMatches: "/login"
            usernameSelector: "input[name=username]"
            passwordSelector: "input[name=password]"
            submitSelector: "button[type=submit]"
            username: display
            password: "..."
            expectUrlMatches: "/protect/dashboard"
          dismiss:
            - selector: "button"
              whenTextMatches: "Got it|Dismiss"

Every field is documented in
[docs/reference/configuration.md](docs/reference/configuration.md).

The file holds the passwords the screen signs in with. It is written 0600 and
nothing else should be able to read it.

## Building it

    make build     # the binary
    make test      # the tests
    make docker    # the image
    make docker-smoke   # start the image against a virtual screen and prove it works

Building needs Go and Docker and nothing else — no Node, no bundler. The web
interface is plain ES modules compiled into the binary.

## Documentation

- [AGENTS.md](AGENTS.md) — what this program is and where the code lives
- [CONTRIBUTING.md](CONTRIBUTING.md) — conventions, invariants, how to send a change
- [docs/reference/](docs/reference/) — configuration, the API, running it locally
- [docs/decisions/](docs/decisions/) — why the architecture is the way it is

## Licence

MIT. See [LICENSE](LICENSE).

noVNC, in `internal/web/static/novnc/`, is Mozilla Public License 2.0 and is
included unmodified.
