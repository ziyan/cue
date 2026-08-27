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

	// manager owns the machine's network once setup has chosen one: it starts
	// the supplicant, joins, asks for an address, and brings it all back after
	// a restart. Setting a device up over the air ends by writing the
	// configuration and letting it do that.
	manager *network.Manager

	// portal serves the setup page on the setup network's own address, on
	// port 80. The web server provides it; it is a function rather than the
	// server itself so that this package does not depend on that one.
	portal func(ctx context.Context, address string) error

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

func New(store *config.Store, manager *network.Manager) *Onboarding {
	return &Onboarding{store: store, manager: manager}
}

// ServePortalWith says how to serve the setup page on the setup network. The
// daemon calls this with the web server's own method once both exist.
func (self *Onboarding) ServePortalWith(serve func(ctx context.Context, address string) error) {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	self.portal = serve
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

	// Invented once per session and then kept, even across the access point
	// going down and coming back.
	//
	// This matters more than it looks. The network comes down and back up
	// twice in normal use: when somebody asks to scan again, and when a join
	// fails and setup has to be recovered. If the name and passphrase changed
	// each time, the phone could not rejoin -- it would be looking for a
	// network that no longer exists, and the new one's passphrase is on a
	// screen the person may have already walked away from. The whole recovery
	// story depends on the setup network coming back as the same network.
	credentials, err := self.sessionCredentials()
	if err != nil {
		return err
	}

	// Best effort: a scan that fails leaves an empty list, and the portal
	// offers a "scan again" button. Refusing to start setup because a scan
	// failed would leave the person with nothing at all.
	found, err := network.ScanStandalone(ctx, self.store, interfaceName)
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

	// Port 80 on the setup address, because that is where a phone looks. It
	// fetches its vendor's address on port 80 to decide whether the network
	// reaches the internet, and the name server has just told it that address
	// is here. With nothing listening there the probe is refused and the phone
	// shows no page at all -- somebody joins the network and then sits looking
	// at a phone doing nothing.
	if self.portal != nil {
		go func() {
			address := onboarding.DeviceAddress.String() + ":80"
			if err := self.portal(running, address); err != nil && running.Err() == nil {
				log.Errorf("the setup page stopped being served: %s", err)
			}
		}()
	}

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

// Stop takes the setup network down.
//
// The name and passphrase are kept, because the network usually comes back:
// this is called to free the radio for a scan, and to free it for an attempt
// to join. Finish is what ends a session and forgets them.
func (self *Onboarding) Stop(ctx context.Context) {
	self.mutex.Lock()
	point := self.point
	stop := self.stop
	self.running = false
	self.point = nil
	self.stop = nil
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

	// The access point comes down first. A radio that is advertising cannot
	// search the other channels, and a supplicant in access point mode refuses
	// a scan outright, so asking it while it is up returns nothing at all --
	// which would look to somebody pressing the button like an empty room.
	self.Stop(ctx)

	found, scanErr := network.ScanStandalone(ctx, self.store, interfaceName)

	// Back up whatever the scan did, because a device left with neither its
	// own network nor anybody else's is a device somebody has to walk to.
	if err := self.Start(ctx, interfaceName); err != nil {
		return fmt.Errorf("onboarding: the setup network did not come back after "+
			"looking for networks: %w", err)
	}
	if scanErr != nil {
		return scanErr
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

	// Written into the configuration first, and the network manager asked to
	// act on it, rather than joining by hand here.
	//
	// The first version did it by hand and could not work. It stopped the
	// access point -- which is the only wpa_supplicant on the interface -- and
	// then asked that same supplicant to join, so every attempt failed
	// immediately with "wpa_supplicant is not running". Even had it associated
	// there would have been no address, because nothing had asked for one.
	//
	// The manager already knows how to start a supplicant, join, run a DHCP
	// client and keep all of it up across a reboot. Writing the configuration
	// and letting it work is both shorter and the thing that has to happen
	// anyway: a device set up over the air must come back on that network
	// after a power cut.
	self.remember(interfaceName, ssid, passphrase)
	if self.manager != nil {
		self.manager.ReconcileNow(ctx)
	}

	deadline := time.Now().Add(joinTimeout)
	for time.Now().Before(deadline) {
		if network.HasUsableAddress(interfaceName) {
			log.Noticef("joined %q and has an address; setup is finished", ssid)
			self.Finish(ctx)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}

	log.Warningf("joined nothing usable on %q within %s, so the setup network is coming back", ssid, joinTimeout)
	self.forget(interfaceName)
	self.recover(ctx, interfaceName, fmt.Sprintf(
		"%q did not accept that password, or did not give this device an address.", ssid))
	return fmt.Errorf("onboarding: %q did not work", ssid)
}

// forget takes a network that did not work back out of the configuration, so
// that the manager stops trying it and the next attempt starts clean.
func (self *Onboarding) forget(interfaceName string) {
	err := self.store.Update(func(configuration *config.Configuration) error {
		kept := configuration.Network.Interfaces[:0]
		for _, one := range configuration.Network.Interfaces {
			if one.Name != interfaceName {
				kept = append(kept, one)
			}
		}
		configuration.Network.Interfaces = kept
		// Management goes back off if this was the only reason it was on: it
		// was switched on by setting this device up, and setting it up did not
		// work.
		if len(kept) == 0 {
			configuration.Network.Manage = false
		}
		return nil
	})
	if err != nil {
		log.Warningf("cannot take the network that did not work back out of the configuration: %s", err)
	}
}

// recover brings the setup network back after a join that did not work, so
// that the phone can reconnect and try again.
func (self *Onboarding) recover(ctx context.Context, interfaceName, trouble string) {
	// Whatever supplicant the manager started is told to forget the network
	// that did not work. Left in place it is retried for ever, which keeps the
	// radio busy and stops the access point coming back. There may be no
	// supplicant at all -- the join may have failed before one started -- and
	// that is not worth a warning.
	if err := network.Forget(interfaceName); err != nil {
		log.Debugf("nothing to forget on %s: %s", interfaceName, err)
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

// sessionCredentials invents a name and passphrase the first time and returns
// the same ones afterwards, so that a setup network which goes down and comes
// back is the same network to a phone that has already joined it.
func (self *Onboarding) sessionCredentials() (network.Credentials, error) {
	self.mutex.Lock()
	existing := self.credentials
	self.mutex.Unlock()

	if existing.SSID != "" {
		return existing, nil
	}

	made, err := network.NewCredentials()
	if err != nil {
		return network.Credentials{}, err
	}

	self.mutex.Lock()
	if self.credentials.SSID == "" {
		self.credentials = made
	}
	made = self.credentials
	self.mutex.Unlock()
	return made, nil
}

// Finish ends a setup session: the network goes down and its passphrase is
// forgotten, so that the next session -- if there ever is one -- is a new
// network with a new password rather than one somebody wrote down last month.
func (self *Onboarding) Finish(ctx context.Context) {
	self.Stop(ctx)

	self.mutex.Lock()
	self.credentials = network.Credentials{}
	self.networks = nil
	self.trouble = ""
	self.mutex.Unlock()
}
