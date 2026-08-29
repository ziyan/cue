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

  // Not on the daemon's own pages that are not playlist items. The mark is put
  // on every page the browser shows, and the menu is a page the browser shows,
  // so the menu got one too -- and pressing it opened a second menu on top of
  // the first. The page that says the screen is being updated is excluded for
  // the same reason: there is nothing useful to do from it, and it is about to
  // be taken away.
  var OWN = MENU.replace(/\/menu$/, "");
  if (location.href.indexOf(OWN + "/menu") === 0 ||
      location.href.indexOf(OWN + "/upgrading") === 0) {
    return;
  }

  var mark = null, idle = null;

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
    mark.style.opacity = "1";
    mark.style.pointerEvents = "auto";
    if (idle) clearTimeout(idle);
    idle = setTimeout(hide, 6000);
  }

  function hide() {
    if (!mark) return;
    mark.style.opacity = "0";
    mark.style.pointerEvents = "none";
  }

  function open() {
    if (idle) clearTimeout(idle);
    hide();

    // This tab goes to the menu. Not a frame over the page, and not a tab of
    // its own: both were tried on a real screen and both were wrong.
    //
    // A frame made the menu a subresource of whatever page was showing,
    // fetched from an address on the local network -- so Chrome asked the
    // viewer to approve it, on a wall, where there is nobody to ask.
    //
    // A tab of its own solved that and broke something worse. The daemon knows
    // the tabs it opened and nothing about one a page opens for itself: it
    // swept it up as a stray window, and when the tab closed itself the
    // browser stopped answering the daemon at all -- Runtime.evaluate timing
    // out, the watchdog escalating, and a frozen display on the wall.
    //
    // Navigating this tab is neither. It is a top-level navigation, so no
    // permission is involved and the menu is same-origin with the daemon that
    // serves it. And no tab is created or destroyed, so the daemon's idea of
    // its own tabs never stops being true -- which is the property that was
    // actually load-bearing.
    // Where to come back to, so that closing the menu does not depend on the
    // browser's history having survived. The menu checks it is an ordinary web
    // address before going anywhere near it.
    location.href = MENU + "?from=" + encodeURIComponent(location.href);
  }

  ["mousemove", "mousedown", "touchstart", "keydown"].forEach(function (name) {
    window.addEventListener(name, show, { passive: true, capture: true });
  });
})();
`

// MenuAddress is where the on-screen menu answers. The browser needs it to
// recognise the tab the control opens as one of its own rather than a stray
// window to be closed.
func (self *Server) MenuAddress() string {
	if _, port, err := net.SplitHostPort(self.Address()); err == nil && port != "" {
		return "http://127.0.0.1:" + port + "/menu"
	}
	return "http://127.0.0.1:8080/menu"
}

// WayBackScript is the control, for the browser to put on pages this daemon
// did not write. The address of the menu is filled in here because only the
// server knows which port it answers on.
func (self *Server) WayBackScript() string {
	script := strings.ReplaceAll(wayBackScriptTemplate, "__MENU__", self.MenuAddress())
	return strings.ReplaceAll(script, "__MARK__", "data:image/png;base64,"+smallMark())
}

// wayBack is the script as something a template will emit unescaped. It is
// built by this program, never from anything a request supplies.
func (self *Server) wayBack() template.JS {
	return template.JS(self.WayBackScript())
}
