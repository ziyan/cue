package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"image/png"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/ziyan/cue/internal/util/deferutil"
	"github.com/ziyan/cue/internal/util/picture"
	"github.com/ziyan/cue/internal/version"
	"github.com/ziyan/cue/internal/wallpaper"
)

// The menu somebody at the screen can open.
//
// It is what the floating mark opens, and it is a page of this daemon's own so
// that it can act: a page served from somewhere else may not, whatever it is
// displayed on. See fromOurOwnPage.
//
// It offers actions and no settings. Somebody standing at a screen with a
// mouse wants to restart something, skip an item, or start the wireless setup
// again; changing a URL or a timezone is work for a keyboard and the web
// interface. Restricting it to actions also means nothing here can leave the
// device in a state somebody has to undo.

// menu renders the page shown inside the overlay.
func (self *Server) menu(response http.ResponseWriter, request *http.Request) {
	configuration := self.store.Current()

	addresses := make([]string, 0, 3)
	for _, address := range machineAddresses() {
		addresses = append(addresses, address)
	}
	if len(addresses) == 0 {
		addresses = append(addresses, "no address")
	}

	network := "not connected"
	if state := self.device.Network(); state != nil {
		if status := state.State(); len(status.Interfaces) > 0 {
			for _, one := range status.Interfaces {
				if one.Wireless != nil && one.Wireless.SSID != "" {
					network = one.Wireless.SSID
					break
				}
			}
		}
	}

	_, setUp := self.device.SetupNetwork()

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	if err := menuTemplate.Execute(response, map[string]interface{}{
		"Device":     configuration.Device.Name,
		"Identifier": configuration.Device.Identifier,
		"Version":    version.String(),
		"Addresses":  strings.Join(addresses, "  ·  "),
		"Network":    network,
		"Uptime":     time.Since(self.device.StartedAt()).Round(time.Second).String(),
		"Machine":    runtime.GOARCH,
		"SettingUp":  setUp,
		"Mark":       template.URL("data:image/png;base64," + smallMark()),
	}); err != nil {
		log.Debugf("cannot render the menu: %s", err)
	}
}

// holdPlaylist keeps the screen still while the menu is open, and lets it go
// again when it closes.
func (self *Server) holdPlaylist(response http.ResponseWriter, request *http.Request) {
	browser := self.device.Browser()
	if browser == nil {
		writeError(response, http.StatusServiceUnavailable, "there is no browser to hold")
		return
	}
	if strings.HasSuffix(request.URL.Path, "/release") {
		browser.Release()
	} else {
		browser.Hold()
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"held": true})
}

// smallMark is this project's mark, shrunk to something a button wants and
// encoded for putting straight into a page.
//
// Inline rather than fetched, because the mark has to appear on pages served
// by other people, and a page that fetches it from this device is a page
// making a cross-origin request that its own rules may forbid. Encoded once:
// it is the same bytes every time.
var smallMark = sync.OnceValue(func() string {
	mark := wallpaper.Mark()
	if mark == nil {
		return ""
	}
	small := picture.Shrink(mark, 96)

	var buffer bytes.Buffer
	if err := png.Encode(&buffer, small); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
})

var menuTemplate = template.Must(template.New("menu").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{ .Device }}</title>
<style>
  :root { color-scheme: dark; --accent: #f57915; }
  * { box-sizing: border-box; }
  html, body { margin: 0; height: 100%; background: rgba(6,8,10,0.72);
    font: 2vmin system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; color: #e7ecf3; }
  body { display: grid; place-items: center; padding: 4vmin; }
  .panel { width: min(64vmin, 92vw); background: #0f1216; border: 1px solid #2a323d;
    border-radius: 1.6vmin; padding: 3vmin; box-shadow: 0 2vmin 6vmin rgba(0,0,0,0.6); }
  header { display: flex; align-items: center; gap: 1.6vmin; margin-bottom: 2vmin; }
  header img { width: 6vmin; height: 6vmin; }
  h1 { font-size: 2.8vmin; margin: 0; }
  .facts { color: #9fb0c5; font-size: 1.7vmin; line-height: 1.7; margin: 0 0 2.4vmin; }
  .facts b { color: #e7ecf3; font-weight: 600; }
  .actions { display: grid; gap: 1vmin; }
  button { all: unset; cursor: pointer; padding: 1.4vmin 1.8vmin; border-radius: 1vmin;
    background: #171d24; border: 1px solid #2a323d; text-align: left; }
  button:hover { background: #1f2731; }
  button .what { display: block; }
  button .why { display: block; color: #9fb0c5; font-size: 1.5vmin; margin-top: 0.4vmin; }
  button.danger .what { color: #ffc9d1; }
  .close { margin-top: 2vmin; text-align: center; color: #9fb0c5; }
  #confirm { display: none; }
  #working { display: none; color: #9fb0c5; margin-top: 2vmin; }
</style>
</head>
<body>
<div class="panel">
  <header>
    {{ if .Mark }}<img src="{{ .Mark }}" alt="">{{ end }}
    <h1>{{ .Device }}</h1>
  </header>

  <p class="facts">
    <b>{{ .Addresses }}</b><br>
    Wireless: <b>{{ .Network }}</b><br>
    {{ .Identifier }} · {{ .Version }} · {{ .Machine }} · up {{ .Uptime }}
  </p>

  <div class="actions" id="actions">
    <button data-do="next"><span class="what">Show the next item</span>
      <span class="why">Move the screen on now</span></button>
    <button data-do="reload"><span class="what">Reload what is on screen</span>
      <span class="why">For a dashboard that has stopped updating</span></button>
    <button data-do="restart-browser" class="danger"><span class="what">Restart the browser</span>
      <span class="why">The screen goes black for a few seconds</span></button>
    <button data-do="restart-display" class="danger"><span class="what">Restart the screen</span>
      <span class="why">Rebuilds the display itself; takes longer</span></button>
    {{ if not .SettingUp }}
    <button data-do="wireless" class="danger"><span class="what">Set up wireless again</span>
      <span class="why">Forgets this network and shows the setup code</span></button>
    {{ end }}
  </div>

  <div id="confirm">
    <p class="facts" id="question"></p>
    <div class="actions">
      <button id="yes" class="danger"><span class="what">Yes, do it</span></button>
      <button id="no"><span class="what">No, go back</span></button>
    </div>
  </div>

  <p id="working"></p>
  <p class="close"><button id="dismiss"><span class="what">Close</span></button></p>
</div>
<script>
  const actions = document.getElementById("actions");
  const confirm = document.getElementById("confirm");
  const question = document.getElementById("question");
  const working = document.getElementById("working");

  // Held while this is open, so the screen does not rotate out from under
  // somebody reading it.
  fetch("/api/v1/playlist/hold", { method: "POST" }).catch(() => {});

  function close() {
    fetch("/api/v1/playlist/release", { method: "POST" }).catch(() => {});
    // The overlay belongs to the page underneath, which put it there.
    parent.postMessage("cue:close-menu", "*");
  }

  const doing = {
    "next": { call: "/api/v1/playlist/next", ask: null, said: "Moving on." },
    "reload": { call: "/api/v1/menu/reload", ask: null, said: "Reloading." },
    "restart-browser": { call: "/api/v1/menu/restart/browser",
      ask: "Restart the browser? The screen goes black for a few seconds.",
      said: "Restarting the browser." },
    "restart-display": { call: "/api/v1/menu/restart/display",
      ask: "Restart the screen? It rebuilds the display and takes longer.",
      said: "Restarting the screen." },
    "wireless": { call: "/api/v1/wireless/reset",
      ask: "Forget this wireless network and show the setup code?",
      said: "Setting up. The code will be on this screen in a moment." },
  };

  actions.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-do]");
    if (!button) return;
    const what = doing[button.dataset.do];
    if (!what) return;

    if (!what.ask) return run(what);

    question.textContent = what.ask;
    actions.style.display = "none";
    confirm.style.display = "block";
    document.getElementById("yes").onclick = () => run(what);
    document.getElementById("no").onclick = () => {
      confirm.style.display = "none";
      actions.style.display = "grid";
    };
  });

  function run(what) {
    actions.style.display = "none";
    confirm.style.display = "none";
    working.style.display = "block";
    working.textContent = what.said;
    fetch(what.call, { method: "POST" })
      .catch(() => {})
      .finally(() => setTimeout(close, 1200));
  }

  document.getElementById("dismiss").addEventListener("click", close);
  window.addEventListener("keydown", (event) => { if (event.key === "Escape") close(); });
</script>
</body>
</html>
`))

// menuReload reloads whatever is on the screen, for a dashboard that has
// quietly stopped updating.
func (self *Server) menuReload(response http.ResponseWriter, request *http.Request) {
	browser := self.device.Browser()
	if browser == nil {
		writeError(response, http.StatusServiceUnavailable, "there is no browser")
		return
	}
	if err := browser.ReloadCurrent(request.Context()); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"reloading": true})
}

// menuRestart restarts one of the programs the screen is made of.
//
// Only the two worth offering to somebody standing at a screen: the browser,
// for a page that has wedged, and the display, for everything else. Anything
// larger is a power cycle, which they can also do.
func (self *Server) menuRestart(response http.ResponseWriter, request *http.Request) {
	program := mux.Vars(request)["program"]
	switch program {
	case "browser", "display":
	default:
		writeError(response, http.StatusBadRequest,
			fmt.Sprintf("%q is not something the menu restarts", program))
		return
	}

	// Answered before doing it: restarting takes the screen down, and this
	// request came from the page on it.
	writeJSON(response, http.StatusOK, map[string]interface{}{"restarting": program})

	go func() {
		defer deferutil.Recover()
		if err := self.device.Restart(context.Background(), program); err != nil {
			log.Warningf("cannot restart the %s: %s", program, err)
		}
	}()
}
