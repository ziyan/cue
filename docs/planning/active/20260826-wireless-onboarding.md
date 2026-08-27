# Let somebody set up a new screen with nothing but a phone

This ExecPlan is a living document. The sections `Progress`, `Surprises &
Discoveries`, `Decision Log`, and `Outcomes & Retrospective` must be kept up to
date as work proceeds. It is maintained in accordance with the ExecPlan rules
described in `docs/coding/execplans.md`.

## Purpose / Big Picture

Today a new Cue device is useless until somebody puts it on a network. The
screen comes up showing the welcome page, which says "open
`http://<an address on a network it has not joined>:8080/`" — an address that only exists if the machine
already has a network, which is exactly what it does not have. If the room has
no ethernet cable, the person standing in front of the screen has no way
forward at all. They have to find a cable, or a keyboard and a shell, or take
the machine back to a desk. Every wireless screen so far has been set up by
carrying it to somebody's office first.

After this work, a freshly provisioned device with wireless hardware and no
network puts a **QR code** on its own screen, along with a short instruction.
Somebody points a phone camera at it. The phone joins the device's own
temporary wireless network — the QR code carries the network name and password,
so there is nothing to type — and the phone's captive-portal prompt opens
automatically, the same way a hotel or airport network does. That page is
served by the device itself. It lists the wireless networks the device can see,
sorted by signal strength. The person taps their own network, types its
password on the phone keyboard, and taps Join. The device leaves its temporary
network, joins theirs, and the screen changes to show its new address. If the
password was wrong, the device brings its own network back up within thirty
seconds and the phone reconnects to it, so a typo never strands the device.

**How you will see it working.** On a machine with wireless hardware and no
network configuration, start the daemon. The attached screen shows a QR code
and the words "Scan to set up this screen". Scan it with a phone. The phone
joins `cue-setup-XXXX` and opens a page headed "Set up this screen" listing
nearby wireless networks. Choose one, enter its password, tap Join. Within
about twenty seconds the screen changes to the ordinary welcome page showing
`http://<the address it just got>:8080/`, and `ssh`-ing to the machine shows
the interface associated with the chosen network. Tests prove each piece
separately, and `make docker-smoke` proves the daemon still works end to end.

## Definitions

Terms used throughout this plan, each defined once, here.

**Access point mode, or AP mode.** A wireless radio normally works as a
*station*: it joins a network somebody else is running. In *access point mode*
it runs a network of its own that other devices join, which is what a home
wireless router does. A radio that supports AP mode can be either, and some can
be both at once.

**wpa_supplicant.** The program already in this image that operates the
wireless radio. Cue starts it and talks to it over a Unix socket, in
`internal/network/wireless.go`. It is usually thought of as the *station* side
program, with `hostapd` as its access-point counterpart, but the build Debian
ships — and therefore the build in this image — has AP mode compiled in, so it
can run the temporary network too. This matters because the image contains no
`hostapd` and adding one would mean shipping another package.

**Captive portal.** The behaviour where joining a wireless network causes a
phone to pop up a web page before it will use the network normally. Phones do
this by fetching a known URL from their vendor immediately after joining and
seeing whether the expected answer comes back. Apple devices fetch
`http://captive.apple.com/hotspot-detect.html` and expect a small page
containing the word `Success`. Android fetches
`http://connectivitycheck.gstatic.com/generate_204` and expects an HTTP 204
with no body. Windows fetches `http://www.msftconnecttest.com/connecttest.txt`
and expects the exact text `Microsoft Connect Test`. If the answer is anything
else — for instance a redirect to our own page — the phone concludes the
network is "captive" and opens that page in a browser window. There is no
standard for this and no way to make it happen other than deliberately
answering those probes wrongly.

**Onboarding mode.** The state this plan adds: the device is running its own
temporary wireless network, a DHCP server, a DNS responder and a setup portal,
because it has no network of its own and nobody has told it what to join.

**DHCP server.** The thing that hands an address to a device joining a network.
Cue already contains a DHCP *client* (it asks for an address). Onboarding mode
needs the other half, so that a phone joining `cue-setup-XXXX` is given an
address instead of sitting there with none.

**Onboarding DNS.** A minimal DNS server that answers every question with the
device's own address. This is what makes the captive portal work: whatever host
name the phone asks for, it is told to come to us, so its probe reaches our web
server and gets the "wrong" answer that triggers the popup.

**Station.** See *access point mode*. The ordinary mode, joining somebody
else's network.

**PSK, or passphrase.** The password of a wireless network. `WPA2-PSK` is the
ordinary kind of home wireless security, where everybody who knows one shared
password can join.

## Progress

- [x] (2026-08-26 20:10Z) Researched feasibility: confirmed the image has no
      `hostapd` and no `dnsmasq`, that its `wpa_supplicant` v2.10 has AP mode
      compiled in, and that a real target radio supports AP mode alongside a
      station on one channel. Evidence in `Surprises & Discoveries`.
- [x] (2026-08-26 20:20Z) Spiked the two dependencies: `github.com/skip2/go-qrcode`
      (QR encoding, MIT, no dependencies of its own) and
      `github.com/insomniacslk/dhcp/dhcpv4/server4` (DHCP server, from a module
      already required). Both vendor and run. Vendored on `main`.
- [x] (2026-08-26 21:30Z) Milestone 1: QR code and on-screen instruction on
      `/welcome`, carrying the setup network's credentials when there is one
      and the device's web address otherwise. `internal/util/qr`,
      `internal/network/credentials.go`, and the page itself. Verified by
      decoding a screenshot of the rendered screen with an independent
      scanner. Remaining for later milestones: nothing runs a setup network
      yet, so `Daemon.SetupNetwork` answers "none" and the page falls back to
      the address, exactly as before.
- [ ] Milestone 2: bring the temporary network up and down (`internal/network/accesspoint.go`).
- [x] (2026-08-26 22:10Z) Milestone 3: DHCP server and DNS responder,
      `internal/network/onboarding/`. The DNS wire format is checked against
      `dig` and Go's own resolver, not only against the test's own query
      builder.
- [x] (2026-08-26 22:30Z) Milestone 4: the setup portal, the eight captive
      probes, and the join call. `internal/web/portal.go`. Rendered at phone
      size and looked at.
- [x] (2026-08-26 22:45Z) Milestone 5: `internal/onboarding`, the trigger in
      `Daemon.considerOnboarding`, the `network.onboarding` setting and its
      control on the Network page. Remaining: none of it has run on real
      hardware yet -- see the note below.
- [ ] Milestone 6: documentation, changelog, decision record.
- [x] (2026-08-26 22:55Z) Ran on a real radio: carbon advertised `cue-gbkthq`
      and a second machine's radio saw it on WPA2. The portal listed nine real
      networks from the room, the welcome page carried the join code, and a
      captive probe answered 302 to the portal.
- [ ] **A phone has still not joined one.** Blocked on the radio: see the
      NetworkManager discovery below.
- [ ] Old note, kept for the record: **the whole thing has never run on a radio.** Every piece is tested
      separately and the pieces that need no hardware are tested properly, but
      no access point has been advertised, no phone has joined one, and no
      captive portal has opened by itself. That needs a wireless interface that
      NetworkManager is not holding, and freeing one needs a local session that
      an SSH command does not have.

## Surprises & Discoveries

- Observation: The first apparently successful join was not one. The daemon
  reported "joined and has an address; setup is finished" three quarters of a
  second after taking the access point down, put the playlist back on the
  screen, and reached nothing. The setup network's own address was still on the
  interface, and two separate things read it as "this device has a network":
  the DHCP client, which skips an interface that already has a usable address
  and so never asked for a real one, and the check for whether the join had
  worked.

  Evidence: after the join, `ip -4 addr show wlp4s0` showed only
  `192.168.216.1/24` -- the address the daemon had given itself -- while the
  configuration said the device was on somebody else's network. Removing that
  address by hand and letting the manager run produced the real one:

      inet 192.168.255.230/24 ... wlp4s0
      SSID: joe

  GiveAddress had no counterpart. It does now, the join discounts the setup
  address when deciding whether it worked, and so does the trigger that decides
  whether the device needs setting up at all -- which would otherwise have
  switched setup off one second after switching it on.

- Observation: With a wireless network configured and onboarding left on
  "always", the daemon fights itself for the radio: its own network manager
  holds the interface on the chosen network while onboarding tries to make it
  an access point. The log said "another program has it joined to joe and is
  driving the radio", and the other program was itself. Onboarding now stands
  down whenever the manager is configured to drive that interface, whatever the
  mode says.

- Observation: The captive portal never appeared, and the reason was not DNS --
  which worked -- but that nothing was listening on port 80. A phone probes its
  vendor's address on port 80, the name server correctly sends it to this
  device, and the connection was then refused, so the phone learned nothing and
  showed no page. The daemon's interface is on 8080, and it had not occurred to
  me that the portal needs a door on the port a phone actually knocks on. The
  redirect target had the same fault: a phone following a redirect cannot be
  told a port.

  Evidence: with a listener on 192.168.216.1:80, from the device itself,

      captive.apple.com                192.168.216.1
      /hotspot-detect.html     302 -> http://192.168.216.1/portal
      /generate_204            302 -> http://192.168.216.1/portal
      /connecttest.txt         302 -> http://192.168.216.1/portal

  A test now asserts the redirect target names no port, and says why.

- Observation: Marking the interface unmanaged in NetworkManager is not enough.
  Its wpa_supplicant keeps the radio even after the device is unmanaged and
  even after the daemon's own attempts have stopped, and while it does, the
  radio can neither scan nor become an access point. `systemctl stop
  wpa_supplicant.service` is what actually frees it. Ethernet does not use that
  service, so a wired machine keeps its network.

  Evidence: with the device unmanaged but the service running,

      $ iw dev wlp4s0 set type __ap
      command failed: Device or resource busy (-16)
      $ iw dev wlp4s0 scan
      command failed: No such file or directory (-2)

  and with the service stopped and the link brought down first, both work:
  `type AP`, and a scan finding 57 networks. The daemon's existing check only
  notices another program when the radio is *associated*, so it did not catch
  this case. Detecting it properly means probing directly -- asking the kernel
  to put the interface into access point mode and seeing whether it says
  EBUSY -- which is the next thing to do here.

- Observation: Changing a wireless interface's type needs the link down first.
  With it up the kernel answers EBUSY, which reads as "something else has this"
  and sent me looking for a program that was not there.

- Observation: I nearly talked myself out of a correct design with a bad test.
  Checking whether the image's wpa_supplicant had access point support by
  grepping its strings for `^AP-ENABLED$` returned nothing, and I was about to
  conclude the whole approach was impossible and that hostapd had to be added.
  The strings carry trailing spaces and format verbs -- `AP-ENABLED `,
  `AP-STA-CONNECTED %s%s%s` -- so the anchored pattern could never match. The
  observation that a second machine had already seen the network on the air was
  the stronger evidence, and it was right.

- Observation: On a machine where NetworkManager is running, the setup network
  cannot come up at all, and the failure says nothing about why. NetworkManager
  keeps its own wpa_supplicant on the radio; ours starts cleanly, fails every
  scan with a bare error number, and never advertises.

  Evidence: the log filled with `wlp4s0: CTRL-EVENT-SCAN-FAILED ret=-2 retry=1`
  and then `was not ready within 20s`, while `nmcli dev status` showed the same
  interface `connected` to a network. The device now detects this and says so:

      cannot offer to be set up over the air: network: wlp4s0 did not come up as
      "cue-wcu83k" because another program has it joined to "joe" and is driving
      the radio; stop it managing this interface and try again (with
      NetworkManager that is "nmcli dev set wlp4s0 managed no")

  Detecting it needed care. The obvious check -- look for another
  wpa_supplicant in /proc -- does not work, because the daemon runs in a
  container with its own view of processes. Asking the kernel over nl80211
  which network the interface is associated with does work, because the
  container shares the kernel's view of the machine's interfaces. The daemon
  has joined nothing, so a radio sitting on somebody's network was put there by
  something else.

- Observation: The setup network must keep its name and passphrase when it goes
  down and comes back, and the first version did not. It comes down twice in
  normal use -- to free the radio for a scan, and to free it to try joining --
  and each restart invented new credentials, so the phone would have been
  looking for a network that no longer existed while the new password sat on a
  screen the person had walked away from. Found by reading the recovery path
  rather than by a test, and now pinned by one.

- Observation: Taking the screen was a separate problem from deciding to offer
  setup. The tabs are worked out when the browser starts and when the
  configuration changes, and setup starting is neither, so a device with a
  playlist went on showing it while quietly advertising a setup network nobody
  could see the code for. Reported from the room as "I see the cue wifi, but I
  do not see the qrcode".

- Observation: The welcome page listed the addresses of Docker and libvirt
  bridges alongside the real one, and the QR code carries the first address in
  that list. On a machine where a bridge sorted first, the screen would have
  told somebody to scan a code leading nowhere. Found by looking at the
  rendered page rather than by a test, which would not have noticed.

  Evidence: the page as it first rendered offered the machine's real address
  followed by its libvirt bridge and its Docker bridge, all three as things to
  open. `machineAddresses` now prefers interfaces with
  hardware behind them, using `network.Interfaces()`, and shows at most three.

- Observation: The generated code really is scannable, verified with a scanner
  that has nothing to do with the encoder that made it. This matters because
  every test written here uses the same library on both sides and would agree
  with itself about a code no phone could read.

  Evidence: rendering the onboarding screen to a PNG through the image's own
  Chromium and decoding that PNG with `zbarimg`:

      $ zbarimg --quiet --raw setup.pgm
      WIFI:S:cue-4k2p9x;T:WPA;P:hd7Rk2m9Qw4x;;

- Observation: The image has no `hostapd` and no `dnsmasq`, so the obvious way
  to run a temporary network is not available without shipping more packages.
  Its `wpa_supplicant` can do it instead.

  Evidence: the package list in `deploy/Dockerfile` is
  `wpasupplicant iw wireless-regdb` with no access-point or DHCP-server
  package, and the binary in the image reports AP support:

      $ docker run --rm --entrypoint /usr/sbin/wpa_supplicant cue:dev -v
      wpa_supplicant v2.10
      $ strings wpa_supplicant | grep -ciE '^AP-STA-CONNECTED$|^ap_scan$|hostapd'
      43

- Observation: A real target radio can run an access point and a station at the
  same time, but only on one channel. This rules out the simplest imagined
  design — "keep the setup network up while joining theirs to check the
  password" — whenever the two are on different channels, which is the normal
  case.

  Evidence: on `carbon`, whose radio is the one to design against:

      valid interface combinations:
         * #{ managed } <= 1, #{ P2P-client, P2P-GO } <= 1, #{ P2P-device } <= 1,
           total <= 3, #channels <= 2
         * #{ managed } <= 1, #{ AP, P2P-client, P2P-GO } <= 1, #{ P2P-device } <= 1,
           total <= 3, #channels <= 1

  The second line is the one that allows AP: it permits one AP and one station
  together, but `#channels <= 1` means both must sit on the same channel.

- Observation: `github.com/skip2/go-qrcode` has no dependencies outside its own
  subpackages, so vendoring it adds one module and no transitive weight. The
  DHCP server needs no new module at all, because
  `github.com/insomniacslk/dhcp` is already required for the client.

  Evidence: after `go get` and `go mod vendor`, the only line added to `go.mod`
  was `github.com/skip2/go-qrcode`, and a spike printed:

      qr err: <nil>
      qr modules: 37 x 37
      dhcp server4 handler type: server4.Handler

## Decision Log

- Decision: Run the temporary network with `wpa_supplicant` in AP mode rather
  than adding `hostapd` to the image.

  Rationale: the image is deliberately small and every package in it is one
  more thing to track for security updates. The `wpa_supplicant` already
  present can do this, and Cue already knows how to start, stop and talk to it,
  so this reuses machinery rather than adding a second wireless daemon with its
  own configuration language and control socket.

  Date/Author: 2026-08-26, Claude (with Ziyan).

- Decision: Write the DHCP server and the DNS responder inside Cue rather than
  shipping `dnsmasq`.

  Rationale: the same argument, and the scope is genuinely small. The DHCP
  server has to serve one address range to a handful of phones for a few
  minutes; the DNS server has to answer every question with one address. Both
  are a few hundred lines against libraries already vendored, and both stop
  when onboarding stops, so neither is a service running on a device in normal
  use.

  Date/Author: 2026-08-26, Claude (with Ziyan).

- Decision: The temporary network is protected with WPA2 and a passphrase
  generated per device, and that passphrase is shown *only* on the physical
  screen, inside the QR code.

  Rationale: the setup portal has to work before there is any password on the
  device, so it cannot be authenticated. That means whoever can reach it can
  configure the screen. Making the network open would put that within reach of
  anybody in radio range. Requiring the passphrase means requiring line of
  sight to the screen, which is a reasonable definition of "is allowed to set
  this up". The QR code means the person still types nothing.

  Date/Author: 2026-08-26, Claude (with Ziyan).

- Decision: Onboarding never starts on a device that already has a working
  network, and never starts unless wireless hardware that supports AP mode is
  present.

  Rationale: a device on a wall that loses its network for a minute must not
  respond by tearing down its connection and broadcasting an open-ish setup
  network. The trigger is deliberately conservative and is spelled out in
  Milestone 5.

  Date/Author: 2026-08-26, Claude (with Ziyan).

- Decision: Scan for networks *before* bringing the access point up, and cache
  the result for the portal to show.

  Rationale: the radio can only be an AP and a station on the same channel, so
  a scan across all channels while the AP is running would either fail or
  interrupt the very network the phone is sitting on. Scanning first costs a
  few seconds during startup, when nobody is connected yet, and the portal
  offers an explicit "Scan again" button that accepts the interruption when the
  person asks for it.

  Date/Author: 2026-08-26, Claude (with Ziyan).

## Outcomes & Retrospective

**Milestone 1, 2026-08-26.** The screen now shows a QR code, and the machinery
for deciding what it says is in place and tested. A scanner reading a
screenshot of the onboarding screen returns the exact join string, so the
promise "point a phone at this and it joins" is proven rather than assumed.

What is not there yet is the network the code names: nothing brings the radio
up as an access point, so `Daemon.SetupNetwork` reports no setup network and
the page shows the device's web address as it always did. The onboarding screen
can only be seen by a test that supplies credentials. That is the honest state
of it, and Milestone 2 is what makes the code on the screen mean something.

A lesson worth keeping: the bridge-address defect above was invisible to every
test and obvious in a screenshot. Rendering the page and looking at it needs to
be part of each remaining milestone, not a final step.

## Context and Orientation

Cue is one Go program, `cue`, that runs inside a container image with no shell.
It starts and supervises an X server, Chromium in kiosk mode, `x11vnc` and
`chronyd`, and serves a web interface on port 8080. The entry point is
`cmd/run.go`; the thing that owns all the child programs is `internal/daemon`.

The parts this plan touches:

`internal/network/` is where everything about networking lives.
`network.go` lists the machine's interfaces (`Interfaces()`), and knows which
ones have real hardware behind them (`isPhysical`). `address.go` applies an
address to an interface, either static or by DHCP client. `wireless.go` talks
to `wpa_supplicant` over its control socket: `Scan(interfaceName)` returns the
networks in range as `[]WirelessNetwork`, and `Join(interfaceName, ssid,
passphrase)` adds a network and connects to it. `manager.go` ties it together:
`Manager.Run` reconciles the machine against the configuration every so often,
and `Manager.ensureSupplicant` starts a `wpa_supplicant` for an interface,
writing it a configuration file first in `writeSupplicantConfiguration`.

`internal/web/` serves the interface. `web.go` has the route table in
`routes()`. Most routes sit behind `requireSession`, which refuses anything
without a signed-in session. Three do not: `/healthz`, `/welcome`, and the
setup and sign-in endpoints, because they have to work before there is a
password. `welcome.go` renders the page the device shows *on its own screen*
when there is no playlist — it is served unauthenticated because only the
browser on this machine can reach it, and its job is to tell somebody standing
in front of the screen where to go.

`internal/browser/playlist.go` decides what the kiosk browser shows. When there
is no playlist it shows `holdingPageURL()`, which is
`http://127.0.0.1:8080/welcome`.

`internal/config/configuration.go` is the whole configuration as Go structs,
loaded from `/etc/cue/cue.yaml`. `Network` holds `Manage`, `ReconcileInterval`
and a list of `Interface`. Anything added there is automatically expected to
have a control in the web interface: a test,
`TestEverySettingIsReachableFromTheInterface` in
`internal/web/configurable_test.go`, walks the configuration struct and fails
if a field is not mentioned anywhere in the interface's JavaScript, unless it
is listed in `deliberatelyNotInTheInterface` with a reason.

Tests that need programs only the image has — `Xvfb`, `x11vnc`, Chromium,
`wpa_supplicant` — are built and run *inside* the image by `make docker-test`.
The list of packages treated this way is `IMAGE_TESTED_PACKAGES` in the
`Makefile`. On a developer machine those tests skip. `make docker-smoke` starts
the real image against a virtual screen and checks a picture reaches it; it is
the test that matters most.

## Plan of Work

### Milestone 1 — the screen says what to do

**Scope.** Before any wireless work, make the screen useful: put a QR code and
an instruction on the welcome page, and settle what the code contains.

The code carries **the credentials of the temporary wireless network**, in the
format every phone camera understands:

    WIFI:S:cue-4k2p9x;T:WPA;P:hd7Rk2m9Qw4x;;

Scanning that offers to join the network, with nothing typed. This is the whole
point of the passphrase-on-screen decision above: the passphrase exists only
inside this code, on this screen, so being able to set the device up means
being able to see it.

On a device that already has a network there is no temporary network and no
passphrase, and the code carries the device's web address instead, so that
scanning it opens the interface. The page therefore asks what the code should
say rather than deciding for itself.

**Work.** Add `internal/util/qr/qr.go` wrapping `github.com/skip2/go-qrcode`,
exposing one function:

    // Encode turns text into a square matrix of black and white modules,
    // where true is black. The matrix includes the quiet border a scanner
    // needs.
    func Encode(text string) ([][]bool, error)

Render it in the welcome page as an inline SVG rather than an image file: no
extra request, no image encoding, and it scales to any screen. Add
`renderQR(matrix [][]bool) template.HTML` in `internal/web/welcome.go` and a
block in `welcomeTemplate`.

Add `internal/network/credentials.go` with the naming and the code string,
which Milestone 2 then uses to configure the radio:

    // Credentials are the name and passphrase of the temporary network.
    type Credentials struct { SSID, Passphrase string }

    // NewCredentials invents a name and passphrase for one setup session.
    func NewCredentials() (Credentials, error)

    // JoinCode is what a phone camera reads to join this network without
    // anybody typing anything.
    func (self Credentials) JoinCode() string

**Acceptance.** Run, from the repository root:

    go test ./internal/util/qr/ ./internal/web/ -run 'QR|Welcome' -v

Expect the new tests to pass, and to have failed before the change. The
important one is `TestTheQRCodeOnTheWelcomePageIsTheCodeForWhatItSays`, which
reads the drawn SVG back into a matrix and compares it with the matrix the
encoder produces, so a page showing the code for the wrong thing fails. Then
start the daemon and fetch the page:

    curl -s http://127.0.0.1:8080/welcome | grep -c '<svg'

Expect `1`. Looking at the screen shows the QR code; scanning it with a phone
opens the device's web interface.

### Milestone 2 — the temporary network

**Scope.** Bring an access point up on a wireless interface and take it down
again, without disturbing anything else.

**Work.** Add `internal/network/accesspoint.go`. The access point is a
`wpa_supplicant` started with a configuration of its own, written to
`<state>/wpa_supplicant-ap-<interface>.conf`, containing a single network block
in AP mode:

    ctrl_interface=<controlDirectory>
    update_config=0
    country=<regulatory domain, if known>

    network={
        ssid="cue-setup-XXXX"
        mode=2
        frequency=2437
        key_mgmt=WPA-PSK
        proto=RSN
        pairwise=CCMP
        group=CCMP
        psk="<generated passphrase>"
    }

`mode=2` is what makes it an access point. `frequency=2437` is channel 6 in the
2.4 GHz band, chosen because every phone supports 2.4 GHz and because 5 GHz
channel availability depends on the regulatory domain, which a device fresh out
of a box may not know. `proto=RSN` with `CCMP` is WPA2, refusing the older WPA1
and TKIP that some phones now reject outright.

The network name is `cue-` followed by six random characters, generated when
onboarding starts: `cue-4k2p9x`. Random rather than derived from the device
identifier, so that a device sitting in a shop window does not broadcast a
stable name that identifies it to everyone in range for as long as it is
unconfigured, and so that two devices set up in one room cannot collide. The
cost is that a reboot part-way through setup produces a different name, and the
phone's saved entry for the old one is useless — which is acceptable because
the passphrase changes with it anyway, so the person has to rescan regardless.

The passphrase is twelve random characters from an unambiguous alphabet
(no `l`, `1`, `O` or `0`), generated at the same time and kept in memory only.
It does not go into `cue.yaml`: it is worthless once onboarding ends, and
writing it to disk only creates a secret to leak.

The interface signatures to add:

    // AccessPoint is a temporary wireless network this device runs itself so
    // that somebody can set it up from a phone.
    type AccessPoint struct { ... }

    // NewAccessPoint prepares one. It does not touch the radio.
    func NewAccessPoint(store *config.Store, interfaceName string) *AccessPoint

    // Name is the network name a phone will see.
    func (self *AccessPoint) Name() string

    // Passphrase is the password for it, generated when the access point was
    // prepared.
    func (self *AccessPoint) Passphrase() string

    // Start puts the radio into access point mode and returns once the network
    // is being advertised.
    func (self *AccessPoint) Start(ctx context.Context) error

    // Stop takes it down and leaves the radio as it was found.
    func (self *AccessPoint) Stop(ctx context.Context)

    // SupportsAccessPoint reports whether this interface's radio can be an
    // access point at all, so that onboarding is never attempted on hardware
    // that cannot do it.
    func SupportsAccessPoint(interfaceName string) (bool, error)

`SupportsAccessPoint` reads the phy's supported interface modes from
`/sys/class/ieee80211/<phy>/` rather than running `iw` and parsing its output,
because parsing the output of a tool is a thing that breaks when the tool is
upgraded. If the sysfs route proves not to expose modes, fall back to running
`iw phy <phy> info` and looking for a line `* AP` under
`Supported interface modes`, and say so in `Surprises & Discoveries`.

**Acceptance.** A test in `internal/network/accesspoint_test.go` that starts an
access point on a virtual radio and asserts the network is being advertised.
Linux provides `mac80211_hwsim`, a kernel module that creates fake wireless
radios exactly for this. The test skips when the module is not loadable, and
`make docker-test` runs it inside the image where it is. The test must assert
the network is *visible* — by scanning for it from the second virtual radio
`mac80211_hwsim` provides — and not merely that `wpa_supplicant` started, for
the same reason the VNC IPv6 test connects instead of reading the command line.

### Milestone 3 — addresses and names for whoever joins

**Scope.** A phone that joins the temporary network gets an address and has
every name it looks up answered with the device's own address.

**Work.** Add `internal/network/onboarding/dhcp.go` and
`internal/network/onboarding/dns.go`.

The device gives itself `192.168.216.1/24` on the access point interface. That
range is chosen to be unlikely to collide with the network the person is about
to join: the two ranges consumer routers hand out by default must be avoided,
because a phone that ends up on the same subnet on both sides of the switch-over
gets confused about which one to route to. `192.168.216.0/24` is not one any
consumer router picks. The DHCP server hands out `192.168.216.10` to `192.168.216.60`, a
one-hour lease, with the router and the DNS server both set to
`192.168.216.1` — itself. It is built on `dhcpv4/server4` from
`github.com/insomniacslk/dhcp`, already vendored for the DHCP client.

The DNS server listens on `192.168.216.1:53` and answers every A query with
`192.168.216.1` and every other query type with an empty successful answer. It
is written directly against the wire format, which for this purpose is small:
read the question, copy the header with the response bit set, append one answer
record. Anything it cannot parse it ignores.

Both bind only to the access point interface's address, never to `0.0.0.0`, so
that a device with a working network never has a DHCP or DNS server facing it.
Both stop when onboarding stops.

**Acceptance.** Tests that speak to each server over a socket:
`TestAPhoneJoiningIsGivenAnAddress` builds a DHCP DISCOVER and asserts an OFFER
comes back with an address in range, a one-hour lease and the router set to the
device. `TestEveryNameAnswersWithThisDevice` sends a query for
`captive.apple.com` and for a random name and asserts both answer
`192.168.216.1`. Both run on the loopback interface and need no hardware, so
they run everywhere.

### Milestone 4 — the portal

**Scope.** The page the phone opens, and the join that follows.

**Work.** Add `internal/web/portal.go` and register its routes *unauthenticated*
and only while onboarding is running. The routes:

`GET /portal` is the page: a heading, the list of networks from the cached
scan sorted by signal strength with a lock icon for the secured ones, a
password field that appears when a secured network is chosen, a Join button, and
a "Scan again" link. It is a self-contained page, not the main interface, because
the main interface is a signed-in application and this must work with no session.

`POST /api/v1/portal/join` takes `{"ssid": "...", "passphrase": "..."}` and
starts the join described in Milestone 5. It answers immediately with
`{"joining": true}` rather than waiting, because the phone is about to lose the
network it is asking over — the access point goes down as part of joining — and
a request that never gets its answer looks like a failure to the person holding
the phone. The page tells them what is about to happen before it calls this.

`POST /api/v1/portal/scan` re-scans and returns the list.

The captive-portal probes, answered so that phones open the portal:
`GET /hotspot-detect.html` and `/library/test/success.html` (Apple),
`GET /generate_204` and `/gen_204` (Android), `GET /connecttest.txt` and
`/ncsi.txt` (Windows), and a catch-all that redirects any host that is not ours
to `http://192.168.216.1/portal`. Each answers with a 302 to the portal rather
than the success text the phone is looking for; that mismatch is precisely what
makes the phone show the page.

These routes exist only while onboarding runs. Rather than adding and removing
routes from a live router — which `gorilla/mux` does not support and which
would be a race — register them once at startup behind a guard that returns 404
when onboarding is not running. State that reasoning in a comment, because a
reader will otherwise wonder why the portal is always routed.

**Acceptance.** `TestThePortalIsOnlyServedWhileOnboarding` asserts every portal
route answers 404 when onboarding is off and 200 or 302 when it is on.
`TestAPhoneProbeIsRedirectedToThePortal` asserts each of the six probe URLs
answers 302 to the portal. `TestThePortalListsTheNetworksInRange` asserts the
page contains an SSID from a faked scan. All run without hardware.

### Milestone 5 — deciding when to do this, and getting back if it fails

**Scope.** The state machine, the trigger, and recovery.

**Work.** Add `internal/onboarding/onboarding.go` owning the whole flow, started
by `internal/daemon`.

Onboarding starts only when **all** of these are true, checked once at startup
and then every reconcile interval:

  - the configuration has no wireless network configured for any interface, and
  - no interface has a usable address other than loopback, and
  - at least one wireless interface exists whose radio supports AP mode, and
  - onboarding has not been switched off in the configuration.

The last is a new setting, `network.onboarding` (default `true`), which the
Network page must offer a control for or the configuration test will fail.

Onboarding stops as soon as the device has a usable address, and also after
thirty minutes, so that a device left alone does not broadcast a setup network
for ever. If it stops on the timeout with still no network, the screen goes
back to the ordinary welcome page, which now explains that setup timed out and
that restarting the device will offer it again.

The join sequence, which is the part that must not strand the device:

  1. Remember the chosen SSID and passphrase in memory.
  2. Answer the phone, and wait two seconds so the answer actually leaves.
  3. Stop the DNS and DHCP servers and take the access point down.
  4. Write the network into `wpa_supplicant`'s configuration and join it,
     using the existing `network.Join`.
  5. Wait up to forty-five seconds for an address.
  6. If an address arrives, write the interface into `cue.yaml` so it comes
     back after a restart, stop onboarding, and let the welcome page show the
     new address.
  7. If no address arrives, forget that network so `wpa_supplicant` does not
     keep retrying it, bring the access point, DHCP and DNS back up, and set a
     message that the portal shows next time: "That network did not accept the
     password." The phone rejoins `cue-setup-XXXX` on its own, because it is a
     network it has already used.

Step 7 is the reason this milestone exists separately. A join that fails must
end with the device back where it started, not with a radio in an unknown state.

**Acceptance.** `TestOnboardingDoesNotStartOnADeviceThatHasANetwork` and
`TestOnboardingDoesNotStartWithoutHardwareThatSupportsIt` assert the trigger.
`TestAFailedJoinBringsTheSetupNetworkBack` drives the sequence against fakes and
asserts the access point is started again and the message is set. Then, on real
hardware, the end-to-end check in `Validation and Acceptance` below.

### Milestone 6 — write it down

Update `docs/reference/configuration.md` with `network.onboarding`, add a
section to `docs/reference/local-development.md` on testing with
`mac80211_hwsim`, write `docs/decisions/` records for running the access point
from `wpa_supplicant` and for the passphrase-on-screen choice, and add the
`CHANGELOG.md` entry.

## Validation and Acceptance

Run, from the repository root:

    make lint-ci
    go test ./...
    make docker-test
    make docker-smoke

Expect all to pass. `make docker-test` runs the wireless tests inside the image,
where `wpa_supplicant`, `iw` and `mac80211_hwsim` exist.

Then the end-to-end check, which is the real acceptance. On a machine with
wireless hardware and no network configuration — `carbon` is such a machine once
its `network.interfaces` list is emptied — start the daemon and observe:

  - The screen shows a QR code and "Scan to set up this screen".
  - A phone camera pointed at it offers to join `cue-setup-XXXX`, without the
    person typing a network name or a password.
  - Once joined, the phone opens a page headed "Set up this screen" by itself,
    with no address typed.
  - The page lists the wireless networks in the room, strongest first.
  - Choosing one, typing its password and tapping Join makes the screen change,
    within about twenty seconds, to the welcome page showing a real address on
    the chosen network.
  - `ssh` to the machine confirms the wireless interface is associated with the
    chosen network, and `/etc/cue/cue.yaml` now names it.

And the recovery check, which matters as much:

  - Repeat the above but type the password wrongly. Within about forty-five
    seconds the phone can rejoin `cue-setup-XXXX`, and the portal says the
    network did not accept the password. The device is not stranded.

## Idempotence and Recovery

Every step is safe to repeat. Starting an access point that is already running
returns without doing anything. Stopping one that is not running does nothing.
The `wpa_supplicant` configuration for the access point is rewritten each time
because it holds no state worth keeping, unlike the station configuration in
`writeSupplicantConfiguration`, which is deliberately written once and then left
alone because `wpa_supplicant` owns the networks in it.

If the daemon is killed while onboarding is running, the next start finds no
network, no configuration, and AP-capable hardware, and starts onboarding
again. Nothing is left behind on disk except the access point's configuration
file, which is overwritten next time.

The one destructive step is joining a network, which takes the access point
down. Step 7 above is its rollback and must be implemented before this feature
is put on a device.

## Interfaces and Dependencies

Two dependencies, both already vendored on `main`:

`github.com/skip2/go-qrcode` for QR encoding. MIT licensed, pure Go, no
dependencies outside its own subpackages. Used only through
`internal/util/qr`, so it can be replaced without touching anything else.

`github.com/insomniacslk/dhcp/dhcpv4/server4` for the DHCP server. No new
module: `github.com/insomniacslk/dhcp` is already required for the DHCP client
in `internal/network/address.go`.

New packages, with the names they must have at the end:

In `internal/util/qr/qr.go`:

    func Encode(text string) ([][]bool, error)

In `internal/network/accesspoint.go`:

    type AccessPoint struct{ ... }
    func NewAccessPoint(store *config.Store, interfaceName string) *AccessPoint
    func (self *AccessPoint) Name() string
    func (self *AccessPoint) Passphrase() string
    func (self *AccessPoint) Start(ctx context.Context) error
    func (self *AccessPoint) Stop(ctx context.Context)
    func SupportsAccessPoint(interfaceName string) (bool, error)

In `internal/network/onboarding/dhcp.go` and `.../dns.go`:

    func ServeDHCP(ctx context.Context, interfaceName string, self net.IP) error
    func ServeDNS(ctx context.Context, self net.IP) error

In `internal/onboarding/onboarding.go`:

    type Onboarding struct{ ... }
    func New(store *config.Store, manager *network.Manager) *Onboarding
    func (self *Onboarding) Running() bool
    func (self *Onboarding) Network() *AccessPoint
    func (self *Onboarding) Run(ctx context.Context)
    func (self *Onboarding) Join(ssid, passphrase string) error

In `internal/config/configuration.go`, on `Network`:

    // Onboarding lets a device with no network run a temporary wireless
    // network of its own so that somebody can set it up from a phone.
    Onboarding bool `yaml:"onboarding" json:"onboarding"`

## Artifacts and Notes

The feasibility evidence gathered before writing this plan, kept because it is
what the design rests on.

The radio on `carbon`, which is the hardware to design against:

    $ iw list | grep -A 8 'Supported interface modes'
        Supported interface modes:
             * IBSS
             * managed
             * AP
             * AP/VLAN
             * monitor
             * P2P-client
             * P2P-GO
             * P2P-device

The dependency spike, run from a scratch `cmd/spike` that was deleted
afterwards:

    qr err: <nil>
    qr modules: 37 x 37
    dhcp server4 handler type: server4.Handler

The image's package list, showing what is not there:

    ENV PACKAGES="... wpasupplicant iw wireless-regdb ..."
