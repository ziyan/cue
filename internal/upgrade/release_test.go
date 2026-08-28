package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// serve stands in for GitHub, so that these tests prove something without
// reaching the internet.
func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.HasSuffix(request.URL.Path, "/releases/latest") {
			t.Errorf("asked for %q, which is not the latest release", request.URL.Path)
		}
		response.WriteHeader(status)
		_, _ = response.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	previous := gitHubAPI
	gitHubAPI = server.URL
	t.Cleanup(func() { gitHubAPI = previous })
	return server
}

func TestTheLatestReleaseIsRead(t *testing.T) {
	serve(t, http.StatusOK, `{
		"tag_name": "v0.2.0",
		"name": "v0.2.0",
		"body": "### Fixed\n\n- The image is built for arm64 again.",
		"html_url": "https://github.com/ziyan/cue/releases/tag/v0.2.0",
		"published_at": "2026-08-28T00:35:50Z",
		"draft": false,
		"prerelease": false
	}`)

	release, err := Latest(context.Background(), nil, Repository)
	if err != nil {
		t.Fatal(err)
	}

	// The v belongs to the tag, not to the version. Keeping it would mean
	// every comparison had to know about it.
	if release.Version != "0.2.0" {
		t.Errorf("version is %q, want %q", release.Version, "0.2.0")
	}
	if !strings.Contains(release.Notes, "built for arm64") {
		t.Errorf("the notes did not come through: %q", release.Notes)
	}
	if release.URL == "" {
		t.Error("there is no page to send somebody to")
	}
	if release.PublishedAt.Year() != 2026 {
		t.Errorf("published at %s", release.PublishedAt)
	}
}

// A screen must not be offered something its author has not finished.
func TestADraftOrPreReleaseIsNotOffered(t *testing.T) {
	for _, body := range []string{
		`{"tag_name":"v0.3.0","draft":true}`,
		`{"tag_name":"v0.3.0","prerelease":true}`,
	} {
		serve(t, http.StatusOK, body)
		if _, err := Latest(context.Background(), nil, Repository); err == nil {
			t.Errorf("an unfinished release was offered: %s", body)
		}
	}
}

func TestWhatGitHubSaysWhenThereIsNothingToSay(t *testing.T) {
	serve(t, http.StatusNotFound, `{"message":"Not Found"}`)
	_, err := Latest(context.Background(), nil, Repository)
	if err == nil {
		t.Fatal("a repository with no releases looked like a success")
	}
	if !strings.Contains(err.Error(), "no published release") {
		t.Errorf("unhelpful complaint: %s", err)
	}
}

// Rate limiting is worth naming, because it is the one failure that a person
// can do nothing about and that will fix itself.
func TestRateLimitingSaysSo(t *testing.T) {
	serve(t, http.StatusForbidden, `{"message":"API rate limit exceeded"}`)
	_, err := Latest(context.Background(), nil, Repository)
	if err == nil || !strings.Contains(err.Error(), "rate limiting") {
		t.Errorf("rate limiting was not named: %v", err)
	}
}

func TestNonsenseIsNotBelieved(t *testing.T) {
	serve(t, http.StatusOK, `not json at all`)
	if _, err := Latest(context.Background(), nil, Repository); err == nil {
		t.Error("a body that is not JSON was accepted")
	}

	serve(t, http.StatusOK, `{"tag_name":"   "}`)
	if _, err := Latest(context.Background(), nil, Repository); err == nil {
		t.Error("a release with no tag was accepted")
	}
}

// A device on a network that accepts the connection and then says nothing must
// not hang the page somebody is looking at.
func TestASilentServerDoesNotHangForEver(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	previous := gitHubAPI
	gitHubAPI = server.URL
	defer func() { gitHubAPI = previous }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	started := time.Now()
	if _, err := Latest(ctx, nil, Repository); err == nil {
		t.Error("a server that said nothing looked like a success")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("waited %s for a server that was never going to answer", elapsed)
	}
}
