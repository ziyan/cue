package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The setup portal: the page a phone opens by itself after joining the
// temporary network this device runs for its own setup.
//
// It is served without a session, because it exists precisely when the device
// has no password yet. What keeps it from being an open door is the network in
// front of it: joining requires the passphrase, and the passphrase is on the
// screen and nowhere else. Being able to configure this device means being in
// the room with it.
//
// These routes are registered once, at startup, and answer 404 unless setup is
// actually running. Adding and removing routes from a live router is not
// something gorilla/mux supports and would be a race against requests in
// flight; a guard is simpler and cannot get out of step.

// portalAddress is where the phone is sent. It is the device's address on the
// setup network, which is the only address that reaches it from there.
const portalAddress = "http://192.168.216.1/portal"

// onboardingOrNotFound answers 404 unless this device is being set up, and is
// wrapped around everything in this file.
func (self *Server) onboardingOrNotFound(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, running := self.device.SetupNetwork(); !running {
			http.NotFound(response, request)
			return
		}
		next(response, request)
	}
}

// portalAction guards the things the setup portal does that change this
// device.
//
// The portal is reached over a network anybody with the code can join, which
// is the point: a device out of its box has nothing to protect and the code is
// on its screen. A device that has been set up is different. Losing a network
// is not losing ownership, and a screen that fell back to offering setup must
// not become a screen that anybody in range can put on their own network.
//
// So the portal asks for the password when there is one -- and when there is
// not, it asks for one to be chosen, which is the same question a step
// earlier. A device with no password is not a device nobody owns; it is one
// nobody finished setting up, and the phone in front of it belongs to whoever
// is doing that now.
//
// What proves it is the pass this page was given when it was served, not a
// session cookie. A phone that joined the setup network to fix a screen should
// not leave signed in to it.
func (self *Server) portalAction(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if self.hasElevatedPass(request) {
			next(response, request)
			return
		}
		if !self.isSetUp() {
			writeError(response, http.StatusUnauthorized, "choose a password first")
			return
		}
		if !self.hasSession(request) {
			writeError(response, http.StatusUnauthorized, "sign in first")
			return
		}
		next(response, request)
	}
}

// portal renders the page somebody fills in to put this device on a network.
func (self *Server) portal(response http.ResponseWriter, request *http.Request) {
	found := self.device.SetupNetworks()

	// Strongest first: the network somebody wants is almost always the one
	// they are standing next to.
	sort.SliceStable(found, func(first, second int) bool {
		return found[first].SignalStrength > found[second].SignalStrength
	})

	// One entry per name. A network with two access points is seen twice, and
	// offering it twice makes somebody wonder which one is theirs.
	seen := map[string]bool{}
	networks := make([]map[string]interface{}, 0, len(found))
	for _, one := range found {
		if one.SSID == "" || seen[one.SSID] {
			continue
		}
		seen[one.SSID] = true
		networks = append(networks, map[string]interface{}{
			"SSID":     one.SSID,
			"Secured":  one.Security != "open",
			"Joinable": one.Security != "enterprise",
			"Bars":     signalBars(one.SignalStrength),
		})
	}

	// The authority this page will carry while it is open, minted before it
	// exists so that there is no moment where the portal is on a phone with no
	// way to prove who is holding it.
	pass, err := self.passes.mint()
	if err != nil {
		log.Errorf("cannot mint a pass for the portal: %s", err)
		writeError(response, http.StatusInternalServerError, "cannot open the setup page")
		return
	}

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	if err := portalTemplate.Execute(response, map[string]interface{}{
		"Device":    self.store.Current().Device.Name,
		"Networks":  networks,
		"Trouble":   self.device.SetupTrouble(),
		"NeedsWord": self.isSetUp(),
		"Pass":      pass,
	}); err != nil {
		log.Debugf("cannot render the setup portal: %s", err)
	}
}

// signalBars turns a signal strength in dBm into something to draw.
//
// Wireless signal is reported in dBm, a negative number where closer to zero
// is stronger. Around -50 is next to the router, -70 is a room away, and below
// -85 is barely there.
func signalBars(strength int) int {
	switch {
	case strength >= -55:
		return 4
	case strength >= -67:
		return 3
	case strength >= -78:
		return 2
	default:
		return 1
	}
}

// portalJoin is asked to put this device on a network.
//
// It answers before doing the work. Joining takes the setup network down --
// the radio cannot be an access point here and a station elsewhere at the same
// time -- so the phone asking this question is about to lose the connection it
// asked over. Waiting to answer would mean the phone never sees a reply and
// the person concludes it failed, at the exact moment it was working.
func (self *Server) portalJoin(response http.ResponseWriter, request *http.Request) {
	var wanted struct {
		SSID       string `json:"ssid"`
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(request.Body).Decode(&wanted); err != nil {
		writeError(response, http.StatusBadRequest, "that is not a network to join")
		return
	}
	if strings.TrimSpace(wanted.SSID) == "" {
		writeError(response, http.StatusBadRequest, "choose a network first")
		return
	}

	if err := self.device.JoinFromSetup(wanted.SSID, wanted.Passphrase); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"joining": true, "ssid": wanted.SSID})
}

// portalScan looks again for networks in range.
func (self *Server) portalScan(response http.ResponseWriter, request *http.Request) {
	if err := self.device.RescanFromSetup(); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"networks": self.device.SetupNetworks()})
}

// captiveProbe is what makes the page open by itself.
//
// A phone that has just joined a network fetches a known address from its own
// vendor and checks the answer. Apple expects a tiny page containing the word
// Success, Android expects an empty 204, Windows expects the words Microsoft
// Connect Test. Our name server has already told the phone that those names
// live here, so the request arrives at this handler -- and answering with a
// redirect instead of what was expected is exactly what makes the phone decide
// the network is captive and show the page.
//
// There is no standard for this. Answering these probes wrongly, on purpose,
// is the whole mechanism.
func (self *Server) captiveProbe(response http.ResponseWriter, request *http.Request) {
	http.Redirect(response, request, portalAddress, http.StatusFound)
}

var portalTemplate = template.Must(template.New("portal").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>Set up {{ .Device }}</title>
<style>
  :root { color-scheme: dark; --accent: #f57915; }
  * { box-sizing: border-box; }
  html, body { margin: 0; background: #0b0d10; color: #e7ecf3;
    font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  main { max-width: 32rem; margin: 0 auto; padding: 1.5rem 1.25rem 4rem; }
  h1 { font-size: 1.5rem; margin: 0 0 0.25rem; }
  .lead { color: #9fb0c5; margin: 0 0 1.5rem; font-size: 0.95rem; }
  .trouble { background: #3a1519; border: 1px solid #7f2233; color: #ffc9d1;
    padding: 0.85rem 1rem; border-radius: 0.7rem; margin: 0 0 1.25rem; font-size: 0.9rem; }
  ul { list-style: none; margin: 0; padding: 0; border: 1px solid #2a323d;
    border-radius: 0.8rem; overflow: hidden; }
  li + li { border-top: 1px solid #2a323d; }
  button.network { display: flex; align-items: center; gap: 0.75rem; width: 100%;
    background: #151a21; border: 0; color: inherit; padding: 0.95rem 1rem;
    font-size: 1rem; text-align: left; cursor: pointer; }
  button.network:hover, button.network:focus { background: #1c232c; }
  button.network[aria-selected="true"] { background: #1f2a35; box-shadow: inset 3px 0 0 var(--accent); }
  .name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .bars { display: inline-flex; align-items: flex-end; gap: 2px; height: 1rem; }
  .bars i { width: 3px; background: #55637a; border-radius: 1px; }
  .bars i.on { background: #7dd3fc; }
  .lock { color: #9fb0c5; font-size: 0.85rem; }
  form { margin-top: 1.25rem; }
  label { display: block; font-size: 0.85rem; color: #9fb0c5; margin-bottom: 0.4rem; }
  #gate .go { margin-top: 1rem; }
  input { width: 100%; padding: 0.85rem 1rem; font-size: 1rem; border-radius: 0.7rem;
    border: 1px solid #2a323d; background: #151a21; color: inherit; }
  .go { width: 100%; margin-top: 1rem; padding: 0.95rem 1rem; font-size: 1rem;
    font-weight: 600; border: 0; border-radius: 0.7rem; background: var(--accent);
    color: #1a0d02; cursor: pointer; }
  .go[disabled] { opacity: 0.5; cursor: default; }
  .working { margin-top: 1.25rem; padding: 1rem; border-radius: 0.7rem;
    background: #151a21; border: 1px solid #2a323d; color: #9fb0c5; font-size: 0.9rem; }
  .hidden { display: none; }
  .again { display: block; width: 100%; margin-top: 1rem; padding: 0.7rem;
    background: none; border: 1px solid #2a323d; border-radius: 0.7rem;
    color: #9fb0c5; font-size: 0.9rem; cursor: pointer; }
</style>
</head>
<body>
<main>
  <h1>Set up {{ .Device }}</h1>
  <p class="lead">Choose the network this screen should use.</p>
  {{ if .Trouble }}<p class="trouble">{{ .Trouble }}</p>{{ end }}

  <div id="gate">
    {{ if .NeedsWord }}
    <p class="lead">This screen already belongs to somebody. Enter its password to
      change which network it uses.</p>
    <label for="word">Password</label>
    <input id="word" type="password" autocomplete="current-password" autocapitalize="none">
    {{ else }}
    <p class="lead">This screen has no password yet. Choose one now: it is what
      will be asked for the next time somebody sets it up, and it is the
      password for its web interface.</p>
    <label for="word">New password</label>
    <input id="word" type="password" autocomplete="new-password" autocapitalize="none">
    <label for="word-again">Type it again</label>
    <input id="word-again" type="password" autocomplete="new-password" autocapitalize="none">
    {{ end }}
    <p class="trouble" id="wrong" style="display:none">That is not the password.</p>
    <button class="go" id="unlock" type="button">Continue</button>
  </div>

  <div id="chooser" style="display:none">
    <ul id="networks">
      {{ range .Networks }}
      <li><button class="network" type="button" data-ssid="{{ .SSID }}" data-secured="{{ .Secured }}"
          {{ if not .Joinable }}disabled{{ end }} aria-selected="false">
        <span class="name">{{ .SSID }}</span>
        {{ if .Secured }}<span class="lock">locked</span>{{ end }}
        <span class="bars">
          {{ $bars := .Bars }}
          <i class="{{ if ge $bars 1 }}on{{ end }}" style="height:4px"></i>
          <i class="{{ if ge $bars 2 }}on{{ end }}" style="height:7px"></i>
          <i class="{{ if ge $bars 3 }}on{{ end }}" style="height:11px"></i>
          <i class="{{ if ge $bars 4 }}on{{ end }}" style="height:15px"></i>
        </span>
      </button></li>
      {{ else }}
      <li><button class="network" type="button" disabled><span class="name">No networks found</span></button></li>
      {{ end }}
    </ul>

    <form id="join">
      <div id="secret" class="hidden">
        <label for="passphrase">Password for <span id="chosen"></span></label>
        <input id="passphrase" type="password" autocomplete="off" autocapitalize="none" autocorrect="off">
      </div>
      <button class="go" id="go" type="submit" disabled>Join</button>
    </form>
    <button class="again" id="again" type="button">Scan again</button>
  </div>

  <div id="working" class="working hidden"></div>
</main>
<script>
  var chosen = null, secured = false;

  // The authority this page carries, minted when the daemon served it. It is
  // not a cookie: a phone that joined a setup network to fix a screen should
  // not walk away signed in to it.
  var pass = "{{ .Pass }}";
  var hasWord = {{ if .NeedsWord }}true{{ else }}false{{ end }};
  function send(path, options) {
    var settings = {};
    for (var key in (options || {})) settings[key] = options[key];
    settings.headers = Object.assign({ "X-Cue-Pass": pass }, settings.headers || {});
    return fetch(path, settings);
  }

  // A device that has been set up asks for its password before it will join
  // anything. Losing a network is not losing ownership, and this page is
  // reached over a network anybody with the code on the screen can join.
  var unlock = document.getElementById("unlock");
  if (unlock) {
    var word = document.getElementById("word");
    var wordAgain = document.getElementById("word-again");
    var wrong = document.getElementById("wrong");

    function complain(what) {
      wrong.textContent = what;
      wrong.style.display = "block";
    }

    function tryWord() {
      wrong.style.display = "none";

      if (!hasWord) {
        if (word.value.length < 8) {
          complain("At least eight characters.");
          return;
        }
        if (word.value !== wordAgain.value) {
          complain("Those two are not the same.");
          wordAgain.value = "";
          wordAgain.focus();
          return;
        }
      }

      send(hasWord ? "/api/v1/screen/unlock" : "/api/v1/screen/password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password: word.value }),
      }).then(function (answer) {
        if (!answer.ok) {
          complain(hasWord ? "That is not the password." : "That password was not accepted.");
          word.value = "";
          if (wordAgain) wordAgain.value = "";
          word.focus();
          return;
        }
        document.getElementById("gate").style.display = "none";
        document.getElementById("chooser").style.display = "block";
      }).catch(function () {
        complain(hasWord ? "That is not the password." : "That password was not accepted.");
      });
    }

    unlock.addEventListener("click", tryWord);
    [word, wordAgain].forEach(function (box) {
      if (!box) return;
      box.addEventListener("keydown", function (event) {
        if (event.key === "Enter") tryWord();
      });
    });
    word.focus();
  }
  var chooser = document.getElementById("chooser");
  var working = document.getElementById("working");
  var secret = document.getElementById("secret");
  var passphrase = document.getElementById("passphrase");
  var go = document.getElementById("go");

  document.querySelectorAll("button.network").forEach(function (button) {
    button.addEventListener("click", function () {
      document.querySelectorAll("button.network").forEach(function (other) {
        other.setAttribute("aria-selected", "false");
      });
      button.setAttribute("aria-selected", "true");
      chosen = button.dataset.ssid;
      secured = button.dataset.secured === "true";
      document.getElementById("chosen").textContent = chosen;
      secret.className = secured ? "" : "hidden";
      go.disabled = false;
      if (secured) passphrase.focus();
    });
  });

  document.getElementById("again").addEventListener("click", function () {
    this.disabled = true;
    this.textContent = "Scanning. This drops your phone off for a moment.";
    send("/api/v1/portal/scan", { method: "POST" })
      .then(function () { location.reload(); })
      .catch(function () { location.reload(); });
  });

  document.getElementById("join").addEventListener("submit", function (event) {
    event.preventDefault();
    if (!chosen) return;

    // Said before the request, not after: this device is about to take its own
    // network down, and the phone will lose this page as it does. Somebody who
    // is not told that sees it freeze.
    chooser.className = "hidden";
    working.className = "working";
    working.innerHTML = "<strong>Joining " + chosen + ".</strong><br><br>" +
      "This screen's own network is going away now, so your phone will drop back " +
      "to wherever it was. Look at the screen: it shows the address to use once " +
      "it has joined.<br><br>If it did not work, this setup network comes back " +
      "within a minute and you can try again.";

    send("/api/v1/portal/join", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ssid: chosen, passphrase: secured ? passphrase.value : "" }),
    }).catch(function () {
      // Expected: the network goes away mid-request. Nothing to report.
    });
  });
</script>
</body>
</html>
`))

// captiveProbePaths are the addresses phones fetch to decide whether a network
// really reaches the internet. They are answered with a redirect, which is
// what makes the phone open the setup page by itself.
var captiveProbePaths = []string{
	"/hotspot-detect.html",       // Apple
	"/library/test/success.html", // Apple, older
	"/generate_204",              // Android
	"/gen_204",                   // Android, older
	"/connecttest.txt",           // Windows
	"/ncsi.txt",                  // Windows, older
	"/canonical.html",            // some Linux desktops
	"/success.txt",               // Firefox
}

// ServeSetupPort opens a second listener on port 80 of the setup network's
// address, serving the same routes, until the context is cancelled.
//
// It is needed because of where phones look. A phone checking whether a
// network reaches the internet fetches its vendor's address on port 80, and
// the address it uses is whatever DNS returned -- which, on the setup network,
// is this device. With nothing listening on port 80 the probe is refused, the
// phone never sees the redirect that makes it open the setup page, and
// somebody joins the network and then sits looking at a phone that does
// nothing. The redirect itself points at port 80 for the same reason: it has
// to be an address the phone can reach without being told a port.
//
// The daemon's own interface stays where it was, on its configured port. This
// is an extra door onto the same rooms, open only while the device is being
// set up and only on the address the setup network uses -- never on whatever
// address the device has on a real network.
func (self *Server) ServeSetupPort(ctx context.Context, address string) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("web: cannot listen on %s for setting this device up: %w", address, err)
	}

	server := &http.Server{
		Handler:           self.router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()

	log.Noticef("the setup page is on http://%s/portal", address)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
		return err
	}
	return nil
}
