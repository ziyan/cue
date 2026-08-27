package web

import (
	"html/template"
	"net/http"
)

// The way back, for somebody standing in front of a screen.
//
// Every other way of putting a device back into setup needs the network: the
// web interface, an SSH session, the API. The situations that need a way back
// are exactly the ones where none of those work -- a screen on a network
// nobody can route to, a screen whose network has gone. What such a device
// always has is its own display and whatever input is attached to it.
//
// So the control is on the screen. It is hidden until somebody moves the
// pointer or touches the display, and hides itself again a few seconds after
// they stop, for the same reason the mouse cursor does: a wall display with a
// button permanently on it is a wall display with a button in every photograph
// of it.
//
// It is injected into every page rather than drawn by Cue itself. A window of
// Cue's own would survive the browser dying, which is the honest argument for
// it, but this repository has no font renderer and no X event loop and that is
// a great deal of new machinery for one button. A browser that is not running
// is the watchdog's problem, not this one's.

// resetWireless forgets the wireless network and starts the setup network, so
// that the screen shows its code again.
//
// It is served to this machine and refused to the network, like the other
// things the screen's own browser asks for. Somebody at the screen has already
// demonstrated the access this grants; somebody on the network has not.
func (self *Server) resetWireless(response http.ResponseWriter, request *http.Request) {
	if err := self.device.ForgetWireless(); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]interface{}{"resetting": true})
}

// wayBackScript is added to every page the browser opens.
//
// It is written to be inert on any page that is not on this device's own
// screen: it asks the daemon whether it should be there at all, and does
// nothing if the answer is no or does not come.
const wayBackScript = `
(function () {
  // Only on the screen itself. This script is injected into every tab, and a
  // tab is only ever the screen's when the daemon says so.
  if (window.__cueWayBack) return;
  window.__cueWayBack = true;

  var idleTimer = null;
  var panel = null;
  var asking = false;

  function make() {
    if (panel) return panel;

    panel = document.createElement("div");
    panel.style.cssText = [
      "position:fixed", "right:1.5vmin", "bottom:1.5vmin", "z-index:2147483647",
      "font:1.8vmin system-ui,-apple-system,Segoe UI,Roboto,sans-serif",
      "color:#e7ecf3", "background:rgba(11,13,16,0.92)",
      "border:1px solid #2a323d", "border-radius:1vmin", "padding:1.2vmin 1.6vmin",
      "box-shadow:0 0.5vmin 2vmin rgba(0,0,0,0.5)",
      "opacity:0", "transition:opacity 0.2s ease", "pointer-events:none",
      "user-select:none",
    ].join(";");
    document.documentElement.appendChild(panel);
    draw();
    return panel;
  }

  function draw() {
    panel.textContent = "";
    if (!asking) {
      var button = document.createElement("button");
      button.textContent = "Set up wireless again";
      button.style.cssText = "all:unset;cursor:pointer;padding:0.4vmin 0.8vmin;color:#7dd3fc";
      button.addEventListener("click", function (event) {
        event.stopPropagation();
        asking = true;
        draw();
        show();
      });
      panel.appendChild(button);
      return;
    }

    // Asked before doing. A screen in a lobby must not be resettable by one
    // stray click, and this takes it off its network.
    var question = document.createElement("span");
    question.textContent = "Forget this network and show the setup code? ";
    panel.appendChild(question);

    var yes = document.createElement("button");
    yes.textContent = "Yes";
    yes.style.cssText = "all:unset;cursor:pointer;padding:0.4vmin 0.8vmin;color:#f57915;font-weight:600";
    yes.addEventListener("click", function (event) {
      event.stopPropagation();
      panel.textContent = "Setting up. The code will be on this screen in a moment.";
      fetch("/api/v1/wireless/reset", { method: "POST" }).catch(function () {});
    });
    panel.appendChild(yes);

    var no = document.createElement("button");
    no.textContent = "No";
    no.style.cssText = "all:unset;cursor:pointer;padding:0.4vmin 0.8vmin;color:#9fb0c5";
    no.addEventListener("click", function (event) {
      event.stopPropagation();
      asking = false;
      draw();
      show();
    });
    panel.appendChild(no);
  }

  function show() {
    make();
    panel.style.opacity = "1";
    panel.style.pointerEvents = "auto";
    if (idleTimer) clearTimeout(idleTimer);
    // Long enough to read the question and decide, short enough that it is not
    // in the photograph.
    idleTimer = setTimeout(hide, asking ? 15000 : 6000);
  }

  function hide() {
    if (!panel) return;
    panel.style.opacity = "0";
    panel.style.pointerEvents = "none";
    asking = false;
    draw();
  }

  // Any sign of somebody being there.
  ["mousemove", "mousedown", "touchstart", "keydown"].forEach(function (name) {
    window.addEventListener(name, show, { passive: true, capture: true });
  });
})();
`

// WayBackScript is the control, for the browser to put on pages this daemon
// did not write.
func WayBackScript() string { return wayBackScript }

// wayBack is the script as something a template will emit unescaped. It is a
// constant of this program, never anything a request supplies.
func wayBack() template.JS {
	return template.JS(wayBackScript)
}
