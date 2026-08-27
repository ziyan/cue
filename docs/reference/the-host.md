# What Cue needs from the machine it runs on

Cue is one container and tries to need as little from the host as possible.
"As little as possible" is not "nothing", and what is left divides into three
kinds: things that must be running, things that must not be holding the screen,
and things that will fight Cue for a device if both are switched on.

This page is what to check when a screen behaves strangely on a machine that is
also used for something else.

## What must be running

**A container runtime.** `docker` and `containerd`. Nothing else about Docker
matters; Cue is started with `docker run` and asks for nothing unusual beyond
the devices below.

**systemd-udevd.** This one is easy to miss and its failure is baffling. The X
server does not look in `/dev/input` to find a keyboard and a mouse — it asks
udev, over a socket, and reads what udev has recorded under `/run/udev/data`.
Cue mounts that directory read-only for exactly this reason. On a machine where
it is missing, X starts, the screen lights up, everything looks right, and no
keyboard or mouse works at all. On a machine where it is present the X log says
how many devices it found:

    docker logs cue 2>&1 | grep -c "device is a"

Zero on a machine with a keyboard attached means udev is the thing to look at.

**A kernel driver for the graphics device.** Not a process, but worth naming:
`/dev/dri` has to exist and be passed through, which the deployment tool does
when it is there.

## What must not be holding the screen

**Any display manager** — `gdm`, `gdm3`, `lightdm`, `sddm`. A display manager
owns the graphics device and the virtual terminal, and Cue's X server cannot
have them while it does. `make deploy DISPLAY_MANAGER=stop` stops and disables
whatever it finds; on a machine set up as a display this should be off for good.

**A getty on the virtual terminal Cue uses.** Cue's X server runs on `vt2`. A
login prompt on the same terminal fights it for the console:

    systemctl is-active getty@tty2

## What will fight Cue if both are switched on

These are the ones that do not announce themselves. Both programs start
happily, both appear to work, and the symptom turns up somewhere else entirely.

**A time daemon.** Cue runs `chronyd` inside the container to keep the screen's
clock right, and a machine that already runs `chrony` or `systemd-timesyncd`
then has two programs steering one clock. Turn Cue's off:

    time:
      enabled: false

That is what the setting is for. A machine that is only a screen should
generally let Cue do it; a machine that is also somebody's laptop already has
one.

**wpa_supplicant, and NetworkManager behind it.** A wireless radio can be
driven by exactly one program. If Cue is configured to manage a wireless
interface and NetworkManager is also managing it, the two fight, and what that
looks like is a radio that will not scan, will not become an access point, and
answers every request with a bare error number.

Decide which one owns the radio:

  - **Cue owns it.** Tell NetworkManager to leave that interface alone, and
    make it stick across a reboot — `nmcli dev set wlp4s0 managed no` is
    forgotten when the machine restarts. Write a file instead:

        /etc/NetworkManager/conf.d/99-cue.conf
        [keyfile]
        unmanaged-devices=interface-name:wlp4s0

  - **NetworkManager owns it.** Leave `network.manage` off in Cue, or leave
    that interface out of `network.interfaces`. Setting a device up over the
    air needs Cue to own the radio, so this rules that out.

Cue notices this one when it tries to start an access point and says so, naming
the network the radio is already on and the command that frees it. It cannot
notice the general case: it has its own view of processes and cannot see the
host's.

## What Cue is given, and why

    /run/udev        read-only   so the X server can find the keyboard and mouse
    /etc/cue         read-write  the configuration, which the daemon rewrites
    /var/lib/cue     read-write  the browser profile, uploads, wireless configs
    /dev/dri                     the graphics device
    /dev/tty0, tty2              the console and the terminal X runs on
    /dev/input                   keyboards, mice, touchscreens
    /dev/snd                     sound, when the machine has any

    CAP_NET_ADMIN        configuring interfaces, when asked to manage the network
    CAP_SYS_ADMIN        what the X server needs for the console
    CAP_SYS_RAWIO        likewise
    CAP_SYS_TIME         setting the clock, when the time client is on
    CAP_SYS_TTY_CONFIG   switching virtual terminals

It is not privileged, and it does not share the host's process namespace. The
network namespace is the host's, because a screen that manages its own network
is managing the machine's.
