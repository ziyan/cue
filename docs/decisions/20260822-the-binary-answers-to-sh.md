# The cue binary answers to the name sh, with a shell that is not one

- Status: accepted
- Date: 2026-08-22
- Deciders: Ziyan Zhou

## Context

The X server compiles its keyboard map at start-up by running `xkbcomp`, and it
does so through the C library's `popen`, which executes `/bin/sh -c <command>`.
A distroless image has no `/bin/sh`, so the X server fails with:

    XKB: Failed to compile keymap
    Keyboard initialization failed. This could be a missing or incorrect setup
    of xkeyboard-config.
    (EE) Fatal server error:
    (EE) Failed to activate virtual core keyboard: 2

which names neither the shell nor xkbcomp, and cost an hour to trace. It is
fatal: the server does not start at all, with or without a keyboard attached.

The obvious answer is to put a shell in the image. That gives back the thing
the image was chosen to avoid: with a shell present, any bug that can influence
a command line becomes arbitrary code execution, and nothing else in the image
wants one.

## Decision

`/bin/sh` is a symbolic link to the `cue` binary. When the binary is invoked
under that name it runs `internal/minishell`, which accepts only `sh -c
<command>`, splits the command into words honouring single quotes, double
quotes and backslash escapes, and executes it with `syscall.Exec` so that the
pipe `popen` set up reaches the program unchanged.

It has no pipes, no redirection, no variables, no expansion, no substitution,
no built-ins, no operators and no job control. Meeting any shell metacharacter
is an error and exits 127, rather than being approximated — a half-implemented
shell that silently runs a different command than the caller wrote would be
worse than none.

## Consequences

- The X server starts, and the image still has no shell in any sense that
  matters. An attacker who can influence a command line here can execute one
  program with literal arguments; they cannot pipe, redirect, glob, chain or
  substitute.
- Anything else in the image that expects a real shell will fail. So far the
  only other case found is Debian's `/usr/bin/chromium`, which is itself a
  wrapper script; `internal/browser.resolveBinary` steps over it by detecting
  the `#!` and running `/usr/lib/chromium/chromium` directly.
- The behaviour is surprising and is documented in three places: this record,
  the package comment, and a comment at the top of `main`.
- `internal/minishell`'s tests assert that every shell feature is *refused*.
  Those tests are the guard rail: if somebody later makes it "just handle
  pipes", they fail.
