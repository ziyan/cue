# Security policy

## Reporting a vulnerability

Report it privately, through GitHub:
[open a draft advisory](https://github.com/ziyan/cue/security/advisories/new).
Private vulnerability reporting is enabled on this repository, so the report
stays between you and the maintainer until there is a fix to publish.

Please do not open a public issue for a vulnerability, and please do not put the
details in a pull request. Both are visible to everybody the moment they exist,
including on a project whose devices are on other people's networks.

This is a small project maintained by one person. There is no response-time
commitment, because one that could not be kept would be worse than none. Reports
are read.

## What is useful in a report

The most useful report says what an attacker can reach and from where, because
that is the part this project's own documentation is organised around. In
particular:

- Which surface — the web interface on port 8080, the setup network a device
  runs before it is configured, the VNC server, or the websocket tunnel to a
  hosted service.
- Whether it needs the administrator's password, a screen pass, a link
  credential, or nothing at all.
- What it gets: reading the configuration, changing what a screen shows,
  driving the screen, or the credentials for the systems on it.

`docs/security/security-notes.md` describes each of those surfaces, what
protects it, and what is deliberately left unprotected. Reading it first will
tell you whether something is a finding or a documented decision — and if it is
a documented decision you disagree with, that is worth reporting too, as a
disagreement rather than as a vulnerability.

## Supported versions

The latest release. This project has not reached 1.0 and does not backport: a
fix goes into the next release, and upgrading is how a device gets it.

Devices upgrade themselves when `upgrade.allowApply` is set, and otherwise by
being redeployed. A device that cannot reach the internet does neither, which is
a supported way to run one and means somebody has to carry the fix to it.

## What is in scope

The daemon in this repository and the container image built from it. That
includes what a device exposes on its network, what it accepts from a hosted
service it is linked to, and what it stores on disk.

Out of scope, in the sense that a report will be read but is not this project's
to fix:

- The pages a device is configured to display. Cue renders whatever it is
  pointed at, in a browser, full screen. A hostile dashboard is a hostile
  dashboard.
- Vendored dependencies, except where cue uses one unsafely. Upgrade paths for
  those are watched by `govulncheck` in CI and proposed by Dependabot.
- Anything reachable only from inside the container, or only by somebody who
  already has the machine. Both are stated as accepted in the security notes,
  with the reasoning; `/etc/cue/cue.yaml` holds the same credentials anyway.

## Credentials, if you find one

If a real credential ever appears in this repository — in a file, an example, a
test, or the history — report it privately rather than opening an issue, and say
where you found it. `make check-secrets` and a gitleaks workflow both scan for
this, and a leak that got past both is worth knowing about quickly.
