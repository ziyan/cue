# Play a video on the screen, as one item of the playlist

This ExecPlan is a living document. The sections `Progress`, `Surprises &
Discoveries`, `Decision Log`, and `Outcomes & Retrospective` must be kept up to
date as work proceeds. It is maintained in accordance with the ExecPlan rules
described in `docs/coding/execplans.md`.

## Purpose / Big Picture

Today a Cue screen shows web pages and nothing else. Everything in the playlist
is a URL, and the only way to put a video on a wall is to find somewhere on the
network to host it and point an item at that. That means a web server somebody
has to maintain, and a screen that shows nothing when it is down. For a promo
loop in a reception area — the obvious reason to want a video on a wall — it is
a lot of machinery for one file.

After this work, an operator opens the Content page, chooses **Add a video**,
picks a file from the laptop they are sitting at, and it uploads to the device.
It becomes an item like any other: it can be reordered, disabled, given a
title. When its turn comes the screen plays it full screen, edge to edge, with
no controls and no browser furniture. When it ends the screen moves to the next
item by itself, rather than sitting on a frozen last frame for the rest of the
rotation interval. Each video can be silent or have its sound, chosen per item,
so one video can play with music without turning sound on for everything.

The file lives on the device's own disk, so the screen keeps playing it with no
network at all. When somebody deletes the item, the file goes too, rather than
filling the disk of a machine nobody logs into.

**How you will see it working.** On the Content page, add a video, choose
`portal-promo-with-music.mp4`, and wait for the upload. The item appears in the
list with its name and length. Within a rotation the screen plays it full
screen; ninety seconds later the screen moves on by itself. Turning **Sound**
on for that item and leaving it off for everything else gives that one video
its music. Deleting the item and looking in `/var/lib/cue/videos` shows the
file gone.

## Definitions

**Playlist.** The ordered list of things the screen shows, one after another,
in `playlist.items` of `/etc/cue/cue.yaml`. Every entry today is a URL. The
code is `internal/browser/playlist.go`.

**Item.** One entry in that list. It has an identifier, a URL, an optional
title, an optional duration, and flags for reloading and for being temporarily
disabled. `config.Item` in `internal/config/configuration.go`.

**Rotation.** Moving from one item to the next when its time is up.
`Browser.rotate` runs the clock; `Browser.currentDuration` says how long the
item on screen should stay.

**Content-addressed storage.** Storing a file under a name derived from a
digest of its own bytes, rather than under the name it arrived with. Two
uploads of the same file become one file on disk, and nothing has to invent
unique names or worry about two items being given the same one.

**Digest.** A short fixed-length value computed from a file's contents, such
that different contents give different values in any practical sense. This plan
uses SHA-256 and keeps the first sixteen bytes of it as hexadecimal.

## Progress

- [x] (2026-08-27 01:20Z) Established that the image's Chromium can play the
      format in question. This was the one thing that could have sunk the
      approach; evidence in `Surprises & Discoveries`.
- [ ] Milestone 1: storing an uploaded video and serving it back.
- [ ] Milestone 2: the page that plays one full screen, and moving on when it ends.
- [ ] Milestone 3: the Content page: adding, naming, sound, deleting.
- [ ] Milestone 4: cleaning up files nothing refers to.
- [ ] Milestone 5: documentation, changelog, decision record.

## Surprises & Discoveries

- Observation: The image's Chromium plays H.264 and AAC, which was not safe to
  assume. Debian builds Chromium without proprietary codecs in some
  configurations, and had this one done so, every ordinary `.mp4` an operator
  owns would have failed to play and the feature would have needed a
  transcoding step — a different and much larger piece of work.

  Evidence: asking that Chromium what it can play, in the image:

      {"h264+aac":"probably","h264 only":"probably","mp4 generic":"maybe",
       "webm vp9":"probably","webm vp8":"probably"}

  `probably` is the strongest answer the browser gives; it never promises.

## Decision Log

- Decision: Files are stored under a digest of their contents, not under the
  name they were uploaded with.

  Rationale: uploaded names are not unique, not safe as paths, and not stable —
  two people upload `promo.mp4` and mean different files. A digest gives a name
  that is unique, safe, and the same for the same bytes, so uploading the same
  file twice costs one copy. It also makes cleanup exact: a file is wanted if
  some item names it, and unwanted otherwise, with no bookkeeping to drift.

  Date/Author: 2026-08-27, Claude (with Ziyan).

- Decision: Sound is per item and off by default.

  Rationale: a wall screen that starts making noise because somebody added a
  video is a bad surprise, and the person who added it may not be in the room.
  Off by default, on deliberately. It is per item because the obvious case —
  one promo with music among several silent dashboards — is impossible with a
  single device-wide setting.

  Date/Author: 2026-08-27, Claude (with Ziyan).

- Decision: A video item moves the playlist on when the video ends, rather than
  after a fixed duration.

  Rationale: a duration would have to be guessed or read out of the file, and
  either way a video whose length changes when it is replaced would leave the
  screen either frozen on a last frame or cutting off early. The page knows
  exactly when it ended and says so.

  Date/Author: 2026-08-27, Claude (with Ziyan).

- Decision: The player page and the video itself are served without a session
  when the request comes from this machine, and require one otherwise.

  Rationale: the browser on the device has no session and must be able to play
  the video, exactly as it fetches `/welcome` today. Anybody else asking is
  asking over the network for a file an operator uploaded, and that should need
  a password like everything else.

  Date/Author: 2026-08-27, Claude (with Ziyan).

## Outcomes & Retrospective

Not started.

## Context and Orientation

Cue is one Go program in a container image with no shell. `internal/daemon` is
the composition root. `internal/browser` drives Chromium over the DevTools
protocol: `playlist.go` opens one tab per enabled item, rotates between them on
a timer, and reloads them when told to. `internal/web` serves the HTTP
interface; its route table is `addRoutes` in `internal/web/web.go`, and most
routes sit behind `requireSession`. `internal/config` holds the configuration
as Go structs, with `config.Item` describing one playlist entry.

Two rules of this repository bear directly on this work. Every field added to
the configuration must have a control in the web interface or be listed in
`deliberatelyNotInTheInterface` with a reason — `TestEverySettingIsReachableFromTheInterface`
enforces it. And every page of the interface is opened in a real browser by
`TestEveryPageRendersWithoutFaulting`, which fails on any exception, so
JavaScript that refers to something that does not exist is caught before it
ships.

Where files may be written: `configuration.Paths.State`, which is
`/var/lib/cue` on a device and a temporary directory in tests. It survives
restarts; `Paths.Runtime` does not.

## Plan of Work

### Milestone 1 — a video on the disk, and served back

**Scope.** Uploading a file and getting it back out again. No playback yet.

**Work.** Add `internal/video/store.go`:

    // Store keeps uploaded videos on the device's own disk.
    type Store struct{ ... }

    // Open prepares the store under the state directory.
    func Open(directory string) (*Store, error)

    // Add streams a video in and returns what it was stored as. The reader is
    // consumed once; nothing is held in memory.
    func (self *Store) Add(name string, source io.Reader) (Video, error)

    // Video is one stored file.
    type Video struct {
        File string // the digest it is stored under
        Name string // what it was called when it arrived
        Size int64
        Type string // its media type, for serving it back
    }

    // Path is where a stored video lives.
    func (self *Store) Path(file string) (string, error)

    // Remove deletes one.
    func (self *Store) Remove(file string) error

    // List is everything stored.
    func (self *Store) List() ([]Video, error)

`Add` streams to a temporary file in the same directory while computing the
digest, then renames it into place — so a failed or abandoned upload never
leaves a half file that looks whole, and the rename is atomic on the same
filesystem. Alongside each `<digest>.<extension>` sits `<digest>.json` holding
the original name, size and media type, because the digest alone says nothing a
person could read.

The upload route is `POST /api/v1/videos`, behind a session, reading the body
as a stream and not with `ParseMultipartForm` — that buffers to memory or to
disk of its own choosing, and a 4 GB video is not a thing to buffer. It refuses
anything whose media type is not a video, and anything larger than a limit that
is a new setting, `playlist.maximumVideoSize`, defaulting to 4 GiB.

Serving is `GET /videos/{file}` using `http.ServeContent`, which handles range
requests — a browser seeking in a video asks for byte ranges, and without them
some seeks fail. It is served without a session to requests from this machine
and with one otherwise, as decided above.

**Acceptance.** `go test ./internal/video/` with tests that a file added comes
back byte for byte, that adding the same bytes twice stores one file, that a
half-finished upload leaves nothing behind, and that `Path` refuses a file name
containing a slash or dots — a store addressed by a name from a request must
never be able to hand back `/etc/shadow`. Then, with the daemon running:

    curl -u ... -F file=@video.mp4 http://127.0.0.1:8080/api/v1/videos
    curl -sI http://127.0.0.1:8080/videos/<file returned above>

expecting `200`, the right `Content-Type`, and `Accept-Ranges: bytes`.

### Milestone 2 — playing it, and moving on when it ends

**Scope.** The screen plays the video full screen and the playlist advances
when it finishes.

**Work.** `config.Item` gains:

    // Video, when set, makes this item a video rather than a web page. The
    // file is one the operator uploaded; URL is ignored.
    Video *ItemVideo `yaml:"video,omitempty" json:"video"`

    type ItemVideo struct {
        File  string `yaml:"file" json:"file"`
        Name  string `yaml:"name,omitempty" json:"name"`
        Sound bool   `yaml:"sound,omitempty" json:"sound"`
    }

`Browser.plannedItems` gives a video item the URL of the daemon's own player
page, `http://127.0.0.1:<port>/play/<identifier>`, in the same way the holding
page is chosen today.

`GET /play/{item}` renders a page that is one `<video>` filling the viewport,
with `autoplay`, `playsinline`, `muted` unless the item asks for sound, and no
`controls`. The page background is black and the video is `object-fit: contain`,
so a video whose shape does not match the screen gets bars rather than being
stretched or cropped.

When the video ends the page asks the daemon to move on:
`POST /api/v1/playlist/next`, served to this machine without a session. The
same call is made if the video fails to load, after saying so on the page for a
few seconds — a screen stuck for ever on a video that will not play is worse
than one that moves on.

`Browser.currentDuration` returns zero for a video item, so the rotation clock
does not also move it on. A video that never fires either event would then
hold the screen for ever, so the page sets its own backstop: a timer for the
video's duration plus thirty seconds, or five minutes if the duration is not
known, after which it asks to move on regardless.

Autoplay with sound is refused by browsers unless the page has been interacted
with. Chromium is already started with `--autoplay-policy=no-user-gesture-required`
(see `internal/browser/arguments.go`), which is exactly this case and means no
work is needed — but it must be checked rather than assumed, because if it were
ever removed, videos with sound would silently not start.

**Acceptance.** A test that `plannedItems` gives a video item the player URL. A
test that the player page contains a `<video>` with `muted` when sound is off
and without it when on. Then on a device: add a video, watch it play full
screen with no controls, and see the screen move on within a second of the
video ending.

### Milestone 3 — the Content page

**Scope.** Adding, naming, choosing sound, and deleting, from the interface.

**Work.** In `internal/web/static/pages/content.js`, an **Add a video** button
beside the existing add. It opens a file chooser, uploads with `XMLHttpRequest`
so that progress can be shown — `fetch` cannot report upload progress — and
shows a bar, because a 66 MB upload over wireless is long enough that a person
will otherwise think it has hung. On success it appends an item with the
returned file and name.

A video item's editor shows its name and size, a **Sound** switch, and the
ordinary title, disabled and reorder controls. It does not show the URL field,
the reload switch, the login rules or the dismiss rules: none of them mean
anything for a video, and showing controls that do nothing is worse than not
showing them.

**Acceptance.** `TestEveryPageRendersWithoutFaulting` passes, which proves the
new JavaScript at least runs. A test that the interface mentions the new
settings, which `TestEverySettingIsReachableFromTheInterface` will demand
anyway. And by hand: upload the file, see the progress bar move, see the item
appear.

### Milestone 4 — not filling the disk

**Scope.** Files nothing refers to are deleted.

**Work.** `internal/video.Store.Sweep(wanted []string) ([]string, error)`
deletes every stored file not in `wanted` and returns what it deleted. The
daemon calls it at startup and after every accepted configuration change, with
the files named by the current playlist.

It deletes only files it can see are unwanted, and it never deletes a file
written in the last few minutes: an upload that has finished but whose item has
not been saved yet is exactly a file nothing refers to, and deleting it would
make uploading and then saving lose the video every time.

**Acceptance.** A test that a store with three files and two wanted deletes the
third, that it keeps a recently written file even when unwanted, and that it
returns the names it deleted so the log can say what went. On a device: delete
the item, then look in `/var/lib/cue/videos`.

### Milestone 5 — write it down

`docs/reference/configuration.md` for the new fields, a `CHANGELOG.md` entry,
and a decision record for content-addressed storage.

## Validation and Acceptance

From the repository root:

    go test ./...
    make docker-test
    make docker-smoke

Then, on a device with a screen: add `portal-promo-with-music.mp4` through the
Content page, watch the upload progress finish, and watch the screen. Expect
the video full screen with no controls and no scrollbars, expect the screen to
move to the next item within a second of the end, expect silence with **Sound**
off and music with it on, and expect the file to disappear from
`/var/lib/cue/videos` when the item is deleted.

## Idempotence and Recovery

Uploading the same file twice stores one copy and is otherwise harmless.
Sweeping is safe to run at any time and does nothing when everything is wanted.
An upload interrupted half way leaves a temporary file that the next sweep
removes. Deleting a file that an item still names leaves that item showing an
error page rather than a black screen, and the page moves the playlist on.

## Interfaces and Dependencies

No new dependencies. Everything here is the standard library: `crypto/sha256`,
`io`, `mime`, `net/http` and `os`.

In `internal/video/store.go`:

    type Video struct{ File, Name, Type string; Size int64 }
    func Open(directory string) (*Store, error)
    func (self *Store) Add(name string, source io.Reader) (Video, error)
    func (self *Store) Path(file string) (string, error)
    func (self *Store) Remove(file string) error
    func (self *Store) List() ([]Video, error)
    func (self *Store) Sweep(wanted []string) ([]string, error)

In `internal/config/configuration.go`, on `Item`:

    Video *ItemVideo `yaml:"video,omitempty" json:"video"`

    type ItemVideo struct {
        File  string `yaml:"file" json:"file"`
        Name  string `yaml:"name,omitempty" json:"name"`
        Sound bool   `yaml:"sound,omitempty" json:"sound"`
    }

## Artifacts and Notes

The file this was designed against, which is the one to test with:

    portal-promo-with-music.mp4
    h264 1920x1080, aac, 90 seconds, 66 MB
