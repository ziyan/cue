// Package fleet is the optional half of this project that talks to somebody
// else's server: it registers the device with a management service and then
// keeps one connection open so that the service can reach it.
//
// Everything here is off unless an operator turns it on. The daemon makes no
// outbound connection of its own accord, and a device that is never enrolled
// never speaks to anything but the pages it is showing.
//
// The shape is deliberate and is the part worth understanding. The device
// dials *out* and holds one WebSocket open; the service opens streams on it
// when it wants something. So:
//
//   - Nothing has to be opened on the firewall in front of the screen, and no
//     port is published. A display in a shop behind a domestic router is
//     reachable without anybody configuring that router.
//   - What the service can do is exactly what a person standing in front of
//     the device could do through the web interface, because the streams are
//     handed to that same HTTP handler. There is no second, more privileged
//     interface.
//   - The device can be unenrolled by deleting one file, and it then goes back
//     to talking to nothing.
package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/util/atomicfile"
	"github.com/ziyan/cue/internal/version"
)

var log = logging.MustGetLogger("fleet")

// Credential is what the service gives back in exchange for an enrolment
// token. It is stored under the state directory and is what the device
// authenticates with from then on.
//
// The token is used once and is then cleared from the configuration file, so
// that a token which leaks — in a provisioning script, in a shell history —
// cannot be used to enrol a second device pretending to be this one.
type Credential struct {
	// DeviceIdentifier is this device's identifier as the service knows it.
	// It is the same one in the configuration file, sent at enrolment.
	DeviceIdentifier string `json:"deviceIdentifier"`

	// Secret authenticates every connection afterwards.
	Secret string `json:"secret"`

	// URL is the service this credential is for, kept so that pointing a
	// device at a different service is noticed rather than silently failing
	// to authenticate.
	URL string `json:"url"`

	EnrolledAt time.Time `json:"enrolledAt"`
}

// State is what the interface shows about enrolment.
type State struct {
	Enabled   bool   `json:"enabled"`
	Enrolled  bool   `json:"enrolled"`
	URL       string `json:"url"`
	Connected bool   `json:"connected"`

	// LastError is why it is not connected, which is the only interesting
	// thing about a fleet connection that is not working.
	LastError     string     `json:"lastError"`
	LastAttempt   *time.Time `json:"lastAttempt"`
	ConnectedAt   *time.Time `json:"connectedAt"`
	Reconnects    int        `json:"reconnects"`
	StreamsServed int        `json:"streamsServed"`
}

// credentialFilename is where the credential lives, under the state
// directory. 0600: it is the only thing standing between somebody with a copy
// of this file and control of the screen.
func credentialFilename(configuration *config.Configuration) string {
	return filepath.Join(configuration.Paths.State, "fleet.json")
}

// LoadCredential reads the stored credential, returning nil when the device
// has not been enrolled.
func LoadCredential(configuration *config.Configuration) (*Credential, error) {
	content, err := os.ReadFile(credentialFilename(configuration))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fleet: cannot read the enrolment credential: %w", err)
	}

	credential := &Credential{}
	if err := json.Unmarshal(content, credential); err != nil {
		return nil, fmt.Errorf("fleet: the enrolment credential is not readable: %w", err)
	}
	return credential, nil
}

// SaveCredential stores a credential.
func SaveCredential(configuration *config.Configuration, credential *Credential) error {
	content, err := json.MarshalIndent(credential, "", "  ")
	if err != nil {
		return fmt.Errorf("fleet: %w", err)
	}
	return atomicfile.Write(credentialFilename(configuration), content, 0o600)
}

// ForgetCredential unenrols the device.
func ForgetCredential(configuration *config.Configuration) error {
	err := os.Remove(credentialFilename(configuration))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("fleet: cannot remove the enrolment credential: %w", err)
	}
	return nil
}

// enrolmentRequest is what the device sends once, to register itself.
type enrolmentRequest struct {
	Token      string `json:"token"`
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Version    string `json:"version"`
	Hostname   string `json:"hostname"`
}

// enrolmentResponse is what comes back.
type enrolmentResponse struct {
	Secret string `json:"secret"`

	// Name, when the service sends one, renames the device. A fleet is
	// usually named centrally, and a screen called "cue" in a list of two
	// hundred is no use to anybody.
	Name string `json:"name"`
}

// Enrol registers the device and returns the credential to store. It is used
// once; afterwards the token is cleared from the configuration.
func Enrol(ctx context.Context, configuration *config.Configuration) (*Credential, string, error) {
	settings := configuration.Fleet
	if !settings.EnrollmentToken.IsSet() {
		return nil, "", fmt.Errorf("fleet: there is no enrolment token to use")
	}

	hostname, _ := os.Hostname()
	request := enrolmentRequest{
		Token:      settings.EnrollmentToken.Reveal(),
		Identifier: configuration.Device.Identifier,
		Name:       configuration.Device.Name,
		Location:   configuration.Device.Location,
		Version:    version.Version(),
		Hostname:   hostname,
	}

	var reply enrolmentResponse
	if err := postJSON(ctx, settings.URL+enrolmentPath, request, &reply); err != nil {
		return nil, "", err
	}
	if reply.Secret == "" {
		return nil, "", fmt.Errorf("fleet: %s accepted the enrolment but sent no credential back", settings.URL)
	}

	credential := &Credential{
		DeviceIdentifier: configuration.Device.Identifier,
		Secret:           reply.Secret,
		URL:              settings.URL,
		EnrolledAt:       time.Now(),
	}
	return credential, reply.Name, nil
}

const (
	// enrolmentPath is where a device registers itself.
	enrolmentPath = "/api/v1/devices/enrol"

	// tunnelPath is where it holds its connection open.
	tunnelPath = "/api/v1/devices/tunnel"
)

func postJSON(ctx context.Context, address string, body interface{}, into interface{}) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("fleet: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytesReader(encoded))
	if err != nil {
		return fmt.Errorf("fleet: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "cue/"+version.Version())

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("fleet: cannot reach %s: %w", address, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fleet: %s answered %s", address, response.Status)
	}
	return json.NewDecoder(response.Body).Decode(into)
}
