# Profiles and playlists, the device half

## Purpose

Today a screen is configured one screen at a time. Every setting is edited on
that device, through its own web interface or by cue.sh writing to its
configuration over the tunnel, so forty screens that ought to be alike are
forty edits and nothing that can say whether they still match. What they show
is worse: the playlist lives in the same file, so changing what a wall shows
means visiting every machine on it.

After this work, an operator sets a profile and a playlist on cue.sh, and this
device converges on them by itself — including a device that was switched off
for a week, which converges when it comes back rather than having missed
something nobody recorded. Media plays from the device's own disk, so a screen
whose uplink is down goes on showing what it was showing.

To see it working: apply a profile on cue.sh that sets `browser.darkMode`, and
watch this device's `/etc/cue/cue.yaml` gain that setting within one poll
without anything else in the file changing. Remove the setting from the
profile, and watch the line disappear again. Assign a playlist with a video,
pull the network cable, and watch the video go on playing.

This is the device half of a design whose other half is in the cue.sh
repository at `docs/planning/active/20260901-profiles-and-playlists.md`. That
document has the full rationale for the direction of travel. Everything this
plan needs from it is restated here.

## Definitions

**Profile.** A set of settings held on cue.sh and applied to any number of
devices. It is *sparse*: it holds only settings somebody chose, in the same
shape as this device's own configuration file, which `internal/config/prune.go`
already writes sparsely for the same reason — so that a reader can tell a
decision from a default.

**Overlay, and the merge rule.** The profile is applied over the device's own
configuration: **it wins where it speaks and is silent elsewhere.** A setting
it names is authoritative, so local drift is corrected at the next poll. A
setting it does not name is left exactly as the device has it, which is what
lets one profile be applied to forty machines without knowing that each has its
own name, its own network and its own identity.

**Managed key set.** The set of configuration keys this device last took from a
profile, written into its own configuration file as `service.profileKeys`.
Without it nothing can ever be un-managed: the merge is destructive, so once a
profile's value is written into the file the previous value is gone, and a
device would keep it for ever.

**Release.** A key that is in the managed key set and is no longer in the
profile is *released*: removed from the local file, so the device falls back to
its own default. That is exactly what `prune.go` already means by a setting
nobody chose. Unapplying a profile entirely is the same operation with an empty
profile.

**Slide.** What cue.sh calls one thing to show. It is this device's
`config.Item`, field for field. The name differs deliberately and it is the
only name that does: `Item` says nothing as a top-level entity beside devices
and profiles, while `Slide` says nothing useful inside a playlist in a
configuration file.

**Media identifier, and digest.** cue.sh names an uploaded file by a ULID it
mints. It also records a SHA-256 digest of the bytes. This device stores media
under the *digest*, as `internal/media` already does, and uses the identifier
only as the handle to fetch with.

**Nudge.** A `POST /api/v1/poll` that cue.sh sends to this device over the
tunnel when it has changed something, so that a change lands at once rather
than at the next poll. It is best effort in the strict sense: correctness is
entirely in the poll, and a nudge that never arrives costs one interval.

## The contract

Three routes cue.sh serves, which this device calls over the tunnel it already
holds. It speaks HTTP to `cue:80` today — that is how it posts screenshots and
state — and these are on the same router. The device is on the request context
because the websocket verified its signed secret, so none of them takes a
device identifier and none can be asked about somebody else's screen.

    GET /api/v1/device/profile          the settings this device should have
    GET /api/v1/device/playlist         what it should show
    GET /api/v1/device/media/<mediaId>  one file, streamed through cue.sh

`GET /api/v1/device/profile` is live as of cue.sh commit `014dddc` and behaves
as follows. The playlist route will match it.

| Answer | Meaning |
| --- | --- |
| `200` + settings document, `ETag: "<32 hex>"` | the profile for this device |
| `200` + `{}` (literally two bytes) | no profile, or an empty one |
| `304`, no body | the `If-None-Match` matched |
| `401` | no device on the context |

`Content-Type: application/json; charset=utf-8`, `Cache-Control: no-store`.

The ETag is a digest of the *answer*, not of the profile, so reassigning a
device between two profiles with identical settings does not move the version —
correctly, since nothing about this device's configuration changes — while
unapplying does, because the document becomes `{}`.

One route this device serves, which cue.sh calls over `device:80`:

    POST /api/v1/poll                   poll now, best effort

## Milestones

Each is independently verifiable. Run everything from the repository root.

### Milestone 1 — the device polls and merges

`internal/service` gains a poller that asks for the profile on an interval with
`If-None-Match`, and applies the overlay to the configuration through
`config.Store.Update`, exactly as the web interface's own writes do, so that
the file is written atomically and every existing watcher sees the change.

Acceptance: with a stub service answering a document that sets
`browser.darkMode: true`, the device's configuration file gains that line
within one interval, the browser restarts because `restartNeeded` says a
command-line setting changed, and nothing else in the file moves — in
particular `device.name`, `device.identifier` and any configured network are
untouched.

    go test ./internal/service/ -run Profile

### Milestone 2 — releasing

The managed key set is written as `service.profileKeys`, a sorted list of
dotted key paths. A key in it that the profile no longer names is removed from
the file.

Acceptance: apply a profile setting two keys, then a profile setting one of
them, and the other is gone from the file and back to its default. Then apply
`{}` and both are gone. A failed request in the middle changes nothing at all.

    go test ./internal/service/ -run Release

### Milestone 3 — the nudge

`POST /api/v1/poll` on the device's own router, reachable only over the tunnel
from cue.sh, which makes the poller run now.

Acceptance: a nudge over a stub tunnel causes a poll within a second; a nudge
that arrives while a poll is already running does not start a second one.

    go test ./internal/web/ -run Poll

### Milestone 4 — the playlist

The same polling treatment for `GET /api/v1/device/playlist`, mapping each
slide to a `config.Item` field for field, including `identifier` — which is
load-bearing, because `internal/browser/playlist.go` keys browser tabs by it.

Acceptance: a playlist of two slides appears in the configuration, the screen
shows the first, and a second poll returning the same document changes nothing
and does not disturb the browser.

    go test ./internal/service/ -run Playlist

### Milestone 5 — media, fetched once and played from disk

A slide's media is fetched only when the device does not already hold those
bytes, verified against the digest on arrival, and stored under the digest by
`internal/media`.

Acceptance: a playlist whose slide has media causes exactly one fetch; a second
poll of the same playlist causes none; a playlist that reorders its slides
causes none; and with the tunnel torn down the video still plays.

    go test ./internal/service/ -run Media
    make docker-smoke

### Milestone 6 — eviction

Nothing to build. `Daemon.sweepUploads` already removes uploads no item points
at, and already runs on every configuration change. This milestone is a test
that proves it covers media that arrived from a playlist, so that the property
is not lost later.

    go test ./internal/daemon/ -run Sweep

## Decision log

**2026-09-01 — the device stores media under the digest, not under cue.sh's
identifier.** cue.sh names media by a ULID it mints, and proposed the device
store it under that. `internal/media` is content-addressed today, which gives
for free a property the identifier scheme gives up: two uploads of identical
bytes are two identifiers but one file on the device. Disk matters more on a
screen than on a server. So the playlist carries both, the device asks "do I
have this digest?" locally — no round trip, which was the property cue.sh
wanted — and fetches by identifier only on a miss. `config.ItemMedia.File`
keeps one meaning instead of holding a ULID for pulled media and a digest for
locally uploaded media. Accepted by cue.sh.

**2026-09-01 — a profile may never carry identity, credentials or network, and
the device refuses as well as cue.sh stripping.** cue.sh extracts a profile
from a device by stripping the per-device parts. If that list is ever wrong,
applying the result to forty screens gives them all one `device.identifier` —
which is cue.sh's own primary key for a device, so forty screens become one.
The refusal list, enforced here: `device.identifier`, the whole `service`
section, `web.passwordHash`, `web.sessionSecret`, `network.*`, `paths.*`. A key
on it is ignored in a profile and never enters the managed key set.

**2026-09-01 — profiles never carry secrets.** Every `config.Secret`
serialises to `********` in JSON. A code path that wrote that back into the
file took a device off its wireless network this week, because the literal
string became the passphrase. Any profile lifted from a device through a JSON
path would contain it. The refusal list above is what makes this true rather
than hoped for.

**2026-09-01 — the managed key set is a set of keys, not a map of key to the
value it replaced.** Restoring previous values resurrects exactly the drift the
profile abolished: unapply across forty screens and each returns to whatever it
had drifted to, so one action gives forty different states. It also contradicts
`prune.go`, where absence already means "nobody chose", and a stored value goes
stale against a schema that has moved while a key path cannot. A set also
cannot hold a secret.

**2026-09-01 — the key set lives in the configuration file, not beside it.** It
must be atomically consistent with the values it describes; the store writes
the file through `internal/util/atomicfile`, so one write keeps values and
provenance in step, where two files can desynchronise into exactly the
un-releasable state this exists to prevent. It also has to survive what the
file survives: `/etc/cue/cue.yaml` and `/var/lib/cue` are different mounts, and
restoring the configuration alone — an ordinary thing to do — would strip the
provenance and freeze the profile's values for ever.

**2026-09-01 — the list is written sorted, and that is a requirement.**
`internal/web/api.go`'s `versionOf` hashes the marshalled YAML to make the
configuration's ETag. An unsorted sequence that reordered between writes would
move the version on every poll, and the visible symptom would be conditional
writes from the local web interface failing with `409` against a document
nobody edited.

**2026-09-01 — the poll interval is `service.pollInterval`, a setting, and is
on the refusal list.** Operators tune intervals here — `display.reconcileInterval`,
`network.reconcileInterval`, `watchdog.interval` — so a constant would be out
of character. But a profile that set it badly would stop the fleet polling, and
the only fix would be per-screen by hand, which is the situation profiles exist
to abolish. It also sits inside `service`, which the refusal list already
covers, so it needs no rule of its own. Clamped on read, the way
`network.Run` already falls back when its interval is not positive.

**2026-09-01 — `401` means change nothing, not release everything.** It is a
successful HTTP conversation, so it does not obviously fall under "a failed
request changes nothing". Only an explicit `200` with `{}` releases. Otherwise
revoking a device, or any window where the tunnel has no device on the context,
would silently revert every managed setting on that screen — forty settings
going back to defaults on a wall, from an authentication blip. Releasing has to
be something cue.sh says on purpose, never something the device infers from not
being recognised.

**2026-09-01 — a profile may carry the network settings but never the
interface list.** Asked for so that an operator can change the network on many
devices at once. The scalars are safe and are allowed: `network.manage`,
`network.onboarding`, `network.lostAfter`, `network.reconcileInterval` carry no
credential and are the same sentence on every machine.

`network.interfaces` is refused, and the reason is the merge rather than the
field. A list is managed whole, so a profile naming it replaces the device's
list outright — and a wireless passphrase lives inside an interface and never
travels, because it serialises as `********`. A profile moving an office to a
new SSID would therefore leave every screen it touched with an SSID and no key,
unable to join, and the thing that would let somebody fix that remotely is the
network it has just lost. Forty screens is forty site visits.

It is worse than the wireless case alone: replacing a list does not care what
is in it, so a profile carrying only a wired interface still deletes the
wireless one from every device that had it. There is no subset of interfaces
that is safe to send while the merge replaces the list. Nor can carrying the
passphrase rescue it — a new SSID needs a new key by definition, so a profile
that moves the SSID and cannot carry the key has disconnection as its only
possible outcome.

Allowing it later means merging by name and keeping per-device fields the
profile does not mention, as `RestoreSecrets` already matches slices on
`Identifier` or `Name` rather than on position. That is its own design, with
its own question — what becomes of an interface the device has and the profile
does not name? — and should not arrive as a side effect of "the network
section can be managed".

**2026-09-01 — a playlist's login credentials stay on the device.**
`playlist.items[].login.password` is a `config.Secret`, so a playlist lifted off
a device through a JSON path would carry `********` and set the literal mask as
the password on every screen it reached. The profile refusal list does not
help, because a playlist genuinely needs a credential where a profile does not.
Of the three options cue.sh raised, "the extractor drops it and the operator
retypes it once per playlist" is not a third option at all — retyping it into
the playlist means cue.sh stores it, which is the option we both argue against,
reached by a route nobody labelled. So the credential stays here and the slide
references it by name. The selectors travel, because they are editorial and
identical across screens; the username and password do not. A slide naming a
credential this device does not hold must fail loudly rather than attempt an
empty password, which is how an account gets locked out — `Login.MinimumInterval`
exists because that has been thought about once already. The inline pair keeps
working, because devices in service have playlists with passwords in them
today; the reference wins when both are set, and the extractor refuses to emit
inline.

**2026-09-01 — an empty profile and no profile are the same answer.** The
device cannot do anything differently between them: both mean release the
managed set and take nothing new, which is one code path with one outcome. A
distinction the device cannot act on is one that eventually gets acted on
wrongly. Whether an operator sees "managed by an empty profile" as different
from "not managed" is a fact about the assignment, which cue.sh holds.

## Surprises and discoveries

**2026-09-01 — a device whose identifier was regenerated is not necessarily
broken by that, and a 401 is not necessarily about the identifier.** Micro came
back from a deploy refusing to attach with `401`, and its `service.deviceId`
(upper case, from when it linked) disagreed with its freshly minted
`device.identifier`. The obvious story — the identifier change broke the link —
was wrong, and worth not having told: the tunnel handshake sends only
`Authorization: Bearer <credential>` and no identifier at all, so a 401 can only
ever be about the credential. The real cause was on the service, and was
neither half's bug: the account that secret names had been deleted, so creating
the device row failed a foreign key. The remedy is relinking.

What was true is the smaller thing: a device carrying two names for itself
would register as a new screen on its next link. The regenerate rule is the
right one for an identifier that cannot be salvaged and the wrong one for a
device that is already linked, and only a device linked before the rule existed
is exposed.

**2026-09-01 — the service normalises identifiers to lower case, not upper.**
Recorded because this plan's author asserted the opposite in conversation more
than once, from reading a test that has since changed. The reasoning that
depended on it survives — a device that changes the case of its identifier is
still the same device, because the service normalises — but the direction was
wrong, and the direction is what somebody would check.

**2026-09-01 — the eviction stage did not need to exist.** The design gave the
device a stage for removing media nothing points at any more.
`Daemon.sweepUploads` has done that since before this work, and since earlier
today it runs on every configuration change rather than on monitor changes, so
a pulled playlist gets it for free. Removed from both plans.

**2026-09-01 — releasing a setting decoded it to Go's zero value, not to the
device's default.** The merge writes the tree back into a `Configuration`, and
the first version decoded onto a zero value. A released key is *absent* from
the tree, so it came back as `false` or `0` rather than as this version's
default: unapplying a profile that had once set `browser.darkMode` left the
screen light instead of returning it to the default. Absent means "the device's
own default" everywhere else in the file and had to mean it here. Decoding onto
`Default()` fixes it, and the test for releasing is what found it — it failed
on the first run, which is the only reason it is written down rather than
shipped.

**2026-09-01 — `config.ItemMedia` is four fields, not one.** The contract
listed media as a single reference. `Kind` decides whether an item holds the
screen until a video ends or rotates on the ordinary timer, and `Sound` is per
item on purpose, because one promotional video with audio among silent
dashboards cannot be expressed device-wide. Had this shipped as written, every
pulled video would have played silently and some would have held the screen
wrongly.

## Progress

- [x] **2026-09-01** — the merge itself: `internal/config/profile.go`, the
  refusal list, releasing, and eleven tests. `service.pollInterval` and
  `service.profileKeys` added to the schema.
- [x] **2026-09-01** — Milestone 1, the device polls and merges. Poller in
  `internal/service/profile.go`, conditional requests, written through
  `store.Update`. Four tests against a stub of the service.
- [x] **2026-09-01** — Milestone 2, releasing. Proved end to end on carbon
  against the real cue.sh, not only against the stub.
- [x] **2026-09-01** — Milestone 3, the nudge. `POST /api/v1/poll` on the
  service-facing allow-list.
- [ ] Milestone 4 — the playlist
- [ ] Milestone 5 — media, fetched once and played from disk
- [ ] Milestone 6 — eviction, proved rather than built

## Outcomes and retrospective

**2026-09-01, milestones 1 to 3.** A profile applied on cue.sh reaches a real
device and changes it, and a setting removed from that profile is released back
to the device's own default. Watched on carbon against the deployed cue.sh
rather than against a stub.

The evidence, from cue.sh's access log and carbon's own log together. cue.sh
applied a profile setting `browser.darkMode: false` at 17:28:58.814; carbon
fetched it 386ms later and had written its own file at 13:28:59.210 local,
restarting the browser 70ms after that. cue.sh then removed the setting,
leaving the profile assigned, so the document became `{}`; carbon fetched that
439ms later and had applied it 15ms after cue.sh's own log line.

What was released is the part worth recording. `browser.darkMode` is *absent*
from `/etc/cue/cue.yaml`, not set back to `true`. Had it come back as a value,
the device would be asserting a choice nobody made, and it would have frozen
there on the day the default changed. `forceDarkContent` and
`ignoreCertificateErrors`, which are carbon's own settings from before any of
this, were untouched throughout — the "silent elsewhere" half holding while the
"wins where it speaks" half released.

The nudge works, and proving it took more than it should have. cue.sh's first
measurement — 386ms from applying to fetching — was real but not conclusive,
because a scheduled poll was due at almost the same moment. It was settled on
the second step: a fetch twelve seconds after a scheduled poll had just
returned `304` is not a coincidence. Nothing on the device logged an arriving
nudge at all, which is why the argument had to be made from the service's side;
there is now a debug line in the handler, because a screen that is not
converging is debugged from the screen.

Two of the four poller tests passed for the wrong reason when first written:
`browser.darkMode` defaults to `true`, so a stub serving `true` was applied and
proved nothing, and the wait for it succeeded before anything had happened.
Both now start from a value the profile actually changes. Worth assuming there
are others of that shape not yet found.
