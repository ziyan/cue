# Signing a page in is a rule that is re-evaluated, not an action performed once

- Status: accepted
- Date: 2026-08-22
- Deciders: Ziyan Zhou

## Context

The case this project exists for: a camera dashboard on a wall, whose session
expires every few hours. When it does, the tab is redirected to
`/login?redirect=/protect/dashboard/...` and sits there — an empty login form
on a wall in an office — until somebody walks over with a keyboard. The device
this was observed on had a browser log repeating a WebRTC timeout against that
login URL every twenty seconds, for hours.

A login performed once when the tab opens does not help with this at all.

## Decision

A playlist item may carry a `login` block, and it is a **rule**, evaluated
against the tab every few seconds for as long as the daemon runs. The rule says
how to recognise that the page needs signing in — a regular expression against
the address, or a CSS selector only the login page has — what to type where,
what to click, and optionally how to know it worked.

Two details that are not obvious and are the difference between working and
not:

- The value is set through the element's **native** value setter and followed
  by `input` and `change` events. Setting `.value` alone is invisible to React,
  Vue and Angular, which track the value themselves, and the form submits
  empty.
- There is a minimum interval between attempts, defaulting to thirty seconds.
  A wrong password submitted in a loop locks the account out, and a locked
  account is much worse than a screen showing a login page.

## Consequences

- The device stores a working password for another system, in its
  configuration file. That file is written 0600, the value never appears in a
  log line or an API response, and `make check-secrets` refuses to let one into
  the repository.
- Recognising the login page is the operator's job and can be got wrong. When
  `expectUrlMatches` is set, a rule that fires but does not work is reported —
  "was signed in but did not reach a page matching …" — which is the difference
  between a wrong selector being diagnosable and being a mystery.
- Nothing here handles a second factor, a CAPTCHA or an OAuth redirect to
  another origin. Those are out of scope and always will be: a screen that
  needs a human to sign it in is not a screen this program can keep running.
