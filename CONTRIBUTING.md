# Contributing to Cue

Cue runs on machines nobody visits. A fault here is not an exception in a log
somebody reads: it is a screen in a lobby showing a login form for three weeks,
and the first anybody hears of it is a photograph in a message. That shapes
most of what follows.

Start with `docs/reference/local-development.md` to get a build.

## Before you send a change

    make format
    make lint          # golangci-lint, then mulint if you have it
    make test
    make docker-smoke  # starts the real image and proves a picture reaches the screen

CI runs `make lint-ci`, which is golangci-lint and the secret scan. `mulint`
enforces this project's naming conventions and is local-only, so a contributor
without it is never blocked; run it anyway if you have it.

## Naming

These are not negotiable, and `mulint` checks most of them.

- **Acronyms follow the first letter.** If the identifier starts with a
  capital, acronyms are fully capitalised: `ReferenceURI`, `SessionID`,
  `WhenURLMatches`, `VNCAddress`. If it starts lowercase, only the first letter
  of the acronym is capitalised: `referenceUri`, `sessionId`, `whenUrlMatches`.
  Register a new acronym in `mulint.yaml` with a comment saying what it stands
  for.
- **Do not abbreviate.** `command`, not `cmd`. `response`, not `resp`.
  `request`, not `req`. `processId`, not `pid`. Go package names are the
  exception and should be short.
- **No single-letter variables.** `err` is the one blessed short name, and Go
  errors should be called `err` wherever possible.
- **Struct receivers are `self`.** Consistently, across the whole codebase.
  `.golangci.yml` disables the linters that would object.
- **Name the same thing the same way everywhere.** If it is an `output` in the
  display package it is not a `screen` in the interface.

## Comments

Explain **why**, not what. The what is in the code underneath.

A comment earns its place when it records something the reader cannot recover
from the code: a protocol requirement, a failure that was actually observed, a
deliberate choice among several reasonable ones. `// increment the counter`
above `counter++` is noise. `// Setting .value alone is invisible to React,
which would submit an empty form` is worth having, and is why the login rule
works at all.

This codebase leans long in its comments on purpose. Most of what is hard here
is hard because of something outside it — how the X server compiles a keymap,
what Chromium does with a profile it thinks crashed, how a dashboard expires a
session — and none of that can be recovered by reading the code.

Exported identifiers get a doc comment starting with their name.

## Invariants

Break any of these and something downstream fails in a way that is hard to
trace back.

- **Never commit a credential, an address or a name from a real deployment.**
  `make check-secrets` scans every tracked file and CI runs it. Examples use
  `example.com` and obvious placeholders. This project was written against a
  real device whose password came with the feature request; that password
  exists only in that device's own `/etc/cue/cue.yaml`.

  A test or an example needs *some* string, so the rule is that it has to say
  so: any value containing the word `test`, `example`, `fake`, `dummy`,
  `sample` or `placeholder` is accepted. `"a long test password"` passes;
  `"a good long password"` does not, and that is deliberate — a real
  credential never announces itself.
- **The configuration file is the source of truth.** Anything an operator sets
  lives in `internal/config`. Do not add a second place.
- **Configuration identifiers are stable.** A playlist item's `identifier` is
  generated once and never changes, because the browser's tab bookkeeping and
  the interface's editing both refer to it.
- **Nothing in this project is a shell script.** Not at build time, not at run
  time. A helper that is genuinely needed is a Go program under `tools/`.
- **Nothing compiled is committed.** `go build ./tools/changelog` writes its
  binary into the working directory under that name, and one of them was
  committed here by a `git add -A` that nobody read. `make check-secrets`
  refuses a tracked ELF file, and the Makefile builds into `build/`.
- **Stop a process group, not a process.** Chromium is a dozen processes;
  signalling only its root leaves renderers holding the graphics device open,
  and the next browser cannot start.
- **Suspend the watchdog around every deliberate restart.** Otherwise a planned
  restart counts as a fault and escalates into restarting the graphics.
- **A secret never leaves the configuration file.** `Secret` renders as a
  placeholder in every log line and every JSON response, and
  `config.RestoreSecrets` is what stops saving a form from erasing the password
  it was never shown.
- **The X server is stopped with SIGTERM first.** Killed outright it leaves the
  graphics hardware in a state its successor cannot recover from, which on a
  machine nobody can reach means a power cycle.

## Tests

Write the test that would have caught the bug. A test that only proves the code
runs is not worth the maintenance.

The tests here drive real processes where the thing being tested is a real
process — signals, process groups, exit codes — because a fake would not have
the part that goes wrong. `internal/supervise` re-executes the test binary as a
helper rather than depending on a shell, since the runtime image has none.

`make docker-smoke` is the end-to-end test: it starts the actual image against
a virtual screen and checks that a picture of the right size reaches it, that
the playlist rotates, and that the watchdog is satisfied. Run it after anything
that touches the X server, the browser or the image.

**A unit test must not reach the network.**

## Commits

Write the message for somebody trying to understand this change a year from
now with no memory of the conversation that produced it.

- Subject in the imperative, under about 72 characters.
- The body explains why the change was needed and what it costs. Describing
  what the diff does is redundant; the diff is right there.
- Note anything an operator has to do by hand when upgrading.

## Changelog

A change that somebody running a screen can observe gets an entry. There are
two places to write one and both count, because there are two ways work
arrives here.

**In a pull request description**, under a `## Changelog` heading, using the
Keep a Changelog headings. That is where it is reviewed alongside the change it
describes, and a workflow refuses a pull request that has neither an entry nor
a `skip-changelog` label. The template says the rest.

**In `CHANGELOG.md` under Unreleased**, for a change committed straight to
main. This is how the file has been written since the beginning, and it stays
open: an entry written here can say more than a line in a description, and
sometimes a change deserves that.

Both are collected when a release is cut, and the hand-written ones come first.
Internal work an operator cannot observe -- a refactor, a test, a comment --
needs no entry either way.

## Releases

Releases cut themselves. Every push to `main` that leaves something under
Unreleased becomes one; a push that leaves nothing there does nothing, which is
most of them. There is no separate step to remember, which is the point: a
release process somebody has to remember is one that happens once and then
stops.

The entries come from the pull requests merged since the last tag and from
whatever is under Unreleased in the file. What the version becomes is read off
them, by the plain reading of semantic versioning:

- **Removed** is a major bump. Something that was there is not any more, and
  anybody relying on it is broken by upgrading. Before 1.0.0 this is softened
  to a minor, so that a removal is not the thing that declares the project
  stable.
- **Added** is a minor bump: something new, and everything old still works.
- Everything else — Fixed, Changed, Security, Deprecated — is a patch.

To see what would happen without changing anything:

    go run ./tools/cut

That prints the version it would choose and why. `--write` dates the section
and leaves an empty Unreleased above it; the workflow does that and then
commits and tags.

A major release outside those rules is the "Major release" workflow, run by
hand, which asks whoever runs it to type MAJOR. Deciding that a change breaks
everybody using the thing is a judgement, not a rule.

The tag is what does the work. Pushing `vX.Y.Z` — by the workflow or by hand —
builds the binaries for both architectures, publishes the GitHub release with
the changelog section as its notes, and pushes the image to `ghcr.io`. That
path is exercised by every automatic release, so the manual one is never
untested.

Publishing needs a `RELEASE_TOKEN` secret with permission to push to `main` and
to create tags. The token that Actions provides by default cannot be used: a
push made with it does not start another workflow, and starting the publishing
workflow is the whole purpose of the tag.

## Decisions

If your change makes a choice a future reader would question, write a decision
record in `docs/decisions/` — see the README there. Larger work gets a plan in
`docs/planning/active/` first, moved to `done/` when it lands.

## Reporting a security problem

Do not open a public issue for anything that would let somebody watch or drive
a screen they should not. Mail it to the address in the repository's profile.
