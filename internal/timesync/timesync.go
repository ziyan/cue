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
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/util/atomicfile"
	"github.com/ziyan/cue/internal/util/executable"
)

var log = logging.MustGetLogger("timesync")

// chronyAccount is the unprivileged account Debian's chronyd drops to. It
// exists in the image for that reason alone.
const chronyAccount = "_chrony"

// Client owns the chronyd process and the file it reads.
//
// It reads the configuration through the store rather than holding a snapshot,
// because the chrony configuration file is rewritten before every start.
type Client struct {
	store *config.Store

	configFilename   string
	runtimeDirectory string
	driftDirectory   string

	mutex sync.Mutex
	// startedWith is the time settings the running chronyd was actually
	// started with, so that the daemon can tell whether a change has reached
	// it. Compared against the running process rather than the previous
	// configuration, so a restart which failed is retried on the next change.
	startedWith config.Time
}

// StartedWith is the settings the running client was started with. The zero
// value means it has never been started.
func (self *Client) StartedWith() config.Time {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.startedWith
}

// socketPath is where chronyd listens for chronyc.
func (self *Client) socketPath() string {
	return filepath.Join(self.runtimeDirectory, "chronyd.sock")
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
func New(store *config.Store) *Client {
	configuration := store.Current()
	return &Client{
		store:          store,
		configFilename: filepath.Join(configuration.Paths.Runtime, "chrony.conf"),
		// chronyd refuses to open its command socket in a directory anybody
		// else can write to, and it writes a pid file whose directory it does
		// not create. Both live in one directory of its own.
		runtimeDirectory: filepath.Join(configuration.Paths.Runtime, "chrony"),
		driftDirectory:   filepath.Join(configuration.Paths.State, "chrony"),
	}
}

// configuration is the settings in force right now.
func (self *Client) configuration() *config.Configuration {
	return self.store.Current()
}

// Settings builds the supervisor settings for chronyd.
func (self *Client) Settings() *supervise.Settings {
	return &supervise.Settings{
		Name:          "chronyd",
		Path:          self.binary(),
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

// binary finds chronyd, which lives in /usr/sbin rather than /usr/bin and so
// is not on the PATH a container is given by default.
func (self *Client) binary() string {
	path, err := executable.Resolve("chronyd", "/usr/sbin/chronyd", "/sbin/chronyd")
	if err != nil {
		log.Errorf("%s", err)
		return "chronyd"
	}
	return path
}

// chronyAccountIds returns the user and group chronyd drops to, or -1 when
// there is no such account — which is the case on a developer's machine, where
// chronyd is not going to be run anyway.
func chronyAccountIds() (int, int) {
	account, err := user.Lookup(chronyAccount)
	if err != nil {
		return -1, -1
	}
	userId, err := strconv.Atoi(account.Uid)
	if err != nil {
		return -1, -1
	}
	groupId, err := strconv.Atoi(account.Gid)
	if err != nil {
		return -1, -1
	}
	return userId, groupId
}

// prepare writes the configuration file. It is rewritten before every start
// so that changing the servers and reloading takes effect on the next restart
// without anybody editing a second file.
func (self *Client) prepare(ctx context.Context) error {
	settings := self.configuration().Time

	self.mutex.Lock()
	self.startedWith = settings
	self.mutex.Unlock()

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
	fmt.Fprintf(&builder, "driftfile %s\n", filepath.Join(self.driftDirectory, "drift"))
	fmt.Fprintf(&builder, "bindcmdaddress %s\n", self.socketPath())
	fmt.Fprintf(&builder, "pidfile %s\n", filepath.Join(self.runtimeDirectory, "chronyd.pid"))

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

	// chronyd drops its privileges to an unprivileged account and then wants
	// to write the drift file and its pid file, so both directories have to
	// belong to that account. The runtime one is 0750 as well, because chronyd
	// refuses to open its command socket in a directory anybody can write to
	// and says so as "Wrong permissions on <directory>".
	userId, groupId := chronyAccountIds()
	for directory, mode := range map[string]os.FileMode{
		self.driftDirectory:   0o755,
		self.runtimeDirectory: 0o750,
	} {
		if err := os.MkdirAll(directory, mode); err != nil {
			return fmt.Errorf("timesync: create %s: %w", directory, err)
		}
		if err := os.Chmod(directory, mode); err != nil {
			return fmt.Errorf("timesync: set the mode of %s: %w", directory, err)
		}
		if userId >= 0 {
			if err := os.Chown(directory, userId, groupId); err != nil {
				log.Debugf("cannot give %s to %s: %s", directory, chronyAccount, err)
			}
		}
	}

	return atomicfile.Write(self.configFilename, []byte(builder.String()), 0o644)
}

// probe is the readiness check: chronyd answers on its command socket. That
// says the process is up, not that the clock is right yet, which can take a
// few seconds more.
func (self *Client) probe(ctx context.Context) error {
	if _, err := os.Stat(self.socketPath()); err != nil {
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
	state := State{Enabled: self.configuration().Time.Enabled, Now: time.Now()}
	if !state.Enabled {
		return state
	}

	chronyc, err := executable.Resolve("chronyc", "/usr/bin/chronyc", "/usr/sbin/chronyc")
	if err != nil {
		state.Problem = err.Error()
		return state
	}

	command := exec.CommandContext(ctx, chronyc, "-c", "-h", self.socketPath(), "tracking")
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
