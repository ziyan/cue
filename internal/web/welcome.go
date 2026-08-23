package web

import (
	"html/template"
	"net"
	"net/http"
	"strings"
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
	err = welcomeTemplate.Execute(response, map[string]interface{}{
		"Device":     configuration.Device.Name,
		"Identifier": configuration.Device.Identifier,
		"Addresses":  addresses,
		"NeedsSetup": !self.isSetUp(),
	})
	if err != nil {
		log.Debugf("cannot render the welcome page: %s", err)
	}
}

// machineAddresses lists this machine's own addresses on the network, which
// are what somebody would type into a laptop in the same room. Loopback and
// link-local addresses are left out because typing them into another machine
// reaches nothing.
func machineAddresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var addresses []string
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
			addresses = append(addresses, text)
		}
	}
	return addresses
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
    <p>{{ if .NeedsSetup }}Open this address to finish setting up this screen.{{ else }}Nothing is scheduled. Open this address to choose what to show.{{ end }}</p>
    {{ range .Addresses }}<div class="address">{{ . }}</div><br>{{ end }}
    <div class="identifier">{{ .Identifier }}</div>
  </main>
</body>
</html>
`))
