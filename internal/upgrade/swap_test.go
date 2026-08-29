package upgrade

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	oldImage = "ghcr.io/ziyan/cue:0.1.0"
	newImage = "ghcr.io/ziyan/cue:0.2.0"
)

func alwaysHealthy(context.Context) error { return nil }

func neverHealthy(context.Context) error {
	return fmt.Errorf("nothing is answering")
}

func TestASuccessfulSwapLeavesTheNewOneRunningUnderTheOldName(t *testing.T) {
	fake, docker := newFakeDocker(t)
	original := fake.add("cue", oldImage, true)

	if err := Swap(context.Background(), docker, original.id, newImage, alwaysHealthy); err != nil {
		t.Fatalf("the swap failed: %s", err)
	}

	running := fake.find("cue")
	if running == nil {
		t.Fatal("nothing is called cue any more")
	}
	if running.image != newImage {
		t.Errorf("cue is running %s, want %s", running.image, newImage)
	}
	if !running.running {
		t.Error("the new container is not running")
	}
	if fake.find(original.id) != nil {
		t.Error("the old container is still there")
	}
}

// The property the whole design is for: nothing that works is destroyed until
// something else works. If this order ever changed, a failed upgrade would
// leave a wall display dark with no way to reach it.
func TestTheOldContainerIsNotDestroyedBeforeTheNewOneAnswers(t *testing.T) {
	fake, docker := newFakeDocker(t)
	original := fake.add("cue", oldImage, true)

	if err := Swap(context.Background(), docker, original.id, newImage, alwaysHealthy); err != nil {
		t.Fatal(err)
	}

	if !fake.happened("start cue", "remove cue-previous") {
		t.Errorf("the old container was removed before the new one started:\n  %s",
			strings.Join(fake.events, "\n  "))
	}
	if !fake.happened("rename cue -> cue-previous", "create cue from "+newImage) {
		t.Errorf("the new container was created before the old one moved aside:\n  %s",
			strings.Join(fake.events, "\n  "))
	}
}

func TestAReplacementThatWillNotStartPutsTheOldOneBack(t *testing.T) {
	fake, docker := newFakeDocker(t)
	fake.failStart = newImage
	original := fake.add("cue", oldImage, true)

	err := Swap(context.Background(), docker, original.id, newImage, alwaysHealthy)
	if err == nil {
		t.Fatal("a replacement that would not start looked like a successful upgrade")
	}

	restored := fake.find("cue")
	if restored == nil {
		t.Fatalf("nothing is called cue any more, so the screen is dark:\n  %s",
			strings.Join(fake.events, "\n  "))
	}
	if restored.id != original.id {
		t.Error("the container called cue is not the original one")
	}
	if restored.image != oldImage {
		t.Errorf("it is running %s, want the old %s", restored.image, oldImage)
	}
	if !restored.running {
		t.Error("the old container was put back but not started, so the screen is still dark")
	}
}

// Starting is not the same as working. A container that comes up and then
// cannot serve anything is exactly the case an upgrade must undo, and it is
// the one a naive "did docker start return an error" check would miss.
func TestAReplacementThatNeverAnswersPutsTheOldOneBack(t *testing.T) {
	fake, docker := newFakeDocker(t)
	original := fake.add("cue", oldImage, true)

	// Not the real two minutes: the deadline is what is being relied on, not
	// what is being tested.
	previous := howLongToProveItselfForTest(t, 3)

	err := Swap(context.Background(), docker, original.id, newImage, neverHealthy)
	previous()

	if err == nil {
		t.Fatal("a replacement that never answered looked like a successful upgrade")
	}
	restored := fake.find("cue")
	if restored == nil || restored.id != original.id || !restored.running {
		t.Errorf("the old container was not put back and started:\n  %s",
			strings.Join(fake.events, "\n  "))
	}
	if fake.find(original.id) == nil {
		t.Error("the original was destroyed even though the upgrade failed")
	}
}

func TestAReplacementThatCannotBeCreatedPutsTheOldOneBack(t *testing.T) {
	fake, docker := newFakeDocker(t)
	original := fake.add("cue", oldImage, true)
	fake.failCreate = true

	if err := Swap(context.Background(), docker, original.id, newImage, alwaysHealthy); err == nil {
		t.Fatal("a create that failed looked like a successful upgrade")
	}

	restored := fake.find("cue")
	if restored == nil || restored.id != original.id || !restored.running {
		t.Errorf("the old container was not put back and started:\n  %s",
			strings.Join(fake.events, "\n  "))
	}
}

// The replacement must be created from the original's own settings, not from a
// template: a device started with an unusual flag keeps it across an upgrade.
func TestTheReplacementKeepsTheOriginalSettings(t *testing.T) {
	fake, docker := newFakeDocker(t)
	original := fake.add("cue", oldImage, true)

	if err := Swap(context.Background(), docker, original.id, newImage, alwaysHealthy); err != nil {
		t.Fatal(err)
	}

	replacement := fake.find("cue")
	if replacement == nil {
		t.Fatal("there is no replacement")
	}
	// The fake records what it was sent; the client is what has to send it.
	if !fake.happened("create cue from "+newImage, "start cue") {
		t.Error("the replacement was not created before it was started")
	}
}

func TestSwappingSomethingThatIsNotThereSaysSo(t *testing.T) {
	_, docker := newFakeDocker(t)
	err := Swap(context.Background(), docker, "0000", newImage, alwaysHealthy)
	if err == nil {
		t.Fatal("swapping a container that does not exist looked like a success")
	}
	if !strings.Contains(err.Error(), "cannot read the container") {
		t.Errorf("unhelpful complaint: %s", err)
	}
}

// howLongToProveItselfForTest shortens the deadline and returns the undo.
func howLongToProveItselfForTest(t *testing.T, seconds int) func() {
	t.Helper()
	previous := howLongToProveItself
	howLongToProveItself = time.Duration(seconds) * time.Second
	return func() { howLongToProveItself = previous }
}

// The helper is kept when it exits, so that a failed upgrade can be asked why.
// Something has to clear them away afterwards or a device collects one dead
// container per upgrade for the rest of its life.
func TestFinishedHelpersAreSweptAway(t *testing.T) {
	fake, docker := newFakeDocker(t)
	fake.add("cue", oldImage, true)
	finished := fake.add("cue-abc123-upgrade", newImage, false)
	working := fake.add("cue-def456-upgrade", newImage, true)
	other := fake.add("somebody-elses-container", "other:latest", false)

	sweepHelpersWith(context.Background(), docker)

	if fake.find(finished.id) != nil {
		t.Error("the helper that had finished was left behind")
	}
	if fake.find(working.id) == nil {
		t.Error("a helper still working was removed, which is an upgrade interrupted")
	}
	if fake.find(other.id) == nil {
		t.Error("a container belonging to somebody else was removed")
	}
	if fake.find("cue") == nil {
		t.Error("the screen's own container was removed")
	}
}
