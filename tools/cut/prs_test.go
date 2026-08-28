package main

import (
	"os"
	"strings"
	"testing"
)

// The failure this is about: the workflow ran cut without a token in its
// environment, so no pull request descriptions were read, so there were no
// entries -- and a release with no entries fails nothing. It said "nothing
// under Unreleased" and exited zero, which was true and was not the reason.
//
// Nothing can make a missing token an error, because running without one on a
// laptop is normal and correct. What it can do is say so.
func TestItSaysWhyItIsNotReadingPullRequests(t *testing.T) {
	for _, one := range []struct {
		repository string
		token      string
		expect     string
	}{
		{"", "", "no GITHUB_REPOSITORY"},
		{"ziyan/cue", "", "no GH_TOKEN"},
		{"", "a-test-token", "no GITHUB_REPOSITORY"},
	} {
		why := whyNotReadingPullRequests(one.repository, one.token)
		if why == "" {
			t.Errorf("repository=%q token=%q: said nothing was missing",
				one.repository, one.token)
			continue
		}
		if !strings.Contains(why, one.expect) {
			t.Errorf("repository=%q token=%q: %q does not mention %q",
				one.repository, one.token, why, one.expect)
		}
	}
}

// And must say nothing when there is nothing to say, or the workflow log fills
// with a warning that is not true.
func TestItSaysNothingWhenItCanRead(t *testing.T) {
	if why := whyNotReadingPullRequests("ziyan/cue", "a-test-token"); why != "" {
		t.Errorf("complained with both present: %s", why)
	}
}

// The token is read from either name, because the workflow sets one and the
// gh command line sets the other.
func TestEitherTokenNameIsAccepted(t *testing.T) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")
		t.Setenv(name, "a-test-token")

		token := firstOf(os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"))
		if token != "a-test-token" {
			t.Errorf("%s was not picked up: got %q", name, token)
		}
	}
}
