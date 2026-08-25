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

  // A phone has no keyboard until something asks for one. noVNC listens for
  // key events on the canvas, and a canvas cannot be focused by a finger, so
  // there is nothing to type into and no keyboard appears — which makes the
  // one thing somebody most often wants from this page, signing a dashboard
  // back in, impossible from the device they are holding.
  //
  // So: an input positioned over nothing, focused when the button is pressed.
  // The phone raises its keyboard for the input, and every key is forwarded to
  // the screen.
  const keyboardTrap = h("input", {
    type: "text",
    autocapitalize: "off",
    autocomplete: "off",
    autocorrect: "off",
    spellcheck: "false",
    "aria-label": "Keyboard for the screen",
    style: "position:absolute; opacity:0; pointer-events:none; width:1px; height:1px; border:0; padding:0",
  });
  keyboardTrap.addEventListener("keydown", (event) => event.stopPropagation());
  keyboardTrap.addEventListener("input", () => {
    for (const character of keyboardTrap.value) {
      rfb.sendKey(character.codePointAt(0), null, true);
      rfb.sendKey(character.codePointAt(0), null, false);
    }
    keyboardTrap.value = "";
  });

  const bar = h("div", { class: "actions", style: "margin-bottom:0.75rem" },
    state,
    h("span", { class: "dim mobile-hide", text: "Click and type to drive the screen." }),
    h("span", { style: "margin-left:auto" }),
    h("button", {
      onClick: () => {
        keyboardTrap.focus();
        rfb.focus();
      },
    }, "Keyboard"),
    h("button", {
      onClick: () => {
        if (document.fullscreenElement) document.exitFullscreen();
        else viewer.requestFullscreen().catch(() => {});
      },
    }, "Fullscreen"));

  main.append(bar, viewer, keyboardTrap);

  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  const rfb = new RFB(container, `${scheme}//${location.host}/api/v1/vnc`, {});
  rfb.scaleViewport = true;
  rfb.background = "#000";
  // An X server started with no cursor sends no cursor image, and noVNC's
  // fallback dot is invisible, so the pointer would seem not to move at all.
  rfb.showDotCursor = true;

  // Rotating the picture to fit a phone held upright was tried and taken out
  // again. Turning the canvas with CSS turns what you see and not where noVNC
  // thinks you touched: it works the pointer position out from the element's
  // bounding box, so every tap would land a quarter turn away from where the
  // finger went. A view somebody cannot drive is worse than one they have to
  // turn the phone for, and turning the phone is a gesture they already have.

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
