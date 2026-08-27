package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Problem is one thing wrong with the configuration, named by its path in the
// YAML file so that an operator can find it without counting lines.
type Problem struct {
	Path    string
	Message string
}

func (self Problem) Error() string {
	return fmt.Sprintf("%s: %s", self.Path, self.Message)
}

// Problems is every problem found in one pass. Validation reports all of them
// at once on purpose: a device that will not start is usually misconfigured in
// more than one place, and fixing them one restart at a time is miserable when
// the only way to see the message is over a serial console.
type Problems []Problem

func (self Problems) Error() string {
	if len(self) == 1 {
		return "config: " + self[0].Error()
	}
	lines := make([]string, 0, len(self)+1)
	lines = append(lines, fmt.Sprintf("config: %d problems:", len(self)))
	for _, problem := range self {
		lines = append(lines, "  "+problem.Error())
	}
	return strings.Join(lines, "\n")
}

// Validate reports every problem with the configuration. A configuration that
// validates is one the daemon is willing to run; it is not a promise that the
// hardware exists, because a monitor that is unplugged is not a mistake.
func (self *Configuration) Validate() error {
	if !self.Network.Onboarding.Valid() {
		return fmt.Errorf("config: network.onboarding is %q; it must be auto, always or off",
			self.Network.Onboarding)
	}

	var problems Problems
	add := func(path, format string, arguments ...interface{}) {
		problems = append(problems, Problem{Path: path, Message: fmt.Sprintf(format, arguments...)})
	}

	if self.Device.Name == "" {
		add("device.name", "must not be empty")
	}
	if self.Device.Timezone != "" {
		if _, err := time.LoadLocation(self.Device.Timezone); err != nil {
			add("device.timezone", "%q is not a known timezone", self.Device.Timezone)
		}
	}

	if self.Paths.State == "" {
		add("paths.state", "must not be empty")
	}
	if self.Paths.Runtime == "" {
		add("paths.runtime", "must not be empty")
	}

	switch self.Display.Server {
	case ServerXorg, ServerXvfb:
	default:
		add("display.server", "must be %q or %q, not %q", ServerXorg, ServerXvfb, self.Display.Server)
	}
	if self.Display.Number < 0 || self.Display.Number > 99 {
		add("display.number", "must be between 0 and 99")
	}
	if self.Display.VirtualTerminal < 0 || self.Display.VirtualTerminal > 63 {
		add("display.virtualTerminal", "must be between 0 and 63; 0 lets the X server choose")
	}
	if self.Display.Framebuffer != "" {
		if _, _, err := ParseSize(self.Display.Framebuffer); err != nil {
			add("display.framebuffer", "%q is not a size like 1920x1080", self.Display.Framebuffer)
		}
	}
	if self.Display.Modeline != "" {
		if err := validateModeline(self.Display.Modeline); err != nil {
			add("display.modeline", "%s", err)
		}
	}
	if self.Display.ReconcileInterval < 0 {
		add("display.reconcileInterval", "must not be negative")
	}
	seenOutputs := map[string]bool{}
	for index, output := range self.Display.Outputs {
		path := fmt.Sprintf("display.outputs[%d]", index)
		if output.Name == "" {
			add(path+".name", "must not be empty; use \"*\" to match every output")
		}
		if seenOutputs[output.Name] {
			add(path+".name", "%q appears more than once", output.Name)
		}
		seenOutputs[output.Name] = true
		switch output.Mode {
		case ModePreferred, ModeOff, "":
		default:
			if _, _, err := ParseSize(output.Mode); err != nil {
				add(path+".mode", "must be %q, %q or a size like 1920x1080, not %q", ModePreferred, ModeOff, output.Mode)
			}
		}
		if output.Position != "" {
			if _, _, err := ParseSize(output.Position); err != nil {
				add(path+".position", "%q is not a position like 0x0", output.Position)
			}
		}
		if output.Rotate != "" && !slices.Contains(rotations, output.Rotate) {
			add(path+".rotate", "must be one of %s, not %q", strings.Join(rotations, ", "), output.Rotate)
		}
		if output.Rate < 0 {
			add(path+".rate", "must not be negative")
		}
	}

	if !self.Display.Cursor.Valid() {
		add("display.cursor", "must be hidden, auto or always")
	}

	if self.Browser.Binary == "" {
		add("browser.binary", "must not be empty")
	}

	if scale := self.Browser.DeviceScaleFactor; scale < 0 || scale > 4 {
		add("browser.deviceScaleFactor", "must be between 0 and 4; 1 gives a page the pixels the screen has, "+
			"and 0 lets the browser work it out from what the panel says its DPI is")
	}

	if self.Playlist.Interval < 0 {
		add("playlist.interval", "must not be negative")
	}
	seenItems := map[string]bool{}
	for index, item := range self.Playlist.Items {
		path := fmt.Sprintf("playlist.items[%d]", index)
		if item.Identifier != "" {
			if seenItems[item.Identifier] {
				add(path+".identifier", "%q appears more than once", item.Identifier)
			}
			seenItems[item.Identifier] = true
		}
		if item.URL == "" {
			add(path+".url", "must not be empty")
		} else if parsed, err := url.Parse(item.URL); err != nil {
			add(path+".url", "%q is not a valid address: %s", item.URL, err)
		} else if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "file" && parsed.Scheme != "data" {
			add(path+".url", "must be an http, https, file or data address, not %q", parsed.Scheme)
		}
		if item.Duration < 0 {
			add(path+".duration", "must not be negative")
		}
		if item.Login != nil {
			validateLogin(item.Login, path+".login", add)
		}
		for dismissIndex, dismiss := range item.Dismiss {
			dismissPath := fmt.Sprintf("%s.dismiss[%d]", path, dismissIndex)
			if dismiss.Selector == "" {
				add(dismissPath+".selector", "must not be empty")
			}
			if dismiss.WhenTextMatches != "" {
				if _, err := regexp.Compile(dismiss.WhenTextMatches); err != nil {
					add(dismissPath+".whenTextMatches", "%q is not a valid regular expression: %s", dismiss.WhenTextMatches, err)
				}
			}
		}
	}

	if self.Watchdog.Enabled {
		if self.Watchdog.Interval <= 0 {
			add("watchdog.interval", "must be greater than zero when the watchdog is enabled")
		}
		if self.Watchdog.Timeout <= 0 {
			add("watchdog.timeout", "must be greater than zero when the watchdog is enabled")
		}
		if self.Watchdog.Timeout >= self.Watchdog.Interval && self.Watchdog.Interval > 0 {
			// Otherwise a probe is still running when the next one is due and
			// the failure count climbs on a display that is merely slow.
			add("watchdog.timeout", "must be shorter than watchdog.interval (%s)", self.Watchdog.Interval)
		}
		steps := []struct {
			path  string
			value int
		}{
			{"watchdog.failuresBeforeReload", self.Watchdog.FailuresBeforeReload},
			{"watchdog.failuresBeforeRecreate", self.Watchdog.FailuresBeforeRecreate},
			{"watchdog.failuresBeforeClearCache", self.Watchdog.FailuresBeforeClearCache},
			{"watchdog.failuresBeforeRestart", self.Watchdog.FailuresBeforeRestart},
			{"watchdog.failuresBeforeRestartDisplay", self.Watchdog.FailuresBeforeRestartDisplay},
		}
		previous := 0
		for _, step := range steps {
			if step.value <= 0 {
				add(step.path, "must be greater than zero")
				continue
			}
			if step.value <= previous {
				// The ladder has to go up, or a later, heavier step would fire
				// before an earlier, cheaper one ever got a chance.
				add(step.path, "must be greater than the step before it (%d)", previous)
			}
			previous = step.value
		}
	}

	if self.VNC.Enabled {
		if err := validateListen(self.VNC.Listen); err != nil {
			add("vnc.listen", "%s", err)
		}
	}

	if err := validateListen(self.Web.Listen); err != nil {
		add("web.listen", "%s", err)
	}
	if self.Web.SessionLifetime <= 0 {
		add("web.sessionLifetime", "must be greater than zero")
	}

	if self.Network.Manage {
		if self.Network.ReconcileInterval < 0 {
			add("network.reconcileInterval", "must not be negative")
		}
		seenInterfaces := map[string]bool{}
		for index, netInterface := range self.Network.Interfaces {
			path := fmt.Sprintf("network.interfaces[%d]", index)
			if netInterface.Name == "" {
				add(path+".name", "must name an interface, for example eth0 or wlan0")
			}
			if seenInterfaces[netInterface.Name] {
				add(path+".name", "%q appears more than once", netInterface.Name)
			}
			seenInterfaces[netInterface.Name] = true

			switch netInterface.Method {
			case AddressMethodDHCP, AddressMethodStatic, "":
			default:
				add(path+".method", "must be %q or %q, not %q", AddressMethodDHCP, AddressMethodStatic, netInterface.Method)
			}

			if netInterface.Method == AddressMethodStatic {
				if netInterface.Address == "" {
					add(path+".address", "a static interface needs an address, for example 192.0.2.10/24")
				} else if _, _, err := net.ParseCIDR(netInterface.Address); err != nil {
					add(path+".address", "%q is not an address and prefix length like 192.0.2.10/24", netInterface.Address)
				}
			} else if netInterface.Address != "" {
				add(path+".address", "an address is only used with the %q method", AddressMethodStatic)
			}

			if netInterface.Gateway != "" && net.ParseIP(netInterface.Gateway) == nil {
				add(path+".gateway", "%q is not an address", netInterface.Gateway)
			}
			for serverIndex, server := range netInterface.Nameservers {
				if net.ParseIP(server) == nil {
					add(fmt.Sprintf("%s.nameservers[%d]", path, serverIndex), "%q is not an address", server)
				}
			}

			if netInterface.Wireless != nil {
				if netInterface.Wireless.SSID == "" {
					add(path+".wireless.ssid", "must name the network to join")
				}
				if length := len(netInterface.Wireless.Passphrase); length > 0 && (length < 8 || length > 63) {
					add(path+".wireless.passphrase", "must be between 8 and 63 characters, or empty for an open network")
				}
			}
		}
	}

	if self.Audio.Volume < 0 || self.Audio.Volume > 100 {
		add("audio.volume", "must be a percentage between 0 and 100")
	}

	if self.Time.Enabled && len(self.Time.Servers) == 0 {
		add("time.servers", "must name at least one server when time synchronisation is enabled")
	}

	if len(problems) > 0 {
		return problems
	}
	return nil
}

func validateLogin(login *Login, path string, add func(string, string, ...interface{})) {
	if login.WhenURLMatches == "" && login.WhenSelectorExists == "" {
		add(path, "needs whenUrlMatches or whenSelectorExists, otherwise there is no way to tell the login page from the page itself")
	}
	if login.WhenURLMatches != "" {
		if _, err := regexp.Compile(login.WhenURLMatches); err != nil {
			add(path+".whenUrlMatches", "%q is not a valid regular expression: %s", login.WhenURLMatches, err)
		}
	}
	if login.ExpectURLMatches != "" {
		if _, err := regexp.Compile(login.ExpectURLMatches); err != nil {
			add(path+".expectUrlMatches", "%q is not a valid regular expression: %s", login.ExpectURLMatches, err)
		}
	}
	if login.PasswordSelector == "" {
		add(path+".passwordSelector", "must not be empty")
	}
	if login.UsernameSelector != "" && login.Username == "" {
		add(path+".username", "must not be empty when a username field is named")
	}
	if !login.Password.IsSet() {
		add(path+".password", "must not be empty")
	}
	if login.MinimumInterval < 0 {
		add(path+".minimumInterval", "must not be negative")
	}
}

func validateListen(address string) error {
	if address == "" {
		return fmt.Errorf("must not be empty")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%q is not an address like :8080 or 127.0.0.1:8080", address)
	}
	if host != "" {
		if net.ParseIP(host) == nil {
			return fmt.Errorf("%q is not an address to bind to", host)
		}
	}
	number, err := strconv.Atoi(port)
	if err != nil || number <= 0 || number > 65535 {
		return fmt.Errorf("%q is not a port number", port)
	}
	return nil
}

// modelinePattern is a pixel clock followed by eight whole numbers and the two
// sync polarities, which is what xrandr --newmode takes and what a television
// with a broken EDID has to be told.
var modelinePattern = regexp.MustCompile(`^\s*[0-9]+(\.[0-9]+)?(\s+[0-9]+){8}(\s+[-+](hsync|vsync)){0,2}\s*$`)

func validateModeline(modeline string) error {
	if !modelinePattern.MatchString(modeline) {
		return fmt.Errorf("must be a modeline like \"173.00 1920 2048 2248 2576 1080 1083 1088 1120 -hsync +vsync\"")
	}
	return nil
}

// ParseSize splits "1920x1080" into its two numbers. It is used for the
// framebuffer size, for an output's mode and for an output's position, all of
// which are written the same way.
func ParseSize(value string) (int, int, error) {
	first, second, found := strings.Cut(strings.TrimSpace(value), "x")
	if !found {
		return 0, 0, fmt.Errorf("config: %q is not two numbers separated by an x", value)
	}
	width, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil {
		return 0, 0, fmt.Errorf("config: %q is not a number", first)
	}
	height, err := strconv.Atoi(strings.TrimSpace(second))
	if err != nil {
		return 0, 0, fmt.Errorf("config: %q is not a number", second)
	}
	return width, height, nil
}
