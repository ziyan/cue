package upgrade

import (
	"context"
	"fmt"
	"time"
)

// The swap: replacing a running cue with a newer one, from the outside.
//
// A container cannot replace itself. The moment it removes itself it is gone,
// along with whatever was going to create the replacement. So a second
// container does it, and the interesting decisions are about what happens when
// that second container fails halfway.
//
// The order below never destroys the only working thing it has. The old
// container is renamed out of the way rather than removed, and is only removed
// once the new one has answered. If the new one does not, the old one is put
// back under its own name and started again. A screen that fails to upgrade
// must still be a screen: the alternative is a dark display on a wall in a
// building whose owner cannot reach it except by walking there.
//
// The helper is built from the image this daemon is *already running*, and
// that is a reversal worth explaining.
//
// It was built from the new image, on the reasoning that the code performing
// the swap should be the code being installed: a bug fixed in the new release
// would then be fixed for the upgrade that installs it. That reasoning is
// sound and the design did not work, because it assumes the new image knows
// how to do this. The first device to try it upgraded to a release published
// before any of this existed: the helper started, said "No help topic for
// upgrade-swap", exited, and was removed by AutoRemove. Nothing was swapped
// and nothing was reported. Every release older than this feature does that,
// and so does anything somebody pins deliberately.
//
// The running image is the one image known to be able to do this, because it
// is doing it. The cost is real and is the other side of the same coin: a bug
// in the swap code here can only be fixed for upgrades that start from a later
// version. That is the lesser failure -- it affects devices already on a bad
// version rather than every device upgrading to a good one.

// helperSuffix names the container that does the work, and the one the old
// container is renamed to while the new one is tried.
const (
	helperSuffix    = "-upgrade"
	displacedSuffix = "-previous"
)

// howLongToStop is what the old daemon gets to shut down before it is killed.
const howLongToStop = 30 * time.Second

// howLongToProveItself is how long the new container has to answer before the
// old one is put back. A variable so that a test can shorten it: what is being
// tested is that the deadline is honoured, not how long it is.
var howLongToProveItself = 2 * time.Minute

// StartHelper creates and starts the container that performs the swap, and
// returns its name once it is running. The caller is about to be stopped by
// it.
//
// helperImage is what the helper is built from -- this daemon's own image --
// and image is what the device is being upgraded to.
func StartHelper(ctx context.Context, docker *Docker, ownContainer, helperImage, image, socket string) (string, error) {
	name := helperName(ownContainer)
	// Anything left from a previous attempt would take the name.
	_ = docker.Remove(ctx, name, true)

	spec := map[string]interface{}{
		"Image": helperImage,
		"Cmd": []string{
			"upgrade-swap",
			"--container", ownContainer,
			"--image", image,
		},
		"HostConfig": map[string]interface{}{
			// The socket to do the work with, and the configuration so that
			// the helper can find out where this device's interface listens
			// and therefore whether the replacement is answering.
			"Binds": []string{
				socket + ":" + socket,
				"/etc/cue:/etc/cue:ro",
			},
			// The host's network namespace, for the same reason: the health
			// check is an HTTP request to this machine's own interface.
			"NetworkMode": "host",
			// Deliberately not AutoRemove. A helper that fails takes its
			// reason with it when it disappears, and the first one to fail
			// did exactly that: it exited immediately, was removed, and left
			// nobody any the wiser about why the screen had not changed.
			// Whoever started it removes it once it has been read.
			"RestartPolicy": map[string]interface{}{
				"Name": "no",
			},
		},
	}

	id, err := docker.CreateWith(ctx, name, spec)
	if err != nil {
		return "", fmt.Errorf("cannot create the container that would do the upgrade: %w", err)
	}
	if err := docker.Start(ctx, id); err != nil {
		return "", fmt.Errorf("cannot start the container that would do the upgrade: %w", err)
	}
	return name, nil
}

// Swap is what the helper runs. It replaces one container with another built
// from image, and puts the original back if the replacement does not answer.
//
// healthy is how it finds out whether the replacement is working; it is a
// function so that this can be tested without a daemon to ask.
func Swap(ctx context.Context, docker *Docker, container, image string, healthy func(context.Context) error) error {
	details, err := docker.Inspect(ctx, container)
	if err != nil {
		return fmt.Errorf("cannot read the container being replaced: %w", err)
	}
	name := details.Name
	if name == "" {
		return fmt.Errorf("the container being replaced has no name")
	}
	log.Noticef("replacing %s with %s", name, image)

	// Anything left over from an interrupted attempt, before the names are
	// needed.
	_ = docker.Remove(ctx, name+displacedSuffix, true)

	if err := docker.Stop(ctx, details.ID, howLongToStop); err != nil {
		return fmt.Errorf("cannot stop %s: %w", name, err)
	}
	if err := docker.Rename(ctx, details.ID, name+displacedSuffix); err != nil {
		// Nothing has been destroyed: the old container is stopped and still
		// itself. Starting it again is the whole of the recovery.
		_ = docker.Start(ctx, details.ID)
		return fmt.Errorf("cannot move %s out of the way: %w", name, err)
	}

	replacement, err := docker.Create(ctx, name, details, image)
	if err != nil {
		putItBack(ctx, docker, details.ID, name)
		return fmt.Errorf("cannot create the new %s: %w", name, err)
	}

	if err := docker.Start(ctx, replacement); err != nil {
		_ = docker.Remove(ctx, replacement, true)
		putItBack(ctx, docker, details.ID, name)
		return fmt.Errorf("cannot start the new %s: %w", name, err)
	}

	if err := waitUntilHealthy(ctx, healthy); err != nil {
		log.Errorf("the new %s did not answer, so the old one is going back: %s", name, err)
		_ = docker.Stop(ctx, replacement, howLongToStop)
		_ = docker.Remove(ctx, replacement, true)
		putItBack(ctx, docker, details.ID, name)
		return fmt.Errorf("the upgraded %s did not answer: %w", name, err)
	}

	// Only now, when there is a working replacement, is the old one destroyed.
	if err := docker.Remove(ctx, details.ID, true); err != nil {
		// Not a failure of the upgrade. The new one is running; this leaves a
		// stopped container behind, which is untidy and harmless.
		log.Warningf("the upgrade worked but the old container could not be removed: %s", err)
	}
	log.Noticef("%s is now running %s", name, image)
	return nil
}

// putItBack restores the original container under its own name and starts it.
//
// Every failure here is reported and none of them stop the attempt: this runs
// when something has already gone wrong, and the worst outcome is a screen
// left dark because the recovery gave up at the first difficulty.
func putItBack(ctx context.Context, docker *Docker, container, name string) {
	if err := docker.Rename(ctx, container, name); err != nil {
		log.Errorf("cannot give %s its name back: %s", name, err)
	}
	if err := docker.Start(ctx, container); err != nil {
		log.Errorf("cannot start %s again: %s", name, err)
	}
}

// waitUntilHealthy asks until it gets a yes, or gives up.
func waitUntilHealthy(ctx context.Context, healthy func(context.Context) error) error {
	deadline := time.Now().Add(howLongToProveItself)
	var last error

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}

		if last = healthy(ctx); last == nil {
			return nil
		}
	}
	if last == nil {
		last = fmt.Errorf("it never answered")
	}
	return last
}

func helperName(container string) string {
	// The id is long; the first twelve characters are what docker ps shows and
	// are enough to keep two of these apart.
	if len(container) > 12 {
		container = container[:12]
	}
	return "cue-" + container + helperSuffix
}

// Begin starts an upgrade and returns as soon as the helper is running. It
// does not return when the upgrade finishes, because by then this process will
// have been stopped by it.
//
// The pull happens here rather than in the helper, on the running daemon,
// while the screen is still showing something. It is the slow part and the
// part most likely to fail -- a gigabyte and a half over whatever connection a
// building has -- and a failure here has changed nothing at all: no container
// has been touched, and the page can say so and offer to try again.
func Begin(ctx context.Context, socket, image string, saying func(string)) error {
	if saying == nil {
		saying = func(string) {}
	}

	docker := NewDocker(socket)
	if err := docker.Ping(ctx); err != nil {
		return fmt.Errorf("cannot reach Docker on %s: %w", socket, err)
	}

	own, err := OwnContainerID()
	if err != nil {
		return err
	}
	details, err := docker.Inspect(ctx, own)
	if err != nil {
		return fmt.Errorf("cannot read this container: %w", err)
	}
	if details.Image == "" {
		return fmt.Errorf("cannot tell which image this container is running")
	}

	saying("Fetching " + image)
	log.Noticef("fetching %s", image)
	if err := docker.Pull(ctx, image); err != nil {
		return err
	}

	saying("Replacing the container")
	log.Noticef("handing over to a helper")
	helper, err := StartHelper(ctx, docker, own, details.Image, image, socket)
	if err != nil {
		return err
	}

	// Watch it, rather than assuming. This daemon is about to be stopped by
	// that helper, so ordinarily this loop simply ends with the process. If it
	// does not -- if the helper exits without replacing anything -- somebody
	// needs to be told why, and the first time this failed nobody was.
	return watchHelper(ctx, docker, helper)
}

// watchHelper waits for the helper to do its work or to die trying.
//
// Returning nil means the helper is still going, which is the ordinary
// outcome: this process is stopped partway through and never returns at all.
func watchHelper(ctx context.Context, docker *Docker, helper string) error {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}

		details, err := docker.Inspect(ctx, helper)
		if err != nil {
			// Gone. Somebody removed it, or Docker did; either way there is
			// nothing left to read and nothing useful to say.
			return nil
		}
		if details.State.Running {
			continue
		}

		// It stopped without stopping us, which means it did not do the job.
		reason := docker.LastWords(ctx, helper)
		_ = docker.Remove(ctx, helper, true)
		if reason == "" {
			reason = "it stopped without saying why"
		}
		return fmt.Errorf("the upgrade did not happen: %s", reason)
	}
	return nil
}
