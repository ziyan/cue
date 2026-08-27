package network

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/supervise"
	"github.com/ziyan/cue/internal/util/atomicfile"
	"github.com/ziyan/cue/internal/util/executable"
)

// Manager keeps the machine's interfaces in the state the configuration asks
// for, and supervises the one program needed to do it.
//
// That program is wpa_supplicant, and it is here for a reason worth stating:
// joining a protected wireless network means a four-way handshake with a
// cipher suite negotiation, and reimplementing that in Go would be a bad idea
// carried out badly. Everything else — addresses, routes, resolver, scanning,
// reading the state — is done directly, so wpa_supplicant is only started on a
// machine that actually has a wireless interface to manage.
type Manager struct {
	store *config.Store

	mutex      sync.Mutex
	processes  map[string]*supervise.Process
	lastError  map[string]string
	manageable bool
	whyNot     string
}

// New returns a network manager. Nothing happens until Run.
func New(store *config.Store) *Manager {
	return &Manager{
		store:     store,
		processes: map[string]*supervise.Process{},
		lastError: map[string]string{},
	}
}

// State is what the interface shows about the machine's network.
type State struct {
	// Managed is whether the daemon is configuring anything at all.
	Managed bool `json:"managed"`

	// Manageable is whether it could, and Problem says why not when it
	// cannot — most often that the container has a network namespace of its
	// own and can see only its own interfaces.
	Manageable bool   `json:"manageable"`
	Problem    string `json:"problem"`

	Interfaces []Interface `json:"interfaces"`

	// Errors are the most recent failure per interface, so that a wireless
	// password that is wrong says so rather than the screen simply having no
	// network.
	Errors map[string]string `json:"errors,omitempty"`
}

// State reports what the machine's network looks like now.
func (self *Manager) State() State {
	configuration := self.store.Current()

	self.mutex.Lock()
	state := State{
		Managed:    configuration.Network.Manage,
		Manageable: self.manageable,
		Problem:    self.whyNot,
		Errors:     map[string]string{},
	}
	for name, message := range self.lastError {
		state.Errors[name] = message
	}
	self.mutex.Unlock()

	if interfaces, err := Interfaces(); err == nil {
		state.Interfaces = interfaces
	}
	return state
}

// Scan looks for wireless networks on one interface.
func (self *Manager) Scan(ctx context.Context, interfaceName string) ([]WirelessNetwork, error) {
	if err := self.ensureSupplicant(ctx, interfaceName); err != nil {
		return nil, err
	}
	return Scan(interfaceName)
}

// Run keeps the interfaces in the configured state until the context ends.
func (self *Manager) Run(ctx context.Context) {
	manageable, whyNot := Manageable()

	self.mutex.Lock()
	self.manageable, self.whyNot = manageable, whyNot
	self.mutex.Unlock()

	if !manageable {
		log.Warningf("the network cannot be managed from here: %s", whyNot)
		return
	}

	interval := self.store.Current().Network.ReconcileInterval.Duration()
	if interval <= 0 {
		interval = 30 * time.Second
	}

	// Whether to manage the network is re-read every time round rather than
	// once at the start. It used to be read once, and a daemon that started
	// with it off never looked again -- so a device set up over the air, which
	// turns it on and writes an interface into the configuration as the last
	// step, went on managing nothing and never joined the network somebody had
	// just chosen for it. Anything that can be switched on while the daemon
	// runs has to be looked at while the daemon runs.
	managing := false
	for {
		configuration := self.store.Current()
		if configuration.Network.Manage {
			if !managing {
				log.Noticef("managing %d network interface(s)", len(configuration.Network.Interfaces))
				managing = true
			}
			self.reconcile(ctx)
		} else if managing {
			log.Noticef("no longer managing the network")
			managing = false
			self.stopAll()
		}

		select {
		case <-ctx.Done():
			self.stopAll()
			return
		case <-time.After(interval):
		}
	}
}

// ReconcileNow puts every configured interface into the state it should be in,
// immediately, instead of waiting for the next time round the loop.
//
// It exists for the moment a device is set up over the air: somebody has just
// chosen a network on their phone, the configuration has been written, and
// waiting up to a reconcile interval before even trying to join it would be
// half a minute of a screen saying nothing while they stand there.
func (self *Manager) ReconcileNow(ctx context.Context) {
	if manageable, whyNot := Manageable(); !manageable {
		log.Warningf("the network cannot be managed from here: %s", whyNot)
		return
	}
	self.reconcile(ctx)
}

// reconcile puts every configured interface into the state it should be in.
// One interface failing does not stop the others: a machine with a wireless
// network that has gone away still wants its wired one configured.
func (self *Manager) reconcile(ctx context.Context) {
	for index := range self.store.Current().Network.Interfaces {
		settings := self.store.Current().Network.Interfaces[index]

		if err := self.reconcileOne(ctx, &settings); err != nil {
			self.mutex.Lock()
			previous := self.lastError[settings.Name]
			self.lastError[settings.Name] = err.Error()
			self.mutex.Unlock()

			// Said once, not every thirty seconds: a wireless network that is
			// out of range would otherwise fill the log for a week.
			if previous != err.Error() {
				log.Warningf("%s", err)
			}
			continue
		}

		self.mutex.Lock()
		delete(self.lastError, settings.Name)
		self.mutex.Unlock()
	}
}

func (self *Manager) reconcileOne(ctx context.Context, settings *config.Interface) error {
	if settings.Wireless != nil {
		if err := self.ensureSupplicant(ctx, settings.Name); err != nil {
			return err
		}
		if err := self.ensureJoined(settings); err != nil {
			return err
		}
	}
	return Apply(ctx, settings)
}

// ensureJoined joins the configured wireless network, unless it is already on
// it. Rejoining a network it is already on would drop the connection every
// time this ran.
func (self *Manager) ensureJoined(settings *config.Interface) error {
	status, err := wirelessStatus(settings.Name)
	if err == nil && status.SSID == settings.Wireless.SSID && status.State == "COMPLETED" {
		return nil
	}
	return Join(settings.Name, settings.Wireless.SSID, settings.Wireless.Passphrase.Reveal())
}

// ensureSupplicant starts wpa_supplicant for one interface if it is not
// already running, and waits for its control socket to appear.
func (self *Manager) ensureSupplicant(ctx context.Context, interfaceName string) error {
	self.mutex.Lock()
	existing := self.processes[interfaceName]
	self.mutex.Unlock()

	if existing != nil && existing.State() == supervise.StateRunning {
		return nil
	}
	if existing != nil {
		return nil
	}

	if err := os.MkdirAll(controlDirectory, 0o700); err != nil {
		return fmt.Errorf("network: cannot create %s: %w", controlDirectory, err)
	}
	if err := self.writeSupplicantConfiguration(interfaceName); err != nil {
		return err
	}

	binary, err := executable.Resolve("wpa_supplicant", "/usr/sbin/wpa_supplicant", "/sbin/wpa_supplicant")
	if err != nil {
		return fmt.Errorf("network: %s has no wireless support in this image: %w", interfaceName, err)
	}

	process := supervise.New(&supervise.Settings{
		Name: "wpa_supplicant " + interfaceName,
		Path: binary,
		Arguments: []string{
			"-i", interfaceName,
			"-c", self.supplicantConfiguration(interfaceName),
			// nl80211 is what every current driver speaks; wext is the older
			// interface, kept as a fallback for the ones that do not.
			"-D", "nl80211,wext",
			"-C", controlDirectory,
		},
		Restart:       true,
		Ready:         func(context.Context) error { return supplicantReady(interfaceName) },
		ReadyTimeout:  20 * time.Second,
		CaptureOutput: true,
		Environment:   supervise.Inherit(),
	})

	self.mutex.Lock()
	self.processes[interfaceName] = process
	self.mutex.Unlock()

	process.Start(ctx)

	readyContext, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	return process.WaitReady(readyContext)
}

func supplicantReady(interfaceName string) error {
	if _, err := os.Stat(filepath.Join(controlDirectory, interfaceName)); err != nil {
		return fmt.Errorf("network: wpa_supplicant has not opened its control socket for %s yet", interfaceName)
	}
	return nil
}

func (self *Manager) supplicantConfiguration(interfaceName string) string {
	return filepath.Join(Directory(self.store.Current()), "wpa_supplicant-"+interfaceName+".conf")
}

// writeSupplicantConfiguration writes the file wpa_supplicant reads at start
// and writes back to when a network is saved.
//
// It is written once and then left alone: the networks in it are the ones the
// daemon told it to join, and rewriting the file would throw them away every
// time the daemon restarted.
func (self *Manager) writeSupplicantConfiguration(interfaceName string) error {
	filename := self.supplicantConfiguration(interfaceName)
	if _, err := os.Stat(filename); err == nil {
		return nil
	}

	var builder strings.Builder
	builder.WriteString("# Written by cue when this interface was first managed.\n")
	builder.WriteString("# wpa_supplicant owns it from then on: the networks below are the\n")
	builder.WriteString("# ones the daemon has been told to join, and it saves them here so\n")
	builder.WriteString("# that the machine comes back onto the network after a power cut.\n\n")
	builder.WriteString("ctrl_interface=" + controlDirectory + "\n")
	// Without this the control interface is read-only and nothing can be
	// joined through it.
	builder.WriteString("update_config=1\n")

	return atomicfile.Write(filename, []byte(builder.String()), 0o600)
}

func (self *Manager) stopAll() {
	self.mutex.Lock()
	processes := self.processes
	self.processes = map[string]*supervise.Process{}
	self.mutex.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, process := range processes {
		process.Stop(ctx)
	}
}

// Statuses reports the supervised wireless programs, so that they appear
// alongside everything else the daemon runs.
func (self *Manager) Statuses() []supervise.Status {
	self.mutex.Lock()
	defer self.mutex.Unlock()

	statuses := make([]supervise.Status, 0, len(self.processes))
	for _, process := range self.processes {
		statuses = append(statuses, process.Status())
	}
	return statuses
}
