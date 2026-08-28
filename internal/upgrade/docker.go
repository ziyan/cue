package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// The Docker API is spoken over a Unix socket by hand rather than through the
// official client library. That library brings a large dependency tree for the
// six calls used here -- inspect, pull, create, start, stop, remove -- and this
// image is assembled a package at a time on the grounds that everything in it
// should be there for a reason.
//
// The version is pinned low deliberately. Docker's API is backwards compatible
// within a major version and a daemon refuses a version newer than its own, so
// asking for an old one works on every daemon anybody is running rather than
// only on ones as new as the machine this was built on.
const dockerAPIVersion = "v1.41"

// Docker talks to the daemon on the other end of the socket.
type Docker struct {
	client *http.Client
}

// NewDocker connects to the socket. It does not check that anything is
// listening: that is what Ping is for, and the answer is worth a better error
// message than a failed inspect.
func NewDocker(socket string) *Docker {
	return &Docker{
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socket)
				},
			},
			// No blanket timeout. Every call here is made with a context and
			// the caller sets the deadline, which is the only place that
			// knows what is reasonable: ten minutes is generous for an
			// inspect and not enough for a gigabyte and a half over the
			// connection some buildings have. A timeout here would override
			// the caller's and fail a pull that was still making progress.
			Timeout: 0,
		},
	}
}

// Ping reports whether there is a Docker daemon on the socket.
func (self *Docker) Ping(ctx context.Context) error {
	response, err := self.do(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

// ContainerDetails is as much of a container as replacing one needs. The two
// configuration blocks are kept as raw JSON and handed back to the daemon
// untouched when the replacement is created: a device started with a flag this
// code has never heard of keeps it, which is the whole reason for inspecting
// rather than rebuilding from a template.
type ContainerDetails struct {
	ID         string          `json:"Id"`
	Name       string          `json:"Name"`
	Image      string          `json:"Image"`
	Config     json.RawMessage `json:"Config"`
	HostConfig json.RawMessage `json:"HostConfig"`
	// NetworkSettings carries the networks a container is attached to, which
	// have to be asked for again by name when the replacement is created.
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
	State struct {
		Running bool   `json:"Running"`
		Status  string `json:"Status"`
	} `json:"State"`
}

// Inspect reads everything about a container.
func (self *Docker) Inspect(ctx context.Context, container string) (ContainerDetails, error) {
	response, err := self.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(container)+"/json", nil)
	if err != nil {
		return ContainerDetails{}, err
	}
	defer func() { _ = response.Body.Close() }()

	var details ContainerDetails
	if err := json.NewDecoder(response.Body).Decode(&details); err != nil {
		return ContainerDetails{}, fmt.Errorf("cannot read the container's settings: %w", err)
	}
	// The API returns the name with a leading slash, and every call that takes
	// one wants it without.
	details.Name = strings.TrimPrefix(details.Name, "/")
	return details, nil
}

// Pull fetches an image, waiting for it to finish.
//
// The daemon streams progress as newline-delimited JSON and only reports a
// failure inside that stream, not in the status code, so the whole body is
// read and the last error in it is what decides.
func (self *Docker) Pull(ctx context.Context, image string) error {
	name, tag := splitImage(image)
	path := fmt.Sprintf("/images/create?fromImage=%s&tag=%s",
		url.QueryEscape(name), url.QueryEscape(tag))

	response, err := self.do(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	decoder := json.NewDecoder(response.Body)
	for {
		var line struct {
			Error string `json:"error"`
		}
		if err := decoder.Decode(&line); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("cannot read the pull: %w", err)
		}
		if line.Error != "" {
			return fmt.Errorf("cannot pull %s: %s", image, line.Error)
		}
	}
	return nil
}

// Create makes a container from configuration taken off another one, with a
// different image. Returns its id.
func (self *Docker) Create(ctx context.Context, name string, details ContainerDetails, image string) (string, error) {
	// The create endpoint takes the Config fields at the top level with
	// HostConfig and NetworkingConfig beside them, while inspect returns them
	// nested. So the Config object is used as the body and the rest added to
	// it.
	var body map[string]json.RawMessage
	if err := json.Unmarshal(details.Config, &body); err != nil {
		return "", fmt.Errorf("cannot read the container's configuration: %w", err)
	}

	newImage, err := json.Marshal(image)
	if err != nil {
		return "", err
	}
	body["Image"] = newImage
	body["HostConfig"] = details.HostConfig

	// Hostname is refused together with the host network namespace, which is
	// how cue runs, and inspect returns one whether it was asked for or not.
	if usesHostNetwork(details.HostConfig) {
		delete(body, "Hostname")
		delete(body, "Domainname")
	}

	if len(details.NetworkSettings.Networks) > 0 {
		networks, err := json.Marshal(map[string]interface{}{
			"EndpointsConfig": details.NetworkSettings.Networks,
		})
		if err != nil {
			return "", err
		}
		body["NetworkingConfig"] = networks
	}

	return self.CreateWith(ctx, name, body)
}

// CreateWith makes a container from a body written here rather than taken off
// another container. Used for the helper that performs the swap, which is not
// a copy of anything.
func (self *Docker) CreateWith(ctx context.Context, name string, body interface{}) (string, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	response, err := self.do(ctx, http.MethodPost,
		"/containers/create?name="+url.QueryEscape(name), bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()

	var created struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("cannot read what was created: %w", err)
	}
	return created.ID, nil
}

// Start starts a container.
func (self *Docker) Start(ctx context.Context, container string) error {
	return self.simple(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/start")
}

// Stop stops one, giving it a moment to finish first.
func (self *Docker) Stop(ctx context.Context, container string, wait time.Duration) error {
	return self.simple(ctx, http.MethodPost, fmt.Sprintf("/containers/%s/stop?t=%d",
		url.PathEscape(container), int(wait.Seconds())))
}

// Rename gives a container a different name, which is how the old one gets out
// of the way of the new one without being destroyed first.
func (self *Docker) Rename(ctx context.Context, container, name string) error {
	return self.simple(ctx, http.MethodPost, fmt.Sprintf("/containers/%s/rename?name=%s",
		url.PathEscape(container), url.QueryEscape(name)))
}

// Remove deletes a container.
func (self *Docker) Remove(ctx context.Context, container string, force bool) error {
	path := fmt.Sprintf("/containers/%s?v=0&force=%t", url.PathEscape(container), force)
	return self.simple(ctx, http.MethodDelete, path)
}

func (self *Docker) simple(ctx context.Context, method, path string) error {
	response, err := self.do(ctx, method, path, nil)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

// do makes one call and turns anything that is not a success into an error
// carrying what the daemon said, because "500 Internal Server Error" on its
// own has never helped anybody.
func (self *Docker) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, "http://docker/"+dockerAPIVersion+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := self.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the Docker daemon: %w", err)
	}
	if response.StatusCode >= 400 {
		defer func() { _ = response.Body.Close() }()
		var said struct {
			Message string `json:"message"`
		}
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		_ = json.Unmarshal(raw, &said)
		if said.Message == "" {
			said.Message = strings.TrimSpace(string(raw))
		}
		return nil, fmt.Errorf("docker: %s: %s", response.Status, said.Message)
	}
	return response, nil
}

// usesHostNetwork reports whether a host configuration puts the container in
// the host's network namespace, which is how cue is run.
func usesHostNetwork(hostConfig json.RawMessage) bool {
	var settings struct {
		NetworkMode string `json:"NetworkMode"`
	}
	_ = json.Unmarshal(hostConfig, &settings)
	return settings.NetworkMode == "host"
}

// splitImage separates a reference into the part before the tag and the tag,
// leaving a digest reference alone. A registry host may carry a port, so the
// last colon only counts when it comes after the last slash.
func splitImage(image string) (string, string) {
	if at := strings.Index(image, "@"); at >= 0 {
		return image[:at], image[at+1:]
	}
	slash := strings.LastIndex(image, "/")
	colon := strings.LastIndex(image, ":")
	if colon > slash {
		return image[:colon], image[colon+1:]
	}
	return image, "latest"
}

// containerFromMount finds a container id in a mount source. Docker mounts
// /etc/hostname and /etc/resolv.conf into every container from paths under its
// own state directory, and those paths carry the id.
var containerFromMount = regexp.MustCompile(`/(?:containers|docker/containers)/([0-9a-f]{64})/`)

// OwnContainerID works out which container this process is in.
//
// Not the hostname, which is the usual trick and is wrong here: cue runs with
// the host's network namespace, so it shares the host's UTS namespace too and
// its hostname is the machine's, not the container's. Reading it would find no
// container, or somebody else's.
func OwnContainerID() (string, error) {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return "", fmt.Errorf("cannot tell which container this is: %w", err)
	}
	if found := containerFromMount.FindSubmatch(raw); found != nil {
		return string(found[1]), nil
	}
	return "", fmt.Errorf("cannot tell which container this is; is this running in Docker?")
}
