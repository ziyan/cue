# Changelog

All notable changes to this project are recorded here, in the categories of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). This project follows
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- The first version of everything. One Go daemon, shipped in a distroless
  image, that turns a headless Linux machine with a screen attached into a
  managed display: it starts and supervises an X server, Chromium in kiosk
  mode, a VNC server and a time client, and needs nothing configured on the
  host.
- A playlist of pages, rotating on a timer, each for as long as it says.
- Login rules, re-evaluated every few seconds, which keep a page signed in
  when its session expires and drops the tab back to a login form.
- Dismiss rules, which get rid of the banners and announcements that appear on
  top of a page and stay there.
- A watchdog that asks three questions a frozen display cannot answer — does
  the X server reply, does the page run JavaScript, does it reach its next
  animation frame — and escalates from reloading the page to restarting the
  graphics.
- Display arrangement over RandR, reconciled every few seconds, so that
  unplugging and replugging a monitor brings the picture back on its own.
- A web interface: first-run setup, an overview with a live screenshot and the
  machine's own numbers, a playlist editor, the screen itself over VNC in a
  browser tab, and the device's hardware and logs.
- `cue config`, `cue display probe`, `cue health` and `cue version`.
