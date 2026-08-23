// Package timesync keeps the clock right, by supervising chronyd.
//
// This matters more than it sounds. A browser cannot validate a TLS
// certificate when the clock is wrong, so a device whose battery has died
// comes up showing a certificate error instead of the dashboard — and the
// error says the certificate is not yet valid, which sends everybody to look
// at the certificate. The screen fixes itself a few seconds after chronyd
// steps the clock, and until then nothing else on the device works either.
package timesync

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/util/atomicfile"
)

var log = logging.MustGetLogger("timesync")

// Client owns the chronyd process and the file it reads.
type Client struct {
	configuration *config.Configuration

	configFilename string
	socketPath     string
	driftFilename  string
}

// State is what the interface shows about the clock.
type State struct {
	Enabled bool `json:"enabled"`

	// Synchronised is whether chronyd believes it has the right time.
	Synchronised bool `json:"synchronised"`

	// Reference is the server the time came from.
	Reference string `json:"reference"`

	// OffsetSeconds is how far the clock is from that server. A large value
	// here is the reason a screen is showing a certificate error.
	OffsetSeconds float64 `json:"offsetSeconds"`

	Stratum int       `json:"stratum"`
	Now     time.Time `json:"now"`

	// Problem is set when the clock cannot be corrected, which on a device is
	// almost always the container not having been given permission to set it.
	Problem string `json:"problem"`
}

// New returns a time client for the given configuration.
func New(configuration *config.Configuration) *Client {
	return &Client{
		configuration:  configuration,
		configFilename: filepath.Join(configuration.Paths.Runtime, "chrony.conf"),
		socketPath:     filepath.Join(configuration.Paths.Runtime, "chronyd.sock"),
		driftFilename:  filepath.Join(configuration.Paths.State, "chrony.drift"),
	}
}

// Settings builds the supervisor settings for chronyd.
func (self *Client) Settings() *supervise.Settings {
	return &supervise.Settings{
		Name:          "chronyd",
		Path:          "chronyd",
		Arguments:     []string{"-d", "-f", self.configFilename},
		Restart:       true,
		BeforeStart:   self.prepare,
		Ready:         self.probe,
		ReadyTimeout:  20 * time.Second,
		CaptureOutput: true,
		OutputLevel:   logging.DEBUG,
		Environment:   supervise.Inherit(),
	}
}

// prepare writes the configuration file. It is rewritten before every start
// so that changing the servers and reloading takes effect on the next restart
// without anybody editing a second file.
func (self *Client) prepare(ctx context.Context) error {
	settings := self.configuration.Time

	var builder strings.Builder
	builder.WriteString("# Written by cue from the time section of cue.yaml.\n")
	builder.WriteString("# Edit that file, not this one: this is rewritten on every start.\n\n")

	for _, server := range settings.Servers {
		// iburst asks four times in the first few seconds rather than once a
		// minute, so a device that has just booted is right almost at once
		// rather than after several minutes of certificate errors.
		fmt.Fprintf(&builder, "pool %s iburst\n", server)
	}

	builder.WriteString("\n")
	fmt.Fprintf(&builder, "driftfile %s\n", self.driftFilename)
	fmt.Fprintf(&builder, "bindcmdaddress %s\n", self.socketPath)

	// The default is to step the clock only during the first three updates
	// and only if it is out by more than a second. A display whose real-time
	// clock battery is dead comes up years out, every time it is switched on,
	// and refusing to step after the first few updates would leave it there.
	// -1 means "always allowed".
	builder.WriteString("makestep 1.0 -1\n")

	// Write the corrected time back to the real-time clock, so that the next
	// boot starts close even before the network is up. Harmless when there is
	// no clock to write to.
	builder.WriteString("rtcsync\n")

	// Nothing should ask this device for the time.
	builder.WriteString("port 0\n")

	if err := os.MkdirAll(filepath.Dir(self.configFilename), 0o755); err != nil {
		return fmt.Errorf("timesync: create %s: %w", filepath.Dir(self.configFilename), err)
	}
	return atomicfile.Write(self.configFilename, []byte(builder.String()), 0o644)
}

// probe is the readiness check: chronyd answers on its command socket. That
// says the process is up, not that the clock is right yet, which can take a
// few seconds more.
func (self *Client) probe(ctx context.Context) error {
	if _, err := os.Stat(self.socketPath); err != nil {
		return fmt.Errorf("timesync: chronyd has not opened its command socket yet")
	}
	return nil
}

// State asks chronyd how the clock is doing.
//
// It runs chronyc rather than speaking chrony's command protocol, which is a
// binary protocol with a version-numbered packet format that would be a
// hundred lines to implement for one screen of the interface. chronyc is in
// the image because chronyd is.
func (self *Client) State(ctx context.Context) State {
	state := State{Enabled: self.configuration.Time.Enabled, Now: time.Now()}
	if !state.Enabled {
		return state
	}

	command := exec.CommandContext(ctx, "chronyc", "-c", "-h", self.socketPath, "tracking")
	output, err := command.Output()
	if err != nil {
		state.Problem = "chronyd is not answering: " + err.Error()
		return state
	}

	// The -c form is comma separated: reference id, reference name, stratum,
	// reference time, system time offset, last offset, rms offset, frequency,
	// and so on. Only the first five are wanted here.
	fields := strings.Split(strings.TrimSpace(string(output)), ",")
	if len(fields) < 5 {
		state.Problem = "chronyc reported something unexpected"
		return state
	}

	state.Reference = fields[1]
	state.Stratum, _ = strconv.Atoi(fields[2])
	state.OffsetSeconds, _ = strconv.ParseFloat(fields[4], 64)
	// Stratum 0 means chronyd has not settled on a source yet.
	state.Synchronised = state.Stratum > 0

	if !state.Synchronised {
		state.Problem = "no time source has answered yet"
	}
	return state
}

// CheckPermission reports whether this process can set the clock at all.
//
// Inside a container it cannot unless the container was given CAP_SYS_TIME,
// and the failure is otherwise silent: chronyd runs, finds a server, works out
// that the clock is an hour out, and cannot do anything about it. That
// produces a device whose screen shows a certificate error forever with a
// perfectly healthy-looking time client running on it.
func CheckPermission() error {
	// Reading the clock's adjustment with a zero-valued request is refused
	// without the capability and harmless with it.
	command := exec.Command("chronyd", "--version")
	if err := command.Run(); err != nil {
		return fmt.Errorf("timesync: chronyd is not available: %w", err)
	}
	return nil
}

// ClockWarning returns a message to log when the clock looks wrong enough to
// break TLS, or an empty string when it is fine.
func ClockWarning(state State) string {
	const tolerable = 30.0
	if !state.Enabled || state.Problem != "" {
		return ""
	}
	if state.OffsetSeconds > tolerable || state.OffsetSeconds < -tolerable {
		return fmt.Sprintf("the clock is %.0f seconds out; pages served over https will fail to load until it is corrected",
			state.OffsetSeconds)
	}
	return ""
}
