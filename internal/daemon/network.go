package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ziyan/cue/internal/config"
	"github.com/ziyan/cue/internal/network"
	"github.com/ziyan/cue/internal/util/deferutil"
)

// Setting the network up from the screen itself.
//
// This is the one kind of configuration the menu on the screen offers, and it
// is there for a plain reason: it is the configuration you cannot do from the
// web interface, because reaching the web interface is what it is for. A
// screen on a wired network with no DHCP server needs a fixed address, and
// today that means somebody with a laptop, a cable and a way in.
//
// Everything else -- the playlist, the timezone, the password -- stays in the
// web interface, where there is a keyboard and room to think.

// InterfacesForSetup is what the menu offers to configure: the ones with
// hardware behind them, with what each is doing now.
func (self *Daemon) InterfacesForSetup() []network.Interface {
	found, err := network.Interfaces()
	if err != nil {
		return nil
	}

	var physical []network.Interface
	for _, one := range found {
		if one.Physical {
			physical = append(physical, one)
		}
	}
	return physical
}

// ScanForNetworks looks for wireless networks in range.
//
// It works whether or not this device is managing the interface: with a
// supplicant running it asks that one, and without it starts one of its own
// for as long as the scan takes. Somebody at the screen should not have to
// know which case they are in.
func (self *Daemon) ScanForNetworks(interfaceName string) ([]network.WirelessNetwork, error) {
	if interfaceName == "" {
		interfaceName = network.AccessPointCapableInterface()
	}
	if interfaceName == "" {
		return nil, fmt.Errorf("daemon: this device has no wireless hardware")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return network.ScanStandalone(ctx, self.store, interfaceName)
}

// JoinWireless puts this device on a wireless network chosen at the screen.
func (self *Daemon) JoinWireless(interfaceName, ssid, passphrase string) error {
	if strings.TrimSpace(ssid) == "" {
		return fmt.Errorf("daemon: choose a network first")
	}
	if interfaceName == "" {
		interfaceName = network.AccessPointCapableInterface()
	}
	if interfaceName == "" {
		return fmt.Errorf("daemon: this device has no wireless hardware")
	}

	settings := config.Interface{
		Name:     interfaceName,
		Method:   "dhcp",
		Wireless: &config.Wireless{SSID: ssid, Passphrase: config.Secret(passphrase)},
	}
	if err := self.rememberInterface(settings); err != nil {
		return err
	}

	log.Noticef("somebody at the screen put this device on %q", ssid)
	self.applyNetworkSoon()
	return nil
}

// ConfigureWired sets a wired interface to ask for an address or to use one it
// is given here.
func (self *Daemon) ConfigureWired(settings config.Interface) error {
	if settings.Name == "" {
		return fmt.Errorf("daemon: choose an interface first")
	}
	switch settings.Method {
	case "dhcp":
		// Nothing else is meaningful, and leaving a stale address behind would
		// be applied alongside the lease.
		settings.Address, settings.Gateway = "", ""
	case "static":
		if settings.Address == "" {
			return fmt.Errorf("daemon: a fixed address needs an address, such as 192.0.2.10/24")
		}
	default:
		return fmt.Errorf("daemon: %q is neither dhcp nor static", settings.Method)
	}
	settings.Wireless = nil

	if err := self.rememberInterface(settings); err != nil {
		return err
	}

	log.Noticef("somebody at the screen configured %s for %s", settings.Name, settings.Method)
	self.applyNetworkSoon()
	return nil
}

// rememberInterface writes one interface into the configuration, replacing
// whatever was there for it, and switches network management on.
//
// Managing the network is off by default, because a daemon that reconfigures
// the network of a machine it was only asked to put a picture on is a
// surprise. Somebody standing at the screen setting one up is that permission
// being given, in person.
func (self *Daemon) rememberInterface(settings config.Interface) error {
	err := self.store.Update(func(configuration *config.Configuration) error {
		configuration.Network.Manage = true
		for index := range configuration.Network.Interfaces {
			if configuration.Network.Interfaces[index].Name == settings.Name {
				configuration.Network.Interfaces[index] = settings
				return nil
			}
		}
		configuration.Network.Interfaces = append(configuration.Network.Interfaces, settings)
		return nil
	})
	if err != nil {
		return fmt.Errorf("daemon: cannot write the network down: %w", err)
	}
	return nil
}

// applyNetworkSoon acts on what was just written, without making the page that
// asked wait for it.
//
// The request came from the screen's own browser, over the very network being
// reconfigured. Waiting would mean answering into a connection that is about
// to be taken down.
func (self *Daemon) applyNetworkSoon() {
	go func() {
		defer deferutil.Recover()

		ctx := context.Background()
		if self.onboarding != nil && self.onboarding.Running() {
			// The setup network is holding the radio. It has to let go before
			// anything can be joined with it.
			self.onboarding.Finish(ctx)
			self.browser.Refresh(ctx)
		}

		self.mutex.Lock()
		self.lastReachable = time.Now()
		self.mutex.Unlock()

		self.network.ReconcileNow(ctx)
	}()
}
