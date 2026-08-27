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

// AccessPoint is a temporary wireless network the device runs itself, so that
// somebody with nothing but a phone can tell it which real network to join.
//
// It is run by wpa_supplicant, the same program that joins networks, with a
// configuration that puts it in mode 2. That is worth saying plainly because
// it is surprising: wpa_supplicant is thought of as the client and hostapd as
// the server, and the two are usually described as opposites. The build Debian
// ships has access point support compiled in, so the image needs no second
// wireless daemon -- which matters, because it has no package manager and
// every program in it is one more thing to keep patched.
type AccessPoint struct {
	store         *config.Store
	interfaceName string
	credentials   Credentials

	mutex   sync.Mutex
	process *supervise.Process
}

// accessPointChannel is channel 6 in the 2.4 GHz band, as a frequency in MHz.
//
// 2.4 GHz because every phone has it, including old ones and cheap ones, and
// because a device fresh out of its box may not know which country it is in --
// and without that the 5 GHz channels it is allowed to use are unknown, while
// the low 2.4 GHz ones are permitted almost everywhere. Channel 6 because 1, 6
// and 11 are the three that do not overlap, and 6 sits in the middle.
//
// This network exists for a few minutes to carry a form. It does not need to
// be fast.
const accessPointChannel = 2437

// NewAccessPoint prepares a temporary network. It does not touch the radio:
// that happens in Start.
func NewAccessPoint(store *config.Store, interfaceName string, credentials Credentials) *AccessPoint {
	return &AccessPoint{store: store, interfaceName: interfaceName, credentials: credentials}
}

// Name is the network name a phone will see.
func (self *AccessPoint) Name() string { return self.credentials.SSID }

// Credentials are what the QR code on the screen has to carry.
func (self *AccessPoint) Credentials() Credentials { return self.credentials }

// Running reports whether the network is up.
func (self *AccessPoint) Running() bool {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.process != nil && self.process.State() == supervise.StateRunning
}

// Start puts the radio into access point mode and returns once the network is
// being advertised.
//
// Calling it when the network is already up does nothing, so a caller that is
// not sure of the state can simply ask again.
func (self *AccessPoint) Start(ctx context.Context) error {
	self.mutex.Lock()
	if self.process != nil && self.process.State() == supervise.StateRunning {
		self.mutex.Unlock()
		return nil
	}
	self.mutex.Unlock()

	supported, err := radioSupportsAccessPoint(self.interfaceName)
	if err != nil {
		return err
	}
	if !supported {
		return fmt.Errorf("network: the radio behind %s cannot run a network of its own, "+
			"so this device cannot be set up over the air", self.interfaceName)
	}

	if err := os.MkdirAll(controlDirectory, 0o700); err != nil {
		return fmt.Errorf("network: cannot create %s: %w", controlDirectory, err)
	}
	if err := self.writeConfiguration(); err != nil {
		return err
	}

	binary, err := executable.Resolve("wpa_supplicant", "/usr/sbin/wpa_supplicant", "/sbin/wpa_supplicant")
	if err != nil {
		return fmt.Errorf("network: this image has no wpa_supplicant: %w", err)
	}

	process := supervise.New(&supervise.Settings{
		Name: "wpa_supplicant ap " + self.interfaceName,
		Path: binary,
		Arguments: []string{
			"-i", self.interfaceName,
			"-c", self.configurationFilename(),
			"-D", "nl80211",
			"-C", controlDirectory,
		},
		// Not restarted. A setup network that keeps coming back on its own
		// would fight the step that takes it down to join a real network,
		// which is the one moment this must stay out of the way.
		Restart:       false,
		Ready:         func(context.Context) error { return self.advertising() },
		ReadyTimeout:  20 * time.Second,
		CaptureOutput: true,
		Environment:   supervise.Inherit(),
	})

	self.mutex.Lock()
	self.process = process
	self.mutex.Unlock()

	process.Start(ctx)

	readyContext, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := process.WaitReady(readyContext); err != nil {
		self.Stop(ctx)
		return fmt.Errorf("network: %s did not come up as %q: %w",
			self.interfaceName, self.credentials.SSID, err)
	}
	return nil
}

// Stop takes the network down. Calling it when nothing is running does
// nothing.
func (self *AccessPoint) Stop(ctx context.Context) {
	self.mutex.Lock()
	process := self.process
	self.process = nil
	self.mutex.Unlock()

	if process != nil {
		process.Stop(ctx)
	}
	// The configuration carries the passphrase, and the passphrase is the one
	// secret of this whole arrangement. It is no use to anybody once the
	// network is down, so it does not outlive it.
	if err := os.Remove(self.configurationFilename()); err != nil && !os.IsNotExist(err) {
		log.Debugf("cannot remove the access point configuration: %s", err)
	}
}

// advertising reports whether wpa_supplicant has actually got the network on
// the air, rather than merely having started.
//
// It asks over the control socket rather than watching for the socket to
// appear, because the socket appears well before the radio is beaconing, and a
// phone told to scan in that window finds nothing and the person concludes the
// screen is broken.
func (self *AccessPoint) advertising() error {
	control, err := openControl(self.interfaceName)
	if err != nil {
		return err
	}
	defer control.Close()

	reply, err := control.ask("STATUS")
	if err != nil {
		return err
	}
	values := parseKeyValues(reply)
	if state := values["wpa_state"]; state != "COMPLETED" {
		return fmt.Errorf("network: %s is in state %q and not yet advertising %q",
			self.interfaceName, state, self.credentials.SSID)
	}
	if got := values["ssid"]; got != self.credentials.SSID {
		return fmt.Errorf("network: %s is advertising %q and not %q",
			self.interfaceName, got, self.credentials.SSID)
	}
	return nil
}

func (self *AccessPoint) configurationFilename() string {
	return filepath.Join(self.store.Current().Paths.State,
		"wpa_supplicant-ap-"+self.interfaceName+".conf")
}

// writeConfiguration writes the file wpa_supplicant reads at start.
//
// Unlike the station configuration, which is written once and then left for
// wpa_supplicant to own, this one is rewritten every time: it holds no state
// worth keeping, and the passphrase in it is different for every setup
// session.
func (self *AccessPoint) writeConfiguration() error {
	var builder strings.Builder
	builder.WriteString("# Written by cue for setting this device up, and deleted afterwards.\n")
	builder.WriteString("ctrl_interface=" + controlDirectory + "\n")
	// The daemon owns this file, so wpa_supplicant must not rewrite it.
	builder.WriteString("update_config=0\n\n")
	builder.WriteString("network={\n")
	builder.WriteString("\tssid=" + quote(self.credentials.SSID) + "\n")
	// mode=2 is what makes this an access point rather than a client.
	builder.WriteString("\tmode=2\n")
	builder.WriteString(fmt.Sprintf("\tfrequency=%d\n", accessPointChannel))
	builder.WriteString("\tkey_mgmt=WPA-PSK\n")
	// RSN with CCMP is WPA2 and nothing older. Some phones now refuse to join
	// a network offering WPA1 or TKIP at all, and offering them would weaken
	// the network for every phone that would still accept them.
	builder.WriteString("\tproto=RSN\n")
	builder.WriteString("\tpairwise=CCMP\n")
	builder.WriteString("\tgroup=CCMP\n")
	builder.WriteString("\tpsk=" + quote(self.credentials.Passphrase) + "\n")
	builder.WriteString("}\n")

	return atomicfile.Write(self.configurationFilename(), []byte(builder.String()), 0o600)
}

// SupportsAccessPoint reports whether this interface's radio can run a network
// of its own. It is the check that keeps onboarding from being attempted on
// hardware that cannot do it.
func SupportsAccessPoint(interfaceName string) (bool, error) {
	return radioSupportsAccessPoint(interfaceName)
}

// AccessPointCapableInterface returns the name of an interface whose radio can
// run a network of its own, or an empty string when the machine has none.
func AccessPointCapableInterface() string {
	interfaces, err := Interfaces()
	if err != nil {
		return ""
	}
	for _, one := range interfaces {
		if one.Kind != "wireless" {
			continue
		}
		if supported, err := SupportsAccessPoint(one.Name); err == nil && supported {
			return one.Name
		}
	}
	return ""
}
