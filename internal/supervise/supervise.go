// Package supervise starts, watches, restarts and stops the programs the
// daemon runs: the X server, the browser, the VNC server, the sound server
// and the time client. It is what this project has instead of an init system,
// and it is deliberately small — it does the four things a supervisor has to
// get right and nothing else.
//
// The four:
//
//   - A child is started in its own process group, so that stopping it stops
//     everything it spawned. Chromium is a tree of a dozen processes and
//     signalling only its root leaves the rest running.
//   - Its output is read line by line into the daemon's log, so that "docker
//     logs" shows one interleaved story rather than nothing at all.
//   - A child that exits is restarted after a backoff that grows, so that a
//     program which cannot start — a browser pointed at a binary that is not
//     there — does not spin the machine.
//   - Stopping asks politely first and insists afterwards, because an X
//     server that is killed outright leaves the graphics hardware in a state
//     the next one cannot recover from.
package supervise

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/util/deferutil"
)

var log = logging.MustGetLogger("supervise")

// State is what a supervised program is doing.
type State string

const (
	// StateStopped means it is not running and is not meant to be.
	StateStopped State = "stopped"

	// StateStarting means the process exists but has not passed its readiness
	// check yet.
	StateStarting State = "starting"

	// StateRunning means it is up and, if it has a readiness check, has
	// passed it.
	StateRunning State = "running"

	// StateBackoff means it exited and is waiting before being started again.
	StateBackoff State = "backoff"
)

// Settings describe a program to supervise. Everything except Name and Path
// has a working default.
type Settings struct {
	// Name appears in every log line about this program and in the web
	// interface. Keep it short: "xorg", "chromium", "x11vnc".
	Name string

	// Path is the executable, looked up on PATH when it has no slash in it.
	Path string

	Arguments   []string
	Environment []string
	Directory   string

	// User is an account name to run as. The daemon and the X server have to
	// be root to touch the graphics hardware; the browser must not be. Empty
	// means "stay as we are".
	User string

	// Ready is an optional check run repeatedly after the process starts,
	// until it returns nil or ReadyTimeout passes. Until it passes, the
	// process is "starting" and anything waiting on it waits. This is how the
	// browser knows the X server is accepting connections rather than merely
	// having been executed.
	Ready func(ctx context.Context) error

	// ReadyTimeout bounds the readiness check. Zero means 30 seconds.
	ReadyTimeout time.Duration

	// ReadyInterval is how often the check is retried. Zero means 200ms.
	ReadyInterval time.Duration

	// Restart says whether an exit should be followed by another start. It is
	// true for everything the daemon runs; false is for tests.
	Restart bool

	// MinimumBackoff and MaximumBackoff bound the wait between restarts. The
	// wait doubles on each consecutive failure and resets once the program
	// has stayed up for StableAfter.
	MinimumBackoff time.Duration
	MaximumBackoff time.Duration
	StableAfter    time.Duration

	// StopSignal is sent first when stopping. Zero means SIGTERM.
	StopSignal syscall.Signal

	// StopTimeout is how long to wait after StopSignal before sending
	// SIGKILL. Zero means 10 seconds.
	StopTimeout time.Duration

	// CaptureOutput reads the program's standard output and error into the
	// daemon's log. Turning it off matters for exactly one program: Chromium
	// with logging enabled writes tens of lines a second about WebRTC.
	CaptureOutput bool

	// OutputLevel is the level captured output is logged at. Unset means
	// DEBUG, which keeps a busy program out of the way until somebody turns
	// the level up to look at it. go-logging numbers CRITICAL as zero, so
	// "unset" and "critical" are the same value here; nothing wants a child's
	// output logged as critical, so that ambiguity costs nothing.
	OutputLevel logging.Level

	// BeforeStart runs immediately before each start attempt. It is where a
	// program's runtime directory is prepared — emptying the browser's cache,
	// writing the X authority file — so that the preparation happens again on
	// every restart rather than only once.
	BeforeStart func(ctx context.Context) error

	// AfterReady runs once each time the program becomes ready.
	AfterReady func(ctx context.Context)
}

// Status is a snapshot of a supervised program, for the web interface and the
// logs. It is a value, so reading it cannot race with the supervisor.
type Status struct {
	Name         string     `json:"name"`
	State        State      `json:"state"`
	ProcessID    int        `json:"processId"`
	StartedAt    *time.Time `json:"startedAt"`
	Restarts     int        `json:"restarts"`
	LastExitedAt *time.Time `json:"lastExitedAt"`
	LastError    string     `json:"lastError"`
}

// Process is one supervised program.
type Process struct {
	settings *Settings

	mutex     sync.Mutex
	state     State
	command   *exec.Cmd
	startedAt time.Time
	exitedAt  time.Time
	restarts  int
	lastError string

	// recent holds the last few lines the program wrote. When a program fails
	// to start, the reason is always in its output — and if that output is
	// logged at DEBUG, which is where a chatty program belongs, the operator
	// sees "exited before it was ready" and nothing else. This is what makes
	// the reason visible without turning the level up and restarting.
	recent []string

	// ready is closed each time the program becomes ready and replaced when
	// it stops, so that a caller can wait for "up" without polling.
	ready chan struct{}

	// restartRequested interrupts the wait for the child to exit, or the
	// backoff, so that a restart is immediate rather than eventual.
	restartRequested chan struct{}

	cancel   context.CancelFunc
	finished chan struct{}
}

// New returns a supervisor for one program. Nothing runs until Start.
func New(settings *Settings) *Process {
	if settings.StopSignal == 0 {
		settings.StopSignal = syscall.SIGTERM
	}
	if settings.StopTimeout == 0 {
		settings.StopTimeout = 10 * time.Second
	}
	if settings.ReadyTimeout == 0 {
		settings.ReadyTimeout = 30 * time.Second
	}
	if settings.ReadyInterval == 0 {
		settings.ReadyInterval = 200 * time.Millisecond
	}
	if settings.MinimumBackoff == 0 {
		settings.MinimumBackoff = 500 * time.Millisecond
	}
	if settings.MaximumBackoff == 0 {
		settings.MaximumBackoff = 30 * time.Second
	}
	if settings.StableAfter == 0 {
		settings.StableAfter = 30 * time.Second
	}
	if settings.OutputLevel == 0 {
		settings.OutputLevel = logging.DEBUG
	}
	return &Process{
		settings:         settings,
		state:            StateStopped,
		ready:            make(chan struct{}),
		restartRequested: make(chan struct{}, 1),
	}
}

// Name is the program's short name.
func (self *Process) Name() string {
	return self.settings.Name
}

// Start begins supervising. It returns immediately; use WaitReady to wait for
// the program to come up.
func (self *Process) Start(ctx context.Context) {
	self.mutex.Lock()
	if self.cancel != nil {
		self.mutex.Unlock()
		return
	}
	supervised, cancel := context.WithCancel(ctx)
	self.cancel = cancel
	self.finished = make(chan struct{})
	finished := self.finished
	self.mutex.Unlock()

	go func() {
		defer deferutil.Recover()
		defer close(finished)
		self.supervise(supervised)
	}()
}

// Stop stops the program and waits for the supervisor to finish. It is safe
// to call more than once.
func (self *Process) Stop(ctx context.Context) {
	self.mutex.Lock()
	cancel := self.cancel
	finished := self.finished
	self.cancel = nil
	self.mutex.Unlock()

	if cancel == nil {
		return
	}
	cancel()

	select {
	case <-finished:
	case <-ctx.Done():
		log.Warningf("%s: gave up waiting for the supervisor to finish", self.settings.Name)
	}
}

// Restart asks for the program to be stopped and started again. It returns as
// soon as the request is recorded; the restart itself is the supervisor's.
func (self *Process) Restart() {
	select {
	case self.restartRequested <- struct{}{}:
	default:
		// One pending restart is as good as two.
	}
	self.sendSignal(self.settings.StopSignal)
}

// Status is a snapshot for the interface and the logs.
func (self *Process) Status() Status {
	self.mutex.Lock()
	defer self.mutex.Unlock()

	status := Status{
		Name:      self.settings.Name,
		State:     self.state,
		Restarts:  self.restarts,
		LastError: self.lastError,
	}
	if self.command != nil && self.command.Process != nil {
		status.ProcessID = self.command.Process.Pid
	}
	if !self.startedAt.IsZero() {
		startedAt := self.startedAt
		status.StartedAt = &startedAt
	}
	if !self.exitedAt.IsZero() {
		exitedAt := self.exitedAt
		status.LastExitedAt = &exitedAt
	}
	return status
}

// State is the current state on its own, which is what most callers want.
func (self *Process) State() State {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.state
}

// WaitReady blocks until the program is running and has passed its readiness
// check, or the context is done. This is how the browser waits for the X
// server without either of them knowing about the other.
func (self *Process) WaitReady(ctx context.Context) error {
	for {
		self.mutex.Lock()
		ready := self.ready
		state := self.state
		lastError := self.lastError
		self.mutex.Unlock()

		if state == StateRunning {
			return nil
		}
		select {
		case <-ready:
			// Loop round and read the state, which is now settled.
		case <-ctx.Done():
			if lastError != "" {
				return fmt.Errorf("supervise: %s did not become ready: %s", self.settings.Name, lastError)
			}
			return fmt.Errorf("supervise: %s did not become ready: %w", self.settings.Name, ctx.Err())
		}
	}
}

func (self *Process) supervise(ctx context.Context) {
	backoff := self.settings.MinimumBackoff

	for {
		if ctx.Err() != nil {
			self.setState(StateStopped)
			return
		}

		startedAt := time.Now()
		err := self.runOnce(ctx)
		lifetime := time.Since(startedAt)

		self.mutex.Lock()
		self.exitedAt = time.Now()
		if err != nil {
			self.lastError = err.Error()
		}
		self.mutex.Unlock()

		if ctx.Err() != nil {
			self.setState(StateStopped)
			return
		}

		switch {
		case err != nil:
			log.Errorf("%s: %s", self.settings.Name, err)
		default:
			log.Warningf("%s: exited after %s", self.settings.Name, lifetime.Round(time.Millisecond))
		}

		if !self.settings.Restart {
			self.setState(StateStopped)
			return
		}

		// A program that stayed up long enough to be considered working
		// starts its next backoff from the bottom. Without this, a display
		// that restarts once a day would eventually wait half a minute
		// before coming back.
		if lifetime >= self.settings.StableAfter {
			backoff = self.settings.MinimumBackoff
		}

		self.mutex.Lock()
		self.restarts++
		restarts := self.restarts
		self.mutex.Unlock()

		self.setState(StateBackoff)
		log.Noticef("%s: restarting in %s (restart %d)", self.settings.Name, backoff.Round(time.Millisecond), restarts)

		select {
		case <-ctx.Done():
			self.setState(StateStopped)
			return
		case <-self.restartRequested:
			// An explicit restart is not a failure, so it does not wait.
			backoff = self.settings.MinimumBackoff
		case <-time.After(backoff):
			backoff *= 2
			if backoff > self.settings.MaximumBackoff {
				backoff = self.settings.MaximumBackoff
			}
		}
	}
}

// runOnce starts the program and returns when it has exited. The error it
// returns is about starting, not about the exit status: a program that ran and
// then failed is reported by its exit, which is normal for a supervisor.
func (self *Process) runOnce(ctx context.Context) error {
	if self.settings.BeforeStart != nil {
		if err := self.settings.BeforeStart(ctx); err != nil {
			return fmt.Errorf("preparing to start: %w", err)
		}
	}

	command := exec.Command(self.settings.Path, self.settings.Arguments...)
	command.Env = self.settings.Environment
	command.Dir = self.settings.Directory
	// Its own process group, so that stopping it stops the tree it spawned.
	// Chromium is a dozen processes and signalling only the root leaves the
	// renderers running, holding the graphics device open.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if self.settings.User != "" {
		credential, err := lookupCredential(self.settings.User)
		if err != nil {
			return err
		}
		command.SysProcAttr.Credential = credential
	}

	if self.settings.CaptureOutput {
		output, err := command.StdoutPipe()
		if err != nil {
			return fmt.Errorf("cannot read standard output: %w", err)
		}
		errors, err := command.StderrPipe()
		if err != nil {
			return fmt.Errorf("cannot read standard error: %w", err)
		}
		go self.copyOutput(output)
		go self.copyOutput(errors)
	} else {
		command.Stdout = nil
		command.Stderr = nil
	}

	self.setState(StateStarting)
	if err := command.Start(); err != nil {
		self.setState(StateBackoff)
		return fmt.Errorf("cannot start %s: %w", self.settings.Path, err)
	}

	self.mutex.Lock()
	self.command = command
	self.startedAt = time.Now()
	self.lastError = ""
	self.recent = nil
	self.mutex.Unlock()

	log.Noticef("%s: started as process %d", self.settings.Name, command.Process.Pid)

	// The wait has to happen in a goroutine so that the readiness check and
	// the stop request can both act while the program is running.
	exited := make(chan error, 1)
	go func() {
		defer deferutil.Recover()
		exited <- command.Wait()
	}()

	if self.settings.Ready != nil {
		if err := self.waitReady(ctx, exited); err != nil {
			self.terminate(command)
			<-exited
			self.reportRecentOutput()
			return err
		}
	}

	self.setReady()
	if self.settings.AfterReady != nil {
		go func() {
			defer deferutil.Recover()
			self.settings.AfterReady(ctx)
		}()
	}

	select {
	case err := <-exited:
		self.clearReady()
		if err != nil {
			log.Warningf("%s: %s", self.settings.Name, err)
		}
		return nil
	case <-ctx.Done():
		self.clearReady()
		self.terminate(command)
		<-exited
		return nil
	}
}

// reportRecentOutput logs what the program said before it failed, at a level
// somebody will see. Without this, a program whose output is logged at DEBUG
// fails with nothing but "exited before it was ready" — which is how an
// afternoon goes on a missing shared library or an account that is not there.
func (self *Process) reportRecentOutput() {
	lines := self.RecentOutput()
	if len(lines) == 0 {
		return
	}
	log.Errorf("%s: what it said before giving up:\n    %s", self.settings.Name, strings.Join(lines, "\n    "))
}

// waitReady polls the readiness check until it passes, the program exits, or
// the deadline is reached.
func (self *Process) waitReady(ctx context.Context, exited chan error) error {
	deadline := time.Now().Add(self.settings.ReadyTimeout)
	for {
		select {
		case err := <-exited:
			// Put it back so runOnce's own wait still finds it.
			exited <- err
			return fmt.Errorf("exited before it was ready")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		checkContext, cancel := context.WithTimeout(ctx, self.settings.ReadyInterval*5)
		err := self.settings.Ready(checkContext)
		cancel()
		if err == nil {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("was not ready within %s: %w", self.settings.ReadyTimeout, err)
		}

		select {
		case <-time.After(self.settings.ReadyInterval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// terminate asks the whole process group to stop, and insists after
// StopTimeout. Signalling the group rather than the process is what makes
// stopping Chromium actually stop Chromium.
func (self *Process) terminate(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	processId := command.Process.Pid

	log.Noticef("%s: stopping process group %d with %s", self.settings.Name, processId, self.settings.StopSignal)
	if err := syscall.Kill(-processId, self.settings.StopSignal); err != nil {
		// The group may already be gone, which is not worth a warning.
		log.Debugf("%s: signalling the process group: %s", self.settings.Name, err)
		_ = command.Process.Signal(self.settings.StopSignal)
	}

	deadline := time.After(self.settings.StopTimeout)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			// Signal 0 tests whether the group still exists.
			if err := syscall.Kill(-processId, 0); err != nil {
				return
			}
		case <-deadline:
			log.Warningf("%s: did not stop within %s, killing it", self.settings.Name, self.settings.StopTimeout)
			_ = syscall.Kill(-processId, syscall.SIGKILL)
			return
		}
	}
}

func (self *Process) sendSignal(signal syscall.Signal) {
	self.mutex.Lock()
	command := self.command
	self.mutex.Unlock()
	if command == nil || command.Process == nil {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, signal)
}

// copyOutput reads a pipe line by line into the log. The reader is bounded
// because Chromium writes single lines of several kilobytes and an unbounded
// scanner would keep the largest of them alive for the life of the process.
func (self *Process) copyOutput(reader io.Reader) {
	defer deferutil.Recover()
	buffered := bufio.NewReaderSize(reader, 8*1024)
	for {
		line, err := buffered.ReadString('\n')
		if len(line) > 0 {
			trimmed := trimLine(line)
			if trimmed != "" {
				self.remember(trimmed)
				self.logOutput(trimmed)
			}
		}
		if err != nil {
			return
		}
	}
}

// recentLimit is how many lines of a program's output are kept for the case
// where it will not start. Enough to hold a fatal error and the few lines
// leading up to it; not enough to matter.
const recentLimit = 20

func (self *Process) remember(line string) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.recent = append(self.recent, line)
	if len(self.recent) > recentLimit {
		self.recent = self.recent[len(self.recent)-recentLimit:]
	}
}

// RecentOutput is the last few lines the program wrote, for the interface and
// for the message logged when it will not start.
func (self *Process) RecentOutput() []string {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return append([]string(nil), self.recent...)
}

// logOutput writes one captured line at the configured level. go-logging has
// no level-taking method, so the level is dispatched here.
func (self *Process) logOutput(line string) {
	switch self.settings.OutputLevel {
	case logging.CRITICAL:
		log.Criticalf("%s: %s", self.settings.Name, line)
	case logging.ERROR:
		log.Errorf("%s: %s", self.settings.Name, line)
	case logging.WARNING:
		log.Warningf("%s: %s", self.settings.Name, line)
	case logging.NOTICE:
		log.Noticef("%s: %s", self.settings.Name, line)
	case logging.INFO:
		log.Infof("%s: %s", self.settings.Name, line)
	default:
		log.Debugf("%s: %s", self.settings.Name, line)
	}
}

func trimLine(line string) string {
	const maximum = 2000
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	if len(line) > maximum {
		return line[:maximum] + " …"
	}
	return line
}

func (self *Process) setState(state State) {
	self.mutex.Lock()
	changed := self.state != state
	self.state = state
	self.mutex.Unlock()
	if changed {
		log.Debugf("%s: %s", self.settings.Name, state)
	}
}

func (self *Process) setReady() {
	self.mutex.Lock()
	self.state = StateRunning
	ready := self.ready
	self.mutex.Unlock()

	select {
	case <-ready:
		// Already closed.
	default:
		close(ready)
	}
	log.Noticef("%s: ready", self.settings.Name)
}

func (self *Process) clearReady() {
	self.mutex.Lock()
	select {
	case <-self.ready:
		self.ready = make(chan struct{})
	default:
	}
	self.command = nil
	self.mutex.Unlock()
}

// lookupCredential turns an account name into the user and group id to run as. The
// supplementary groups are included, because the account the browser runs as
// needs to be in the group that owns the graphics device.
func lookupCredential(name string) (*syscall.Credential, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("no account named %q on this system: %w", name, err)
	}
	userId, err := strconv.Atoi(account.Uid)
	if err != nil {
		return nil, fmt.Errorf("account %q has a user id that is not a number: %w", name, err)
	}
	groupId, err := strconv.Atoi(account.Gid)
	if err != nil {
		return nil, fmt.Errorf("account %q has a group id that is not a number: %w", name, err)
	}

	groups := []uint32{}
	if names, err := account.GroupIds(); err == nil {
		for _, group := range names {
			number, err := strconv.Atoi(group)
			if err != nil {
				continue
			}
			groups = append(groups, uint32(number))
		}
	}

	return &syscall.Credential{Uid: uint32(userId), Gid: uint32(groupId), Groups: groups}, nil
}

// Environ builds an environment from a base plus overrides, which is what
// every settings in this project needs and what os/exec does not provide.
func Environ(base []string, overrides map[string]string) []string {
	environment := make([]string, 0, len(base)+len(overrides))
	seen := map[string]bool{}
	for name, value := range overrides {
		environment = append(environment, name+"="+value)
		seen[name] = true
	}
	for _, entry := range base {
		name, _, found := cut(entry, '=')
		if found && seen[name] {
			continue
		}
		environment = append(environment, entry)
	}
	return environment
}

func cut(value string, separator byte) (string, string, bool) {
	for index := 0; index < len(value); index++ {
		if value[index] == separator {
			return value[:index], value[index+1:], true
		}
	}
	return value, "", false
}

// Inherit returns the daemon's own environment, for the specs that want it.
func Inherit() []string {
	return os.Environ()
}
