// The live view of the screen, over VNC, in a browser tab.
//
// The daemon's VNC server listens on the loopback address only; the WebSocket
// this connects to is what bridges it, behind the same session as everything
// else here. See internal/web/vnc.go.

import { h } from "../dom.js";
import RFB from "../novnc/core/rfb.js";

export function screen(main) {
  const container = h("div");
  const viewer = h("div", { class: "viewer" }, container);
  const state = h("span", { class: "pill", text: "connecting" });

  const bar = h("div", { class: "actions", style: "margin-bottom:0.75rem" },
    state,
    h("span", { class: "dim", text: "Click and type to drive the screen." }),
    h("span", { style: "margin-left:auto" }),
    h("button", { onClick: () => viewer.requestFullscreen() }, "Fullscreen"));

  main.append(bar, viewer);

  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  const rfb = new RFB(container, `${scheme}//${location.host}/api/v1/vnc`, {});
  rfb.scaleViewport = true;
  rfb.background = "#000";
  // An X server started with no cursor sends no cursor image, and noVNC's
  // fallback dot is invisible, so the pointer would seem not to move at all.
  rfb.showDotCursor = true;

  const onConnect = () => {
    state.textContent = "connected";
    state.className = "pill good";
  };
  const onDisconnect = (event) => {
    state.textContent = event.detail.clean ? "disconnected" : "connection lost";
    state.className = "pill bad";
  };

  rfb.addEventListener("connect", onConnect);
  rfb.addEventListener("disconnect", onDisconnect);

  return () => {
    rfb.removeEventListener("connect", onConnect);
    rfb.removeEventListener("disconnect", onDisconnect);
    rfb.disconnect();
  };
}
