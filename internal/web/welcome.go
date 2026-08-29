package web

import (
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"

	"github.com/ziyan/cue/internal/network"
	"github.com/ziyan/cue/internal/util/qr"
)

// welcome is the page the device shows on its own screen when there is no
// playlist yet.
//
// A screen that is black tells whoever is standing in front of it nothing:
// not whether the machine is on, not whether the software is running, and
// certainly not what to do next. This says all three, and gives the address to
// open. It is built here rather than served as a file because the useful part
// — the address — is only known at run time.
func (self *Server) welcome(response http.ResponseWriter, request *http.Request) {
	configuration := self.store.Current()

	_, port, err := net.SplitHostPort(self.Address())
	if err != nil {
		port = "8080"
	}

	addresses := make([]string, 0, 4)
	for _, address := range machineAddresses() {
		addresses = append(addresses, "http://"+net.JoinHostPort(address, port)+"/")
	}
	if len(addresses) == 0 {
		addresses = append(addresses, "http://<this machine>:"+port+"/")
	}

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	// What the code carries depends on whether this device is running a
	// temporary network for its own setup.
	//
	// When it is, the code carries that network's name and passphrase, and
	// scanning it joins the phone to the device. That passphrase is on this
	// screen and nowhere else -- not in the configuration file, not in the
	// log -- so being able to set this device up means being able to see it,
	// which is the whole security model of setting up a device that has no
	// password yet.
	//
	// When it is not, there is a real network, and the useful thing to carry
	// is the address of the interface, so that scanning opens it.
	setup, onboarding := self.device.SetupNetwork()

	var code template.HTML
	var contents string
	switch {
	case onboarding:
		contents = setup.JoinCode()
	case len(addresses) > 0:
		contents = addresses[0]
	}
	if contents != "" {
		if matrix, err := qr.Encode(contents); err == nil {
			code = renderQR(matrix, "Setup code")
		} else {
			log.Debugf("cannot encode the welcome QR code: %s", err)
		}
	}

	err = welcomeTemplate.Execute(response, map[string]interface{}{
		"Device":     configuration.Device.Name,
		"Identifier": configuration.Device.Identifier,
		"Addresses":  addresses,
		"NeedsSetup": !self.isSetUp(),
		"Code":       code,
		"Onboarding": onboarding,
		"WayBack":    self.wayBack(),
		"SetupSSID":  setup.SSID,
	})
	if err != nil {
		log.Debugf("cannot render the welcome page: %s", err)
	}
}

// machineAddresses lists this machine's own addresses on the network, which
// are what somebody would type into a laptop in the same room. Loopback and
// link-local addresses are left out because typing them into another machine
// reaches nothing.
//
// The addresses of interfaces with no hardware behind them are left out for a
// stronger reason. A machine running containers has a Docker bridge, a veth
// per container and whatever a VPN left behind, and each of those has an
// address that reaches nothing from the laptop in the room. On a developer
// machine this page listed a Docker bridge and a libvirt bridge alongside the
// real one, and since the QR code carries the first address in this list, a
// machine whose bridge happened to sort first would have put an unreachable
// into the code -- a screen telling somebody to scan something that goes
// nowhere.
//
// Physical ones come first for that reason, and the rest are kept only as a
// fallback for a machine where nothing looks physical, which is what a daemon
// running inside a container of its own sees.
func machineAddresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	physical := map[string]bool{}
	if known, err := network.Interfaces(); err == nil {
		for _, one := range known {
			if one.Physical {
				physical[one.Name] = true
			}
		}
	}

	var addresses, fallback []string
	for _, current := range interfaces {
		if current.Flags&net.FlagUp == 0 || current.Flags&net.FlagLoopback != 0 {
			continue
		}
		list, err := current.Addrs()
		if err != nil {
			continue
		}
		for _, entry := range list {
			network, ok := entry.(*net.IPNet)
			if !ok || network.IP.IsLoopback() || network.IP.IsLinkLocalUnicast() {
				continue
			}
			text := network.IP.String()
			if strings.Contains(text, ":") {
				// An address with colons in it has to be bracketed in a URL,
				// and nobody types one at a screen anyway.
				continue
			}
			if physical[current.Name] {
				addresses = append(addresses, text)
			} else {
				fallback = append(fallback, text)
			}
		}
	}
	if len(addresses) == 0 {
		addresses = fallback
	}

	// Three is as many as fits on the screen under the code, and more than
	// three is a wall of numbers nobody reads anyway.
	if len(addresses) > 3 {
		addresses = addresses[:3]
	}
	return addresses
}

// renderQR draws a QR matrix as an inline SVG.
//
// Inline, rather than a PNG served from another route, for three reasons: the
// page is one request with nothing that can fail separately, there is no image
// to encode or cache, and an SVG scales to whatever the screen is without
// going soft. A television at the end of a room and a small monitor an arm's
// length away get the same crisp code.
//
// Each dark module becomes one <rect>. The viewBox is the module count, so the
// page can size the whole thing in whatever units it likes.
// The label is a parameter because two different codes are drawn by this: the
// one that sets a device up and the one that links it to an account. A screen
// reader announcing "setup code" over the linking code is telling somebody who
// cannot see it the wrong thing about the only part of the page that matters.
func renderQR(matrix [][]bool, label string) template.HTML {
	if len(matrix) == 0 {
		return ""
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s" shape-rendering="crispEdges">`,
		len(matrix), len(matrix), template.HTMLEscapeString(label))
	// A white ground under the code. Scanners read dark-on-light, and the page
	// behind this is nearly black.
	fmt.Fprintf(&builder, `<rect width="%d" height="%d" fill="#fff"/>`, len(matrix), len(matrix))
	for row := range matrix {
		for column, dark := range matrix[row] {
			if !dark {
				continue
			}
			// Drawn a hair over one unit wide so that neighbouring modules
			// meet: at some sizes an exact 1 leaves a seam that browsers
			// render as a light line through the code.
			fmt.Fprintf(&builder, `<rect x="%d" y="%d" width="1.02" height="1.02" fill="#000"/>`, column, row)
		}
	}
	builder.WriteString("</svg>")
	return template.HTML(builder.String())
}

// The page is deliberately large, high contrast and centred: it is read from
// across a room, on a screen that may be a television at the far end of it.
var welcomeTemplate = template.Must(template.New("welcome").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{ .Device }}</title>
<style>
  :root { color-scheme: dark; }
  html, body {
    margin: 0; height: 100%;
    background: #0b0d10; color: #e7ecf3;
    font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  }
  body { display: grid; place-items: center; text-align: center; padding: 4vmin; }
  h1 { font-size: 6vmin; font-weight: 600; margin: 0 0 2vmin; letter-spacing: -0.02em; }
  p { font-size: 2.6vmin; margin: 0 0 4vmin; color: #9fb0c5; }
  .address {
    font-family: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
    font-size: 4.4vmin; padding: 1.6vmin 3vmin; margin: 0 auto 1.4vmin;
    display: inline-block; border-radius: 1.2vmin;
    background: #151a21; border: 1px solid #2a323d; color: #7dd3fc;
  }
  .identifier { font-size: 1.8vmin; color: #55637a; margin-top: 5vmin; }
  .code {
    width: 34vmin; height: 34vmin; margin: 0 auto 3vmin;
    padding: 1.6vmin; border-radius: 1.6vmin; background: #fff;
  }
  .code svg { display: block; width: 100%; height: 100%; }
  .scan { font-size: 2.2vmin; color: #9fb0c5; margin: 0 0 3vmin; line-height: 1.6; }
  .scan strong { color: #7dd3fc; font-family: ui-monospace, "SF Mono", Menlo, Consolas, monospace; }
  .badge {
    display: inline-block; font-size: 1.8vmin; letter-spacing: 0.1em;
    text-transform: uppercase; color: #0b0d10; background: #7dd3fc;
    padding: 0.6vmin 1.6vmin; border-radius: 999px; margin-bottom: 3vmin;
  }
</style>
</head>
<body>
  <main>
    {{ if .NeedsSetup }}<div class="badge">Not set up yet</div>{{ end }}
    <h1>{{ .Device }}</h1>
    {{ if .Onboarding }}
    <p>Scan this with your phone's camera to set up this screen.</p>
    {{ if .Code }}<div class="code">{{ .Code }}</div>{{ end }}
    <p class="scan">Your phone joins <strong>{{ .SetupSSID }}</strong> and opens the setup page by itself.<br>
    This screen is the only place that network's password appears.</p>
    {{ else }}
    <p>{{ if .NeedsSetup }}Open this address to finish setting up this screen.{{ else }}Nothing is scheduled. Open this address to choose what to show.{{ end }}</p>
    {{ if .Code }}<div class="code">{{ .Code }}</div>
    <p class="scan">Scan this with a phone, or open the address below.</p>{{ end }}
    {{ range .Addresses }}<div class="address">{{ . }}</div><br>{{ end }}
    {{ end }}
    <div class="identifier">{{ .Identifier }}</div>
  </main>
<script>{{ .WayBack }}</script>
</body>
</html>
`))
