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
  /* box-sizing after all:unset, not before. "all: unset" resets box-sizing
     too, so these buttons were content-box while everything else was
     border-box -- and a row with width:100% and padding came out wider than
     the list holding it, clipping the signal bars off the right of every
     network. Three rounds of looking at screenshots did not find that; one
     measurement did. */
  button { all: unset; box-sizing: border-box; cursor: pointer;
    padding: 1.4vmin 1.8vmin; border-radius: 1vmin;
    background: #171d24; border: 1px solid #2a323d; text-align: left; }
  button:hover { background: #1f2731; }
  button .what { display: block; }
  button .why { display: block; color: #9fb0c5; font-size: 1.5vmin; margin-top: 0.4vmin; }
  button.danger .what { color: #ffc9d1; }
  .close { margin-top: 2vmin; text-align: center; color: #9fb0c5; }
  #confirm { display: none; }
  .tabs { display: flex; gap: 1vmin; margin-bottom: 1.6vmin; }
  .tabs button { flex: 1; text-align: center; }
  .tabs button.on { border-color: var(--accent); }
  .list { max-height: 34vmin; overflow-y: auto; margin-bottom: 1.6vmin;
    border: 1px solid #2a323d; border-radius: 1vmin; }
  .list button { width: 100%; border: 0; border-radius: 0; background: #131920;
    display: flex; align-items: center; gap: 1vmin; padding-right: 2.4vmin; }
  .list button + button { border-top: 1px solid #2a323d; }
  .list button.on { background: #1f2a35; box-shadow: inset 0.3vmin 0 0 var(--accent); }
  .list .ssid { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .list .lock { color: #9fb0c5; font-size: 1.5vmin; }
  /* Drawn rather than written. Block characters were tried first and render
     at widths the font decides, so the strongest network had its last bar
     clipped and looked like the weakest. */
  .list .bars { display: inline-flex; align-items: flex-end; gap: 0.4vmin;
    height: 2.4vmin; flex: none; }
  .list .bars i { width: 0.9vmin; background: #3d4756; border-radius: 0.2vmin; }
  .list .bars i.on { background: #7dd3fc; }
  input, select { width: 100%; padding: 1.2vmin; margin-bottom: 1.2vmin; font: inherit;
    color: inherit; background: #131920; border: 1px solid #2a323d; border-radius: 1vmin; }
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
    <button data-do="network"><span class="what">Set up the network</span>
      <span class="why">Join a wireless network, or give this screen a fixed address</span></button>
    {{ if not .SettingUp }}
    <button data-do="wireless" class="danger"><span class="what">Set up wireless again</span>
      <span class="why">Forgets this network and shows the setup code</span></button>
    {{ end }}
  </div>

  <div id="network" style="display:none">
    <div class="tabs">
      <button id="tab-wireless" class="on"><span class="what">Wireless</span></button>
      <button id="tab-wired"><span class="what">Wired</span></button>
    </div>

    <div id="wireless">
      <div class="list" id="networks"><p class="facts">Looking for networks…</p></div>
      <div id="secret" style="display:none">
        <p class="facts">Password for <b id="chosen"></b></p>
        <input id="passphrase" type="password" autocomplete="off" autocapitalize="none" spellcheck="false">
      </div>
      <div class="actions">
        <button id="join"><span class="what">Join</span></button>
        <button id="rescan"><span class="what">Look again</span></button>
      </div>
    </div>

    <div id="wired" style="display:none">
      <p class="facts">Which interface</p>
      <select id="wired-interface"></select>
      <p class="facts">How it gets an address</p>
      <select id="wired-method">
        <option value="dhcp">Ask the network (DHCP)</option>
        <option value="static">Use the address below</option>
      </select>
      <div id="fixed" style="display:none">
        <p class="facts">Address and prefix, for example 192.0.2.10/24</p>
        <input id="wired-address" autocomplete="off" spellcheck="false">
        <p class="facts">Gateway</p>
        <input id="wired-gateway" autocomplete="off" spellcheck="false">
        <p class="facts">Name servers, separated by spaces</p>
        <input id="wired-dns" autocomplete="off" spellcheck="false">
      </div>
      <div class="actions"><button id="apply"><span class="what">Apply</span></button></div>
    </div>

    <div class="actions"><button id="back"><span class="what">Back</span></button></div>
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

  const network = document.getElementById("network");
  const panelWireless = document.getElementById("wireless");
  const panelWired = document.getElementById("wired");
  let chosenNetwork = null, chosenSecured = false;

  function openNetwork() {
    actions.style.display = "none";
    network.style.display = "block";
    loadInterfaces();
    scan();
  }

  document.getElementById("back").addEventListener("click", () => {
    network.style.display = "none";
    actions.style.display = "grid";
  });

  document.getElementById("tab-wireless").addEventListener("click", () => tab(true));
  document.getElementById("tab-wired").addEventListener("click", () => tab(false));

  function tab(wireless) {
    panelWireless.style.display = wireless ? "block" : "none";
    panelWired.style.display = wireless ? "none" : "block";
    document.getElementById("tab-wireless").className = wireless ? "on" : "";
    document.getElementById("tab-wired").className = wireless ? "" : "on";
  }

  function scan() {
    const list = document.getElementById("networks");
    list.innerHTML = "<p class=\"facts\">Looking for networks…</p>";
    fetch("/api/v1/menu/network/scan", { method: "POST" })
      .then((answer) => answer.json())
      .then((found) => {
        list.textContent = "";
        const networks = found.networks || [];
        if (!networks.length) {
          list.innerHTML = "<p class=\"facts\">Nothing in range.</p>";
          return;
        }
        for (const one of networks) {
          const button = document.createElement("button");
          button.disabled = !one.joinable;
          var bars = "";
          for (var level = 1; level <= 4; level++) {
            bars += "<i class=\"" + (level <= one.bars ? "on" : "") +
              "\" style=\"height:" + (level * 0.55 + 0.6) + "vmin\"></i>";
          }
          button.innerHTML = "<span class=\"ssid\"></span>" +
            (one.secured ? "<span class=\"lock\">locked</span>" : "") +
            "<span class=\"bars\">" + bars + "</span>";
          button.querySelector(".ssid").textContent = one.ssid;
          button.addEventListener("click", () => {
            list.querySelectorAll("button").forEach((other) => { other.className = ""; });
            button.className = "on";
            chosenNetwork = one.ssid;
            chosenSecured = one.secured;
            document.getElementById("chosen").textContent = one.ssid;
            document.getElementById("secret").style.display = one.secured ? "block" : "none";
            if (one.secured) document.getElementById("passphrase").focus();
          });
          list.appendChild(button);
        }
      })
      .catch(() => { list.innerHTML = "<p class=\"facts\">The scan did not work.</p>"; });
  }

  document.getElementById("rescan").addEventListener("click", scan);

  document.getElementById("join").addEventListener("click", () => {
    if (!chosenNetwork) return;
    say("Joining " + chosenNetwork + ". This screen may lose its connection for a moment.");
    fetch("/api/v1/menu/network/wireless", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        ssid: chosenNetwork,
        passphrase: chosenSecured ? document.getElementById("passphrase").value : "",
      }),
    }).catch(() => {}).finally(() => setTimeout(close, 2500));
  });

  function loadInterfaces() {
    fetch("/api/v1/menu/network")
      .then((answer) => answer.json())
      .then((state) => {
        const chooser = document.getElementById("wired-interface");
        chooser.textContent = "";
        for (const one of (state.interfaces || [])) {
          if (one.kind === "wireless") continue;
          const option = document.createElement("option");
          option.value = one.name;
          option.textContent = one.name + (one.addresses && one.addresses.length
            ? "  ·  " + one.addresses.join(", ") : "  ·  no address");
          chooser.appendChild(option);
        }
      })
      .catch(() => {});
  }

  document.getElementById("wired-method").addEventListener("change", (event) => {
    document.getElementById("fixed").style.display =
      event.target.value === "static" ? "block" : "none";
  });

  document.getElementById("apply").addEventListener("click", () => {
    const name = document.getElementById("wired-interface").value;
    if (!name) return;
    const dns = document.getElementById("wired-dns").value.trim();
    say("Setting up " + name + ". This screen may lose its connection for a moment.");
    fetch("/api/v1/menu/network/wired", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        interface: name,
        method: document.getElementById("wired-method").value,
        address: document.getElementById("wired-address").value.trim(),
        gateway: document.getElementById("wired-gateway").value.trim(),
        nameservers: dns ? dns.split(/\s+/) : [],
      }),
    }).catch(() => {}).finally(() => setTimeout(close, 2500));
  });

  function say(what) {
    network.style.display = "none";
    actions.style.display = "none";
    working.style.display = "block";
    working.textContent = what;
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
    if (button.dataset.do === "network") return openNetwork();

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
