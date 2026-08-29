package upgrade

import (
	"context"
	"net/http"
	"testing"
)

func TestACheckerReportsWhatItFound(t *testing.T) {
	serve(t, http.StatusOK, `{
		"tag_name": "v0.2.0",
		"body": "### Fixed\n\n- Something.",
		"html_url": "https://github.com/ziyan/cue/releases/tag/v0.2.0",
		"published_at": "2026-08-28T00:35:50Z"
	}`)

	checker := NewChecker(Repository, "0.1.0")
	state, err := checker.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if state.Running != "0.1.0" || state.Latest != "0.2.0" {
		t.Errorf("running %q, latest %q", state.Running, state.Latest)
	}
	if !state.Newer {
		t.Error("0.2.0 was not reported as newer than 0.1.0")
	}
	if state.CheckedAt.IsZero() {
		t.Error("the answer has no date, so a page cannot say how old it is")
	}
	if state.Trouble != "" {
		t.Errorf("a successful check left trouble behind: %q", state.Trouble)
	}
}

func TestADeviceAlreadyOnTheNewestIsNotOfferedAnUpgrade(t *testing.T) {
	serve(t, http.StatusOK, `{"tag_name":"v0.2.0","published_at":"2026-08-28T00:35:50Z"}`)

	state, err := NewChecker(Repository, "0.2.0").Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Newer {
		t.Error("a device on the newest release was offered an upgrade to it")
	}
	if state.Latest != "0.2.0" {
		t.Errorf("it should still know what the newest is: %q", state.Latest)
	}
}

// The point of this one: a device that saw a release yesterday and cannot
// reach GitHub today still knows about it. Forgetting would make an upgrade
// silently disappear from the page the moment a network went down, which reads
// as "you are up to date" and is the opposite of the truth.
func TestAFailedCheckDoesNotForgetWhatWasKnown(t *testing.T) {
	serve(t, http.StatusOK, `{"tag_name":"v0.2.0","body":"notes","published_at":"2026-08-28T00:35:50Z"}`)
	checker := NewChecker(Repository, "0.1.0")
	if _, err := checker.Check(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Now the network goes away.
	serve(t, http.StatusInternalServerError, `nope`)
	state, err := checker.Check(context.Background())
	if err == nil {
		t.Fatal("a failing GitHub looked like a success")
	}

	if state.Latest != "0.2.0" {
		t.Errorf("what was known was forgotten: latest is %q", state.Latest)
	}
	if !state.Newer {
		t.Error("the device stopped being told an upgrade exists")
	}
	if state.Trouble == "" {
		t.Error("nothing says the last check failed, so the age of the answer is a lie")
	}
}

// Before the first check there is nothing to say, and saying nothing must not
// look like being up to date.
func TestBeforeAnyCheckThereIsNoClaim(t *testing.T) {
	state := NewChecker(Repository, "0.1.0").State()
	if state.Latest != "" {
		t.Errorf("it claims to know the latest is %q before asking anybody", state.Latest)
	}
	if state.Newer {
		t.Error("it offers an upgrade it has not heard of")
	}
	if state.Running != "0.1.0" {
		t.Errorf("it does not know what it is running: %q", state.Running)
	}
}
