# Running it on your own machine

You need Go and Docker. There is no JavaScript toolchain.

## Build and test

    make build          # build/cue
    make test           # the unit tests
    make lint           # golangci-lint, the secret scan, and mulint if you have it
    make docker         # the container image, tagged cue:dev
    make docker-smoke   # start that image against a virtual screen and prove it works

`make check-packages` asks Debian whether every package the image installs
exists for every architecture the image is published for. It is not part of
`make lint` because it needs the network; continuous integration runs it as a
step of its own, and the release workflow runs it before spending twenty
minutes building for two architectures under emulation.

`make docker-smoke` is the test that matters. It starts the real image, sets
the device up, waits for a page to reach the screen, checks that every program
is running, takes a screenshot and checks its size, watches the playlist
rotate, and checks that the watchdog is satisfied. Run it after anything that
touches the X server, the browser or the image.

## Run the daemon directly

Against a virtual screen, as yourself, with everything under `./dev`:

    make dev

That writes `dev/cue.yaml` on the first run with development settings: `Xvfb`
instead of `Xorg`, display `:9` so it cannot collide with your own desktop, no
user switching, the sandbox off, and the interface on `127.0.0.1:8080`.

It needs `Xvfb`, `chromium` and `x11vnc` on your PATH. On Debian:

    apt-get install xvfb chromium x11vnc

If you would rather not install those, use the image instead:

    docker compose -f deploy/docker-compose.dev.yml up

## Look at a device

`cue display probe` lists the machine's display connectors and what is plugged
into them, without needing an X server:

    ./build/cue display probe

`cue display outputs` asks a running X server what it is actually driving:

    XAUTHORITY=/run/cue/Xauthority ./build/cue display outputs

`cue config check` reports every problem with a configuration file at once:

    ./build/cue config check --config dev/cue.yaml

## Watch what a device is doing

Everything the web interface shows is in one API response:

    curl -s http://device:8080/api/v1/status | jq

and a picture of the screen is one request:

    curl -s http://device:8080/api/v1/screenshot.png > screen.png

Both need a session; see `docs/reference/api.md`.

## Things that will trip you up

- **`gofmt -w .` reformats `vendor/`.** Use `make format`.
- **The image build downloads several hundred megabytes of Debian packages the
  first time.** It is cached afterwards; a change to the Go code does not
  repeat it.
- **Do not mount the configuration file individually.** `-v ./cue.yaml:/etc/cue/cue.yaml`
  makes every save fail, because a rename cannot replace a bind mount. Mount
  the directory.
- **The X server needs a real graphics device.** Inside a container that means
  `/dev/dri` passed through, and on the host it means no display manager
  holding it.
