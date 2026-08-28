package web

import (
	"html/template"
	"net/http"
)

// upgrading is what the screen shows while it is being replaced.
//
// A wall display goes dark for the better part of a minute during an upgrade.
// Without this, somebody standing in front of one watches it die for no
// visible reason -- in a lobby or a waiting room, that is the moment somebody
// decides the screen is broken and stops trusting it.
//
// Deliberately plain: no fetches, no timers, nothing that needs the daemon
// that is about to stop. It has to keep saying this after the process serving
// it has gone, because the browser holds the rendered page until the container
// stops and the X server with it.
func (self *Server) upgrading(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	if err := upgradingTemplate.Execute(response, map[string]interface{}{
		"Device":  self.store.Current().Device.Name,
		"Version": request.URL.Query().Get("version"),
		"Mark":    template.URL("data:image/png;base64," + smallMark()),
	}); err != nil {
		log.Debugf("cannot render the upgrading page: %s", err)
	}
}

var upgradingTemplate = template.Must(template.New("upgrading").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{ .Device }}</title>
<style>
  :root { color-scheme: dark; --accent: #f57915; --step: 1.2vmin; }
  html, body { margin: 0; height: 100%; background: #0f1216; color: #e7ecf3;
    font: 2.4vmin system-ui, -apple-system, "Segoe UI", Roboto,
      "Noto Sans CJK SC", "Noto Sans CJK JP", sans-serif; }
  body { display: grid; place-items: center; text-align: center; }
  .panel { max-width: 70vmin; }
  img { width: 10vmin; height: 10vmin; margin-bottom: calc(var(--step) * 2); }
  h1 { font-size: 3.4vmin; margin: 0 0 calc(var(--step) * 1.5); }
  p { color: #9fb0c5; line-height: 1.6; margin: 0 0 var(--step); }
  /* Drawn rather than animated with anything clever: this page has to work
     while the machine underneath it is being taken apart. */
  .bar { margin-top: calc(var(--step) * 3); height: 0.6vmin; width: 40vmin;
    background: #1f2731; border-radius: 1vmin; overflow: hidden;
    margin-left: auto; margin-right: auto; }
  .bar i { display: block; height: 100%; width: 35%; background: var(--accent);
    border-radius: 1vmin; animation: sweep 1.8s ease-in-out infinite; }
  @keyframes sweep {
    0%   { transform: translateX(-100%); }
    100% { transform: translateX(320%); }
  }
</style>
</head>
<body>
<div class="panel">
  <img src="{{ .Mark }}" alt="">
  <h1>Updating{{ if .Version }} to {{ .Version }}{{ end }}</h1>
  <p>This screen will go blank for a minute and come back on its own.</p>
  <p>Nothing needs doing here.</p>
  <div class="bar"><i></i></div>
</div>
</body>
</html>
`))
