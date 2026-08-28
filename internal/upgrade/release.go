package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Repository is where releases of this program are published.
const Repository = "ziyan/cue"

// gitHubAPI is where releases are asked about. A variable rather than a
// constant so that a test can point it at a server of its own: the alternative
// is a test that either reaches the real internet or proves nothing.
var gitHubAPI = "https://api.github.com"

// howLongToWait bounds a check. A device on a network that accepts the
// connection and then says nothing must not leave the caller waiting: this
// runs behind a page somebody is looking at.
const howLongToWait = 20 * time.Second

// Release is a published release of this program, as much of one as anything
// here needs.
type Release struct {
	// Version is the release number with no leading v, as internal/version
	// reports it, so that the two can be compared without either having to
	// know how the other is written.
	Version string `json:"version"`
	// Notes is the body of the release: Markdown, written for a person,
	// because the release workflow copies it out of CHANGELOG.md.
	Notes string `json:"notes"`
	// PublishedAt is when it was released.
	PublishedAt time.Time `json:"publishedAt"`
	// URL is the page a person can open to read it themselves.
	URL string `json:"url"`
}

// Latest asks GitHub for the newest published release of a repository.
//
// Draft and pre-release versions are not returned: the endpoint used here is
// the one GitHub defines as the latest full release, which skips both. That is
// the right behaviour rather than a limitation -- a screen on a wall should not
// be offered a release its author has not finished.
func Latest(ctx context.Context, client *http.Client, repository string) (Release, error) {
	return latestFrom(ctx, client, repository, "")
}

// latestFrom is Latest against a named API, so that a Checker can be pointed
// at a stand-in and this can be tested without reaching the real internet or
// depending on somebody else's release.
func latestFrom(ctx context.Context, client *http.Client, repository, api string) (Release, error) {
	if client == nil {
		client = &http.Client{Timeout: howLongToWait}
	}
	if api == "" {
		api = gitHubAPI
	}

	url := fmt.Sprintf("%s/repos/%s/releases/latest", api, repository)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	// Unauthenticated, and rate limited by address to sixty an hour. One check
	// a day is nowhere near it, and asking for a token would mean a screen on
	// a wall holding a credential for something it only ever reads.
	request.Header.Set("User-Agent", "cue")

	response, err := client.Do(request)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// A repository with no releases yet answers this way, and so does one
		// that does not exist. Neither is a fault worth alarming anybody with.
		return Release{}, fmt.Errorf("no published release")
	case http.StatusForbidden, http.StatusTooManyRequests:
		return Release{}, fmt.Errorf("GitHub is rate limiting this address; the next check will try again")
	default:
		return Release{}, fmt.Errorf("GitHub answered %s", response.Status)
	}

	// Bounded, because this is parsed into memory on a device with little of
	// it and the other end is not ours to trust with the size.
	var body struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("cannot read what GitHub said: %w", err)
	}

	if body.Draft || body.Prerelease {
		return Release{}, fmt.Errorf("no published release")
	}

	version := strings.TrimPrefix(strings.TrimSpace(body.TagName), "v")
	if version == "" {
		return Release{}, fmt.Errorf("the release has no tag")
	}

	return Release{
		Version:     version,
		Notes:       strings.TrimSpace(body.Body),
		PublishedAt: body.PublishedAt,
		URL:         body.HTMLURL,
	}, nil
}
