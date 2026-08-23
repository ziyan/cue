# Execution plans

A plan under `docs/planning/active/` is written so that somebody with only this
repository and that one file can carry the work on. That is a high bar and it
is the point: the plan outlives the conversation that produced it.

## What a plan must contain

- **Purpose.** What somebody can do after this work that they could not
  before, and how to see it working. Not a list of code changes.
- **Definitions.** Every term of art defined in plain language, once, where it
  is first used. If you would not use the phrase to somebody who has never seen
  this repository, define it or do not use it.
- **Progress.** A checklist with timestamps, reflecting the actual state. Every
  stopping point is recorded, splitting a half-done item into "done" and
  "remaining" if necessary.
- **Surprises and discoveries.** What turned out not to be true, with evidence.
  This is usually the most valuable section a year later.
- **Decision log.** Every decision, with its reasoning and date. A decision
  that also answers "why on earth is it like this?" gets a record in
  `docs/decisions/` as well.
- **Outcomes and retrospective.** Written at each milestone and at the end.
  What was achieved, what was not, what was learned.
- **Milestones.** Each independently verifiable, each with the commands to run
  and what to expect to see. Acceptance is phrased as behaviour a person can
  check — "the screenshot endpoint returns a 1280x720 PNG" — not as internals.

## How to write one

Prose, not bullet points, except in Progress where the checklist is mandatory.
Name files by their full path. Say what to run and where. Resolve ambiguity in
the plan rather than leaving it to the reader.

Do not point at an external document for anything the reader needs. If
knowledge is required, put it in the plan in your own words, even if that means
repeating yourself.

## Keeping it current

Update it as you go, not at the end. When you change course, say so in the
decision log and reflect it in Progress. When something surprises you, write it
down while you still remember what you expected instead.
