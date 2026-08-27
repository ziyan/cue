package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Where the entries come from.
//
// A change reaches the changelog by one of two roads, and both are open on
// purpose. Somebody working through pull requests writes the entry in the
// description, where it is reviewed along with the change it describes and
// cannot be forgotten afterwards. Somebody committing straight to main writes
// it into the Unreleased section by hand, as this repository has been doing
// since the beginning.
//
// Taking only one of the two would quietly drop work. So both are collected
// and merged.

// pullRequest is what this needs from a merged pull request.
type pullRequest struct {
	Number int
	Body   string
	Labels []string
}

// entriesFromPullRequests reads the changelog blocks out of the pull requests
// merged since a tag, keyed by the kind of change.
//
// Returns nothing at all, and no error, when there is no repository to ask or
// no token to ask with. That is the ordinary case on a laptop, where the
// answer comes from the Unreleased section instead.
func entriesFromPullRequests(since string) (map[string][]string, error) {
	repository := os.Getenv("GITHUB_REPOSITORY")
	token := firstOf(os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"))
	if repository == "" || token == "" {
		return nil, nil
	}

	numbers, err := mergedSince(since)
	if err != nil {
		return nil, err
	}

	entries := map[string][]string{}
	for _, number := range numbers {
		request, err := fetchPullRequest(repository, token, number)
		if err != nil {
			// One unreadable pull request is not a reason to release nothing.
			// It is worth saying so, loudly enough to be seen in a log.
			fmt.Fprintf(os.Stderr, "cut: cannot read pull request #%d: %s\n", number, err)
			continue
		}
		if hasLabel(request.Labels, "skip-changelog") {
			fmt.Fprintf(os.Stderr, "cut: #%d is labelled skip-changelog\n", number)
			continue
		}

		for kind, lines := range changelogBlockOf(request.Body) {
			for _, line := range lines {
				// Which pull request said it, so that a year later somebody
				// reading the changelog can find the change itself.
				entries[kind] = append(entries[kind], fmt.Sprintf("%s (#%d)", line, request.Number))
			}
		}
	}
	return entries, nil
}

// mergedPullRequest matches the way a squashed merge names itself: the subject
// ends with the number in brackets. A merge commit says it differently, so
// both are looked for.
var (
	squashed = regexp.MustCompile(`\(#(\d+)\)\s*$`)
	merged   = regexp.MustCompile(`^Merge pull request #(\d+) `)
)

// mergedSince is the pull requests merged since a tag, oldest first.
func mergedSince(tag string) ([]int, error) {
	span := "HEAD"
	if tag != "" {
		span = tag + "..HEAD"
	}

	output, err := exec.Command("git", "log", "--reverse", "--format=%s", span).Output()
	if err != nil {
		return nil, fmt.Errorf("cannot read the commits since %s: %w", span, err)
	}

	var numbers []int
	seen := map[int]bool{}
	for _, subject := range strings.Split(string(output), "\n") {
		for _, pattern := range []*regexp.Regexp{squashed, merged} {
			if found := pattern.FindStringSubmatch(subject); found != nil {
				number, _ := strconv.Atoi(found[1])
				if number > 0 && !seen[number] {
					seen[number] = true
					numbers = append(numbers, number)
				}
				break
			}
		}
	}
	return numbers, nil
}

func fetchPullRequest(repository, token string, number int) (*pullRequest, error) {
	address := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", repository, number)
	request, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 400))
		return nil, fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	var answer struct {
		Number int    `json:"number"`
		Body   string `json:"body"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		return nil, err
	}

	found := &pullRequest{Number: answer.Number, Body: answer.Body}
	for _, label := range answer.Labels {
		found.Labels = append(found.Labels, label.Name)
	}
	return found, nil
}

func hasLabel(labels []string, wanted string) bool {
	for _, label := range labels {
		if strings.EqualFold(label, wanted) {
			return true
		}
	}
	return false
}

func firstOf(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
