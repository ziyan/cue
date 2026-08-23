# CLAUDE.md

Start with `AGENTS.md`. It covers what this program is, where the code lives,
how a page gets onto the screen, and the invariants that matter.

Then, depending on what you are doing:

- `CONTRIBUTING.md` — naming, comments, commits, changelog, tests
- `docs/reference/local-development.md` — build, test, run it on your own machine
- `docs/reference/configuration.md` — every field of cue.yaml
- `docs/reference/api.md` — the HTTP interface
- `docs/decisions/` — why the architecture is the way it is
- `docs/coding/execplans.md` — how a plan is written here
- `docs/security/security-notes.md` — what is exposed and what protects it
- `docs/planning/active/` — the plan this was built from

## Notes specific to Claude Code

- `make docker-smoke` is the test that matters. It starts the real image
  against a virtual screen and checks that a picture reaches it. Run it after
  anything that touches the X server, the browser or the image.
- The image build takes a few minutes and downloads a few hundred megabytes of
  Debian packages the first time. It is cached afterwards.
- `make lint` runs `mulint`, which is not installed everywhere and is not in
  CI. `make lint-ci` is what CI runs.
- Do not run `gofmt -w .` from the repository root: it reformats `vendor/`.
  `make format` has the right exclusions.
- Never put a real host name, address or credential in a file — not in a test,
  not in an example, not in a comment. Use `example.com` and obvious
  placeholders. `make check-secrets` is the backstop, not the rule.
