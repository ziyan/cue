package network

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// wpa_supplicant is spoken to over a Unix datagram socket in its control
// directory, one per interface. The protocol is plain text: a command is sent,
// a reply comes back. It is the same interface wpa_cli presents, and speaking
// it directly means the image needs no wpa_cli and the daemon gets replies it
// can parse rather than output it has to scrape.
const controlDirectory = "/run/wpa_supplicant"

// controlTimeout bounds a single exchange. wpa_supplicant answers immediately
// for everything here except SCAN_RESULTS on a busy band.
const controlTimeout = 5 * time.Second

// control is one conversation with wpa_supplicant about one interface.
type control struct {
	connection *net.UnixConn
	ourAddress string
}

// openControl connects to the supplicant's socket for an interface.
//
// The client end needs a filesystem path of its own — the protocol replies to
// the address it was sent from — so one is made in a directory that is
// certainly writable and removed afterwards.
func openControl(interfaceName string) (*control, error) {
	socket := filepath.Join(controlDirectory, interfaceName)
	if _, err := os.Stat(socket); err != nil {
		return nil, fmt.Errorf("network: wpa_supplicant is not running for %s: %w", interfaceName, err)
	}

	ourAddress := filepath.Join(os.TempDir(),
		fmt.Sprintf("cue-wpa-%s-%d", interfaceName, os.Getpid()))
	_ = os.Remove(ourAddress)

	connection, err := net.DialUnix("unixgram",
		&net.UnixAddr{Name: ourAddress, Net: "unixgram"},
		&net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return nil, fmt.Errorf("network: cannot reach wpa_supplicant for %s: %w", interfaceName, err)
	}

	return &control{connection: connection, ourAddress: ourAddress}, nil
}

func (self *control) Close() {
	_ = self.connection.Close()
	_ = os.Remove(self.ourAddress)
}

// ask sends one command and returns the reply.
func (self *control) ask(command string) (string, error) {
	if err := self.connection.SetDeadline(time.Now().Add(controlTimeout)); err != nil {
		return "", err
	}
	if _, err := self.connection.Write([]byte(command)); err != nil {
		return "", fmt.Errorf("network: cannot send %q to wpa_supplicant: %w", command, err)
	}

	// Scan results on a busy band run to several kilobytes.
	buffer := make([]byte, 64*1024)
	count, err := self.connection.Read(buffer)
	if err != nil {
		return "", fmt.Errorf("network: no answer to %q from wpa_supplicant: %w", command, err)
	}

	reply := string(buffer[:count])
	if strings.HasPrefix(reply, "FAIL") {
		return "", fmt.Errorf("network: wpa_supplicant refused %q", command)
	}
	return reply, nil
}

// mustSucceed is for the commands that answer only OK or FAIL.
func (self *control) mustSucceed(command string) error {
	reply, err := self.ask(command)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(reply, "OK") {
		return fmt.Errorf("network: wpa_supplicant answered %q to %q", strings.TrimSpace(reply), command)
	}
	return nil
}

// wirelessStatus asks what the interface is currently doing.
func wirelessStatus(interfaceName string) (*WirelessStatus, error) {
	connection, err := openControl(interfaceName)
	if err != nil {
		return nil, err
	}
	defer connection.Close()

	reply, err := connection.ask("STATUS")
	if err != nil {
		return nil, err
	}

	fields := parseKeyValues(reply)
	status := &WirelessStatus{
		State: fields["wpa_state"],
		SSID:  fields["ssid"],
		BSSID: fields["bssid"],
	}
	status.Frequency, _ = strconv.Atoi(fields["freq"])

	// The signal is not in STATUS; it comes from the current network's entry
	// in the scan results, which wpa_supplicant keeps up to date.
	if status.BSSID != "" {
		if signal, err := connection.ask("BSS " + status.BSSID); err == nil {
			values := parseKeyValues(signal)
			status.SignalStrength, _ = strconv.Atoi(values["level"])
		}
	}
	return status, nil
}

// Scan asks an interface to look for networks and returns what it found.
//
// A scan takes a moment and the results arrive separately, so this asks for
// one and then reads the list; the list is whatever the supplicant last saw,
// which after a scan is what is in the room.
func Scan(interfaceName string) ([]WirelessNetwork, error) {
	connection, err := openControl(interfaceName)
	if err != nil {
		return nil, err
	}
	defer connection.Close()

	// A scan already in progress answers FAIL-BUSY, which is not a failure:
	// the results are about to be fresh anyway.
	if _, err := connection.ask("SCAN"); err != nil {
		log.Debugf("scan on %s: %s", interfaceName, err)
	}

	// Radios take a second or two to sweep the bands.
	time.Sleep(2 * time.Second)

	reply, err := connection.ask("SCAN_RESULTS")
	if err != nil {
		return nil, err
	}
	return parseScanResults(reply), nil
}

// parseScanResults reads the table wpa_supplicant returns:
//
//	bssid / frequency / signal level / flags / ssid
//	00:11:22:33:44:55	2462	-45	[WPA2-PSK-CCMP][ESS]	Example
func parseScanResults(reply string) []WirelessNetwork {
	var networks []WirelessNetwork
	best := map[string]int{}

	for index, line := range strings.Split(reply, "\n") {
		if index == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			continue
		}

		network := WirelessNetwork{
			BSSID:    fields[0],
			SSID:     fields[4],
			Security: securityFrom(fields[3]),
		}
		network.Frequency, _ = strconv.Atoi(fields[1])
		network.SignalStrength, _ = strconv.Atoi(fields[2])

		if network.SSID == "" {
			// A hidden network. There is nothing useful to show and nothing
			// an operator could pick.
			continue
		}

		// One entry per name, the strongest. An access point with three
		// radios is one network to the person choosing it.
		if existing, found := best[network.SSID]; found {
			if networks[existing].SignalStrength >= network.SignalStrength {
				continue
			}
			networks[existing] = network
			continue
		}
		best[network.SSID] = len(networks)
		networks = append(networks, network)
	}

	sort.Slice(networks, func(first, second int) bool {
		return networks[first].SignalStrength > networks[second].SignalStrength
	})
	return networks
}

// The security a network needs to be joined.
const (
	SecurityOpen       = "open"
	SecurityPreShared  = "wpa-psk"
	SecurityEnterprise = "enterprise"
)

// securityFrom reads the flags column. Only two kinds can be joined with what
// an operator can type into a form: nothing, and a password.
func securityFrom(flags string) string {
	switch {
	case strings.Contains(flags, "EAP"):
		return SecurityEnterprise
	case strings.Contains(flags, "PSK"), strings.Contains(flags, "SAE"):
		return SecurityPreShared
	case strings.Contains(flags, "WEP"):
		// Joinable in principle. Reported as enterprise so that the interface
		// does not offer it: a network still using WEP is one to fix, not to
		// put a screen on.
		return SecurityEnterprise
	default:
		return SecurityOpen
	}
}

// Join puts an interface on a wireless network and makes it the only one it
// will use.
//
// Every other configured network is removed first. A display belongs on one
// network, and a supplicant that quietly falls back to a network somebody
// configured months ago is worse than one that fails: the screen works, on the
// wrong network, and nothing says so.
func Join(interfaceName, ssid, passphrase string) error {
	if ssid == "" {
		return fmt.Errorf("network: a wireless network needs a name")
	}

	connection, err := openControl(interfaceName)
	if err != nil {
		return err
	}
	defer connection.Close()

	existing, err := connection.ask("LIST_NETWORKS")
	if err != nil {
		return err
	}
	for _, identifier := range networkIdentifiers(existing) {
		if err := connection.mustSucceed("REMOVE_NETWORK " + identifier); err != nil {
			log.Debugf("cannot remove an old wireless network: %s", err)
		}
	}

	created, err := connection.ask("ADD_NETWORK")
	if err != nil {
		return err
	}
	identifier := strings.TrimSpace(created)

	if err := connection.mustSucceed(fmt.Sprintf("SET_NETWORK %s ssid %s", identifier, quote(ssid))); err != nil {
		return err
	}

	if passphrase == "" {
		if err := connection.mustSucceed(fmt.Sprintf("SET_NETWORK %s key_mgmt NONE", identifier)); err != nil {
			return err
		}
	} else {
		if len(passphrase) < 8 || len(passphrase) > 63 {
			return fmt.Errorf("network: a wireless password is between 8 and 63 characters")
		}
		if err := connection.mustSucceed(fmt.Sprintf("SET_NETWORK %s psk %s", identifier, quote(passphrase))); err != nil {
			return err
		}
	}

	if err := connection.mustSucceed("ENABLE_NETWORK " + identifier); err != nil {
		return err
	}
	// Written to the supplicant's own configuration so that the machine comes
	// back onto the network after a power cut without anybody visiting it.
	if err := connection.mustSucceed("SAVE_CONFIG"); err != nil {
		log.Warningf("joined %s but could not save it; it will be forgotten on a restart: %s", ssid, err)
	}

	log.Noticef("joining the wireless network %q on %s", ssid, interfaceName)
	return nil
}

// networkIdentifiers reads the first column of LIST_NETWORKS, skipping its
// header.
func networkIdentifiers(reply string) []string {
	var identifiers []string
	for index, line := range strings.Split(reply, "\n") {
		if index == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) > 0 && fields[0] != "" {
			identifiers = append(identifiers, fields[0])
		}
	}
	return identifiers
}

// quote wraps a value the way wpa_supplicant expects, and refuses one that
// would end the string early. A network name is chosen by somebody else and
// arrives here as text.
func quote(value string) string {
	replaced := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `"` + replaced + `"`
}

// parseKeyValues reads the name=value lines wpa_supplicant answers with.
func parseKeyValues(reply string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(reply, "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			values[name] = value
		}
	}
	return values
}
