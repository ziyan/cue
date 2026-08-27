# Cue ships as one image containing exactly five programs that run: the cue
# daemon, the X server, Chromium, x11vnc and chronyd. There is no shell, no
# package manager and no init system in it.
#
# It is assembled in three stages. The first builds the daemon. The second
# installs the Debian packages the other four programs come from and collects
# precisely the files those packages own — not the whole builder filesystem —
# so the result is an exact, auditable set, listed in
# /usr/share/cue/packages.txt. The third copies that onto a distroless base.
#
# The base is debian 13 (trixie) throughout, because the packages and the base
# image have to agree about the C library.

# ---------------------------------------------------------------------------
# The daemon.
# ---------------------------------------------------------------------------
FROM golang:1.25-trixie AS daemon

# Passed by the release workflow so the binary can say what it is. Without
# them "cue version" reports 0.0.0-dev, which is useless in a bug report.
ARG VERSION=0.0.0-dev
ARG COMMIT=unknown

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor \
    -ldflags "-s -w -extldflags \"-static\" \
      -X github.com/ziyan/cue/internal/version.version=${VERSION} \
      -X github.com/ziyan/cue/internal/version.commit=${COMMIT}" \
    -o /cue .

# ---------------------------------------------------------------------------
# The four programs the daemon supervises, and the libraries and data they
# need.
# ---------------------------------------------------------------------------
FROM debian:trixie-slim AS rootfs

# Which architecture this image is being built for. buildx sets it; a plain
# "docker build" does not, and the default covers that.
ARG TARGETARCH=amd64

# PACKAGES is what the image is for. Each line says why it is here.
ENV PACKAGES="\
    chromium chromium-sandbox \
    xserver-xorg-core \
    xserver-xorg-video-amdgpu xserver-xorg-video-nouveau xserver-xorg-video-fbdev \
    xserver-xorg-input-libinput \
    x11-xkb-utils xkb-data \
    xvfb \
    x11vnc \
    chrony \
    wpasupplicant iw wireless-regdb \
    libgl1-mesa-dri mesa-va-drivers libglx-mesa0 libvulkan1 \
    fonts-liberation2 fonts-dejavu-core fonts-noto-color-emoji fontconfig \
    ca-certificates libnss3-tools tzdata"

# Two X drivers exist only for x86, and asking for them on any other
# architecture fails the build outright — which is how the first
# multi-architecture release would have broken. The intel driver is for older
# Intel chips, since modesetting drives the modern ones; the vesa driver is the
# last resort for hardware with no kernel mode setting at all. Neither has an
# equivalent to miss on ARM, where the graphics are always driven by a kernel
# driver. tools/checkpackages checks this against Debian's own indexes so it
# cannot drift back.
ENV X86_PACKAGES="xserver-xorg-video-intel xserver-xorg-video-vesa"

# EXCLUDED names the packages whose *files* are left out. Two kinds:
#
#   - The command line utilities of the builder's own base system. This image
#     has no shell on purpose, and shipping coreutils, dpkg and apt inside it
#     would undo the point of that. Their shared libraries live in separate
#     packages and are kept.
#   - An init system, a service manager and a message bus, which arrive as
#     dependencies of the X server but which nothing here runs.
#
# Everything else installed in this stage is copied, base system included.
# An earlier version copied only the packages apt *added*, which silently
# dropped the libraries the builder's base image already had — and the first
# thing that happened was Xvfb failing to start for want of libselinux.so.1.
ENV EXCLUDED="\
    apt apt-utils base-files bash bsdutils coreutils dash debconf debian-archive-keyring \
    debianutils diffutils dpkg e2fsprogs findutils gpgv grep gzip hostname \
    init-system-helpers login logsave mawk mount ncurses-bin passwd perl-base \
    sed sysvinit-utils tar util-linux \
    systemd systemd-sysv systemd-timesyncd udev \
    dbus dbus-bin dbus-daemon dbus-session-bus-common dbus-system-bus-common \
    dbus-user-session keyboard-configuration \
    tcl tcl8.6 tk tk8.6 libtcl8.6 libtk8.6 \
    adwaita-icon-theme"

RUN set -eux; \
    if [ "${TARGETARCH}" = "amd64" ] || [ "${TARGETARCH}" = "386" ]; then \
        PACKAGES="${PACKAGES} ${X86_PACKAGES}"; \
    fi; \
    apt-get update -qq; \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ${PACKAGES}; \
    dpkg-query -W -f='${binary:Package}\n' | sort -u > /tmp/installed.txt; \
    cp /tmp/installed.txt /tmp/wanted.txt; \
    for package in ${EXCLUDED}; do \
        sed -i "\|^${package}\$|d" /tmp/wanted.txt; \
    done; \
    # dpkg -L lists every path a package owns, directories included. Feeding
    # that to tar with --no-recursion copies exactly those paths with their
    # permissions — which matters for chrome-sandbox, whose setuid bit is what
    # lets Chromium keep its process sandbox.
    : > /tmp/files.txt; \
    while read -r package; do \
        dpkg -L "${package}" >> /tmp/files.txt; \
    done < /tmp/wanted.txt; \
    sort -u /tmp/files.txt | sed '/^\/\.$/d' > /tmp/paths.txt; \
    mkdir -p /rootfs; \
    tar -C / -cpf - --no-recursion --files-from=/tmp/paths.txt 2>/dev/null | tar -C /rootfs -xpf -; \
    # Files that no package owns because a maintainer script generated them:
    # the bundle of trusted certificates, the font cache, and the shared
    # library cache. Without the first, every https page fails; without the
    # second, Chromium rebuilds it on every start.
    fc-cache -f >/dev/null 2>&1 || true; \
    ldconfig; \
    mkdir -p /rootfs/etc/ssl/certs /rootfs/var/cache/fontconfig; \
    cp -a /etc/ssl/certs/. /rootfs/etc/ssl/certs/; \
    cp -a /etc/ld.so.cache /rootfs/etc/ld.so.cache; \
    cp -a /var/cache/fontconfig/. /rootfs/var/cache/fontconfig/ 2>/dev/null || true; \
    # The record of what is in here, so a security question about this image
    # can be answered without unpacking it.
    mkdir -p /rootfs/usr/share/cue; \
    while read -r package; do \
        dpkg-query -W -f='${binary:Package} ${Version} ${Architecture}\n' "${package}"; \
    done < /tmp/wanted.txt > /rootfs/usr/share/cue/packages.txt; \
    # The directories the daemon owns.
    mkdir -p /rootfs/etc/cue /rootfs/var/lib/cue /rootfs/run/cue /rootfs/tmp/.X11-unix; \
    # /bin/sh is the cue binary, which answers to that name with a shell that
    # runs one simple command and refuses every shell feature. The X server
    # compiles its keyboard map by running xkbcomp through popen, which is
    # "/bin/sh -c ...", and without this it fails to start with a message
    # mentioning neither the shell nor xkbcomp. See internal/minishell.
    ln -sf /usr/local/bin/cue /rootfs/usr/bin/sh; \
    chmod 1777 /rootfs/tmp /rootfs/tmp/.X11-unix; \
    rm -rf /rootfs/usr/share/doc /rootfs/usr/share/man /rootfs/usr/share/locale \
           /rootfs/usr/share/info /rootfs/var/lib/apt /rootfs/var/lib/dpkg/info; \
    # The base image already has these, as directories in one case and
    # symbolic links in the other; a copy that disagrees fails the build with
    # "cannot replace to directory".
    rm -rf /rootfs/var/lock /rootfs/var/run /rootfs/bin /rootfs/sbin \
           /rootfs/lib /rootfs/lib64 /rootfs/usr/lib64; \
    du -sh /rootfs

# The accounts. Chromium refuses to enable its own sandbox when it is running
# as root, and this browser renders whatever the network serves it, so the
# daemon starts it as an unprivileged account instead. The daemon and the X
# server stay root because the graphics device needs it.
#
# _chrony is there because Debian's chronyd is built to drop privileges to
# that account and refuses to start without it — "Fatal error : Could not get
# user/group ID of _chrony". It parses packets from the network, so keeping
# that privilege separation is worth an extra line here.
#
# The supplementary groups are worked out by the daemon at run time from the
# ownership of the device files, because the group numbers for video, render
# and audio differ between hosts and a fixed number here would be wrong on
# somebody's machine.
RUN set -eux; \
    printf '%s\n' \
        'root:x:0:0:root:/root:/sbin/nologin' \
        'cue:x:1000:1000:cue:/var/lib/cue/browser:/sbin/nologin' \
        '_chrony:x:123:127:chrony:/var/lib/cue/chrony:/sbin/nologin' \
        'nobody:x:65534:65534:nobody:/nonexistent:/sbin/nologin' \
        > /rootfs/etc/passwd; \
    printf '%s\n' \
        'root:x:0:' \
        'cue:x:1000:' \
        '_chrony:x:127:' \
        'nogroup:x:65534:' \
        > /rootfs/etc/group; \
    chown -R 1000:1000 /rootfs/var/lib/cue

# ---------------------------------------------------------------------------
# The image itself.
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/base-debian13:latest AS runtime

COPY --from=rootfs /rootfs /
COPY --from=daemon /cue /usr/local/bin/cue

# Chromium and the X server are found on PATH, and Xorg lives outside the
# usual one.
ENV PATH=/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin:/usr/lib/xorg
ENV CUE_CONFIG=/etc/cue/cue.yaml

# The web interface, and the VNC server for a viewer that would rather not go
# through it. Both are only reachable if the operator publishes them.
EXPOSE 8080 5900

VOLUME ["/var/lib/cue"]

ENTRYPOINT ["/usr/local/bin/cue"]
CMD ["run"]
