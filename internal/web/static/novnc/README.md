# noVNC

This directory is a copy of the `core/` and `vendor/` directories of
[noVNC](https://github.com/novnc/noVNC) 1.7.0, taken from the npm package
`@novnc/novnc`. It is the VNC client the Screen page uses to show the display
in a browser tab.

It is vendored rather than fetched by a package manager, and used as plain ES
modules rather than through a bundler, so that building this project is
`go build` and nothing else. An appliance image that needs a JavaScript
toolchain to produce is one more thing that can stop working on a machine
nobody has looked at in a year.

noVNC is licensed under the Mozilla Public License 2.0; see `LICENSE.txt`. The
files here are unmodified. To update it, replace `core/` and `vendor/` with the
contents of a newer release and note the version here.
