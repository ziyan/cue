package web

import (
	"html/template"
	"net"
	"net/http"
	"strings"
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
// It puts a small mark in the corner and nothing else. Everything it can
// actually do lives behind it, in a page this daemon served: this script runs
// inside whatever page is on the screen, which is usually somebody else's, and
// a page from somewhere else may not act on this device however it got here.
// So the mark opens an overlay containing that page, and the page does the
// work.
//
// The mark and the frame are the only two things here, which is why this is
// short. It was longer, and did the acting itself, until it was noticed that
// any dashboard on the screen could then have done the same.
const wayBackScriptTemplate = `
(function () {
  if (window.__cueWayBack) return;
  window.__cueWayBack = true;

  var MENU = "__MENU__";
  var MARK = "__MARK__";

  var mark = null, frame = null, idle = null;

  function make() {
    if (mark) return;

    mark = document.createElement("div");
    mark.setAttribute("aria-label", "Set up this screen");
    mark.style.cssText = [
      "position:fixed", "right:2vmin", "bottom:2vmin", "z-index:2147483646",
      "width:7vmin", "height:7vmin", "border-radius:50%",
      "background:rgba(11,13,16,0.82)", "border:1px solid #2a323d",
      "display:grid", "place-items:center", "cursor:pointer",
      "opacity:0", "transition:opacity 0.25s ease", "pointer-events:none",
      "box-shadow:0 0.5vmin 2vmin rgba(0,0,0,0.5)",
    ].join(";");

    if (MARK) {
      var picture = document.createElement("img");
      picture.src = MARK;
      picture.alt = "";
      picture.style.cssText = "width:4.6vmin;height:4.6vmin;display:block";
      // A page whose own rules forbid inline pictures gets a word instead,
      // rather than an empty circle nobody would press.
      picture.addEventListener("error", word);
      mark.appendChild(picture);
    } else {
      word();
    }

    mark.addEventListener("click", open);
    document.documentElement.appendChild(mark);
  }

  function word() {
    mark.textContent = "cue";
    mark.style.font = "700 2vmin system-ui,-apple-system,Segoe UI,Roboto,sans-serif";
    mark.style.color = "#f57915";
  }

  function show() {
    make();
    if (frame) return;
    mark.style.opacity = "1";
    mark.style.pointerEvents = "auto";
    if (idle) clearTimeout(idle);
    idle = setTimeout(hide, 6000);
  }

  function hide() {
    if (!mark || frame) return;
    mark.style.opacity = "0";
    mark.style.pointerEvents = "none";
  }

  function open() {
    if (frame) return;
    if (idle) clearTimeout(idle);
    hide();

    frame = document.createElement("iframe");
    frame.src = MENU;
    frame.style.cssText = [
      "position:fixed", "inset:0", "z-index:2147483647",
      "width:100%", "height:100%", "border:0", "background:transparent",
    ].join(";");
    document.documentElement.appendChild(frame);
  }

  function close() {
    if (!frame) return;
    frame.remove();
    frame = null;
    show();
  }

  // The menu is a page of its own and cannot reach into this one, so closing
  // is a message rather than a call. Nothing but closing is accepted, and the
  // message carries no authority: a page could send it, and all that happens
  // is that a frame it did not open goes away.
  window.addEventListener("message", function (event) {
    if (event.data === "cue:close-menu") close();
  });

  ["mousemove", "mousedown", "touchstart", "keydown"].forEach(function (name) {
    window.addEventListener(name, show, { passive: true, capture: true });
  });
})();
`

// WayBackScript is the control, for the browser to put on pages this daemon
// did not write. The address of the menu is filled in here because only the
// server knows which port it answers on.
func (self *Server) WayBackScript() string {
	menu := "http://127.0.0.1:8080/menu"
	if _, port, err := net.SplitHostPort(self.Address()); err == nil && port != "" {
		menu = "http://127.0.0.1:" + port + "/menu"
	}
	script := strings.ReplaceAll(wayBackScriptTemplate, "__MENU__", menu)
	return strings.ReplaceAll(script, "__MARK__", "data:image/png;base64,"+smallMark())
}

// wayBack is the script as something a template will emit unescaped. It is
// built by this program, never from anything a request supplies.
func (self *Server) wayBack() template.JS {
	return template.JS(self.WayBackScript())
}
