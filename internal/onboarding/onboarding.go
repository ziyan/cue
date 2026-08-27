// Package onboarding runs the temporary wireless network, the address server,
// the name server and the setup portal that let somebody configure a brand new
// device with nothing but a phone.
//
// It exists because a device with no network shows a welcome page telling
// whoever is standing in front of it to open an address the device does not
// have. If the room has no ethernet, there is no way forward at all.
package onboarding

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/op/go-logging"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/network"
	"github.com/ziyan/cue/internal/network/onboarding"
)

var log = logging.MustGetLogger("onboarding")

// Onboarding is the whole flow. One of these exists per daemon, and it does
// nothing at all unless the device turns out to need setting up.
type Onboarding struct {
	store *config.Store

	mutex         sync.Mutex
	running       bool
	credentials   network.Credentials
	point         *network.AccessPoint
	interfaceName string
	stop          context.CancelFunc

	// networks is what the radio saw before it became an access point. The
	// portal shows this list: the radio cannot scan across every channel while
	// it is busy advertising on one, so the scan happens first, while nobody
	// is connected yet.
	networks []network.WirelessNetwork

	// trouble is what to tell somebody next time they open the portal, after
	// a join that did not work.
	trouble string
}

func New(store *config.Store) *Onboarding {
	return &Onboarding{store: store}
}

// Running reports whether the setup network is up.
func (self *Onboarding) Running() bool {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.running
}

// Credentials are the setup network's name and passphrase, for the QR code on
// the screen. They are meaningless unless Running.
func (self *Onboarding) Credentials() network.Credentials {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.credentials
}

// Networks is what the portal offers to join.
func (self *Onboarding) Networks() []network.WirelessNetwork {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return append([]network.WirelessNetwork(nil), self.networks...)
}

// Trouble is what went wrong with the last attempt to join, or empty.
func (self *Onboarding) Trouble() string {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.trouble
}

// Start brings up the setup network on the named interface, after scanning
// from it for the networks the portal will offer.
//
// Scanning first is not an optimisation. A radio can be an access point and a
// station at the same time only on one channel, so a scan across every channel
// while the access point is advertising would either fail or interrupt the very
// network the phone is sitting on. Doing it first costs a few seconds when
// nobody is connected.
func (self *Onboarding) Start(ctx context.Context, interfaceName string) error {
	self.mutex.Lock()
	if self.running {
		self.mutex.Unlock()
		return nil
	}
	self.mutex.Unlock()

	supported, err := network.SupportsAccessPoint(interfaceName)
	if err != nil {
		return err
	}
	if !supported {
		return fmt.Errorf("onboarding: the radio behind %s cannot run a network of "+
			"its own, so this device cannot be set up over the air", interfaceName)
	}

	credentials, err := network.NewCredentials()
	if err != nil {
		return err
	}

	// Best effort: a scan that fails leaves an empty list, and the portal
	// offers a "scan again" button. Refusing to start setup because a scan
	// failed would leave the person with nothing at all.
	found, err := network.Scan(interfaceName)
	if err != nil {
		log.Warningf("cannot scan from %s before setting up, so the setup page will "+
			"start with an empty list: %s", interfaceName, err)
	}

	point := network.NewAccessPoint(self.store, interfaceName, credentials)
	if err := point.Start(ctx); err != nil {
		return err
	}

	if err := network.GiveAddress(interfaceName, onboarding.DeviceAddress, onboarding.SubnetMask); err != nil {
		point.Stop(ctx)
		return err
	}

	running, stop := context.WithCancel(ctx)
	go func() {
		if err := onboarding.ServeDHCP(running, interfaceName); err != nil && running.Err() == nil {
			log.Errorf("the setup address server stopped: %s", err)
		}
	}()
	go func() {
		address := onboarding.DeviceAddress.String() + ":53"
		if err := onboarding.ServeDNS(running, onboarding.DeviceAddress, address); err != nil && running.Err() == nil {
			log.Errorf("the setup name server stopped: %s", err)
		}
	}()

	self.mutex.Lock()
	self.running = true
	self.credentials = credentials
	self.point = point
	self.interfaceName = interfaceName
	self.networks = found
	self.stop = stop
	self.mutex.Unlock()

	log.Noticef("this device is ready to be set up: it is advertising %q, and the "+
		"passphrase for it is on its screen and nowhere else", credentials.SSID)
	return nil
}

// Stop takes the setup network down and forgets the passphrase.
func (self *Onboarding) Stop(ctx context.Context) {
	self.mutex.Lock()
	point := self.point
	stop := self.stop
	self.running = false
	self.point = nil
	self.stop = nil
	self.credentials = network.Credentials{}
	self.mutex.Unlock()

	if stop != nil {
		stop()
	}
	if point != nil {
		point.Stop(ctx)
	}
}

// Rescan takes the setup network down for as long as a scan takes, scans, and
// brings it back.
//
// This is what the "scan again" button does, and it is deliberately something
// somebody has to ask for: it drops the phone off the network for a few
// seconds. The phone rejoins on its own, because it is a network it already
// knows, but that is a worse experience than not doing it, so it is not done
// on a timer.
func (self *Onboarding) Rescan(ctx context.Context) error {
	self.mutex.Lock()
	interfaceName := self.interfaceName
	running := self.running
	self.mutex.Unlock()

	if !running {
		return fmt.Errorf("onboarding: this device is not being set up")
	}

	found, err := network.Scan(interfaceName)
	if err != nil {
		return err
	}

	self.mutex.Lock()
	self.networks = found
	self.mutex.Unlock()
	return nil
}

// joinTimeout is how long to wait for an address on the network somebody
// chose, before deciding it did not work and bringing the setup network back.
//
// Forty-five seconds covers a slow association plus a slow DHCP server. It is
// long enough that somebody watching the screen wonders, which is why the
// portal says what is happening before this starts.
const joinTimeout = 45 * time.Second

// Join leaves the setup network and joins the one somebody chose.
//
// This is the step that must not strand the device. It takes the setup network
// down -- the radio cannot be an access point here and a station on another
// channel at once -- and if the join does not work it puts the setup network
// back, so that a mistyped passphrase costs forty-five seconds rather than a
// trip up a ladder.
func (self *Onboarding) Join(ctx context.Context, ssid, passphrase string) error {
	self.mutex.Lock()
	interfaceName := self.interfaceName
	running := self.running
	self.mutex.Unlock()

	if !running {
		return fmt.Errorf("onboarding: this device is not being set up")
	}

	log.Noticef("leaving the setup network to join %q", ssid)
	self.Stop(ctx)

	if err := network.Join(interfaceName, ssid, passphrase); err != nil {
		self.recover(ctx, interfaceName, fmt.Sprintf("Could not join %q.", ssid))
		return err
	}

	deadline := time.Now().Add(joinTimeout)
	for time.Now().Before(deadline) {
		if network.HasUsableAddress(interfaceName) {
			log.Noticef("joined %q and has an address; setup is finished", ssid)
			self.remember(interfaceName, ssid, passphrase)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}

	log.Warningf("joined nothing usable on %q within %s, so the setup network is coming back", ssid, joinTimeout)
	self.recover(ctx, interfaceName, fmt.Sprintf(
		"%q did not accept that password, or did not give this device an address.", ssid))
	return fmt.Errorf("onboarding: %q did not work", ssid)
}

// recover brings the setup network back after a join that did not work, so
// that the phone can reconnect and try again.
func (self *Onboarding) recover(ctx context.Context, interfaceName, trouble string) {
	// The network that did not work is forgotten first. Left in place,
	// wpa_supplicant keeps trying it, which keeps the radio busy and stops the
	// access point coming back.
	if err := network.Forget(interfaceName); err != nil {
		log.Warningf("cannot forget the network that did not work: %s", err)
	}
	if err := self.Start(ctx, interfaceName); err != nil {
		log.Errorf("cannot bring the setup network back after a failed join, so this "+
			"device now needs somebody to go to it: %s", err)
		return
	}
	self.mutex.Lock()
	self.trouble = trouble
	self.mutex.Unlock()
}

// remember writes the network that worked into the configuration, so that the
// device comes back onto it after a restart.
func (self *Onboarding) remember(interfaceName, ssid, passphrase string) {
	settings := config.Interface{
		Name:     interfaceName,
		Method:   "dhcp",
		Wireless: &config.Wireless{SSID: ssid, Passphrase: config.Secret(passphrase)},
	}

	err := self.store.Update(func(configuration *config.Configuration) error {
		// Managing the network is off by default, because a daemon that
		// reconfigures the network of a machine it was only asked to put a
		// picture on is a surprise nobody wants. Setting this device up over
		// the air is that permission being given, explicitly, by somebody
		// standing in front of the screen.
		configuration.Network.Manage = true

		for index := range configuration.Network.Interfaces {
			if configuration.Network.Interfaces[index].Name == interfaceName {
				configuration.Network.Interfaces[index] = settings
				return nil
			}
		}
		configuration.Network.Interfaces = append(configuration.Network.Interfaces, settings)
		return nil
	})
	if err != nil {
		log.Errorf("this device joined %q but the network could not be written to its "+
			"configuration, so it will not come back after a restart: %s", ssid, err)
	}
}
