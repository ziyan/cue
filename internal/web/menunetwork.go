package web

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/ziyan/cue/internal/config"
)

// Setting the network up from the menu on the screen.
//
// The rest of the menu offers actions and no settings, deliberately: a screen
// somebody can reconfigure by walking up to it is a screen anybody can
// reconfigure by walking up to it. The network is the exception, and it earns
// it -- the web interface is where settings belong, and reaching the web
// interface is exactly what this is for. A screen on a wired network with no
// DHCP server needs a fixed address, and without this that means somebody with
// a laptop, a cable, and a way in.

// menuNetwork is what the machine has and what each interface is doing.
func (self *Server) menuNetwork(response http.ResponseWriter, request *http.Request) {
	interfaces := self.device.InterfacesForSetup()

	configured := map[string]config.Interface{}
	for _, one := range self.store.Current().Network.Interfaces {
		configured[one.Name] = one
	}

	listed := make([]map[string]interface{}, 0, len(interfaces))
	for _, one := range interfaces {
		entry := map[string]interface{}{
			"name":      one.Name,
			"kind":      one.Kind,
			"addresses": one.Addresses,
			"up":        one.Up,
		}
		if settings, found := configured[one.Name]; found {
			entry["method"] = settings.Method
			entry["address"] = settings.Address
			entry["gateway"] = settings.Gateway
			if settings.Wireless != nil {
				entry["ssid"] = settings.Wireless.SSID
			}
		}
		listed = append(listed, entry)
	}

	writeJSON(response, http.StatusOK, map[string]interface{}{"interfaces": listed})
}

// menuScan looks for wireless networks in range.
func (self *Server) menuScan(response http.ResponseWriter, request *http.Request) {
	var asked struct {
		Interface string `json:"interface"`
	}
	_ = json.NewDecoder(request.Body).Decode(&asked)

	found, err := self.device.ScanForNetworks(asked.Interface)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}

	// Strongest first, and once each: a network with two access points is seen
	// twice, and offering it twice makes somebody wonder which is theirs.
	sort.SliceStable(found, func(first, second int) bool {
		return found[first].SignalStrength > found[second].SignalStrength
	})
	seen := map[string]bool{}
	networks := make([]map[string]interface{}, 0, len(found))
	for _, one := range found {
		if one.SSID == "" || seen[one.SSID] {
			continue
		}
		seen[one.SSID] = true
		networks = append(networks, map[string]interface{}{
			"ssid":     one.SSID,
			"secured":  one.Security != "open",
			"joinable": one.Security != "enterprise",
			"bars":     signalBars(one.SignalStrength),
		})
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"networks": networks})
}

// menuJoinWireless puts this device on a network somebody chose at the screen.
//
// It answers before the join finishes. The request came over the very network
// being reconfigured, and waiting would mean answering into a connection that
// is about to be taken down.
func (self *Server) menuJoinWireless(response http.ResponseWriter, request *http.Request) {
	var wanted struct {
		Interface  string `json:"interface"`
		SSID       string `json:"ssid"`
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(request.Body).Decode(&wanted); err != nil {
		writeError(response, http.StatusBadRequest, "that is not a network to join")
		return
	}

	if err := self.device.JoinWireless(wanted.Interface, wanted.SSID, wanted.Passphrase); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"joining": wanted.SSID})
}

// menuConfigureWired sets a wired interface to ask for an address or to use a
// fixed one.
func (self *Server) menuConfigureWired(response http.ResponseWriter, request *http.Request) {
	var wanted struct {
		Interface   string   `json:"interface"`
		Method      string   `json:"method"`
		Address     string   `json:"address"`
		Gateway     string   `json:"gateway"`
		Nameservers []string `json:"nameservers"`
	}
	if err := json.NewDecoder(request.Body).Decode(&wanted); err != nil {
		writeError(response, http.StatusBadRequest, "that is not an interface to configure")
		return
	}

	settings := config.Interface{
		Name:        wanted.Interface,
		Method:      wanted.Method,
		Address:     wanted.Address,
		Gateway:     wanted.Gateway,
		Nameservers: wanted.Nameservers,
	}
	if err := self.device.ConfigureWired(settings); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"configured": wanted.Interface})
}
