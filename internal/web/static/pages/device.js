// Everything about the device itself rather than about what it is showing:
// the screens attached to it, the settings that need a restart, and the two
// logs worth reading from a distance.

import { h, clear } from "../dom.js";
import { api } from "../api.js";
import { field, checkbox, secondsOf } from "./content.js";

export function device(main) {
  const body = h("div");
  main.append(body);

  let configuration = null;
  let status = null;

  const load = async () => {
    clear(body);
    body.append(h("p", { class: "dim", text: "Loading…" }));
    try {
      [configuration, status] = await Promise.all([api.configuration(), api.status()]);
      draw();
    } catch (error) {
      clear(body);
      body.append(h("div", { class: "notice bad", text: String(error.message || error) }));
    }
  };

  const save = async () => {
    try {
      configuration = await api.saveConfiguration(configuration);
      draw("Saved.");
    } catch (error) {
      draw(null, String(error.message || error));
    }
  };

  function draw(good, bad) {
    clear(body);
    if (good) body.append(h("div", { class: "notice good", text: good }));
    if (bad) body.append(h("div", { class: "notice bad", text: bad }));

    if ((status.ignoredSettings || []).length) {
      body.append(h("div", { class: "notice bad" },
        h("div", { text: "The configuration file has settings this version does not have. They are ignored, and will be removed from the file the next time it is written." }),
        h("ul", { style: "margin:0.5rem 0 0 1rem" },
          status.ignoredSettings.map((one) => h("li", { class: "mono", text: one }))),
        h("div", { class: "dim", style: "margin-top:0.4rem", text: "If one of these is a key you meant to set, it is mistyped — from in front of the screen a mistyped key and a setting that does nothing look exactly the same." })));
    }

    body.append(identityCard(), connectorsCard(), displayCard(), browserCard(), watchdogCard(), inputCard(), soundAndClockCard(), remoteCard(), fleetCard(), actionsCard(), logCard());

    body.append(h("div", { class: "actions" },
      h("button", { class: "primary", onClick: save }, "Save"),
      h("button", { onClick: load }, "Discard changes")));
  }

  function identityCard() {
    return h("div", { class: "card" },
      h("h2", { text: "This device" }),
      h("div", { class: "row" },
        field("Name", "text", configuration.device.name, (value) => { configuration.device.name = value; }),
        field("Where it is", "text", configuration.device.location, (value) => { configuration.device.location = value; }),
        field("Timezone", "text", configuration.device.timezone, (value) => { configuration.device.timezone = value; })),
      h("div", { class: "readout" },
        h("span", { class: "label", text: "Identifier" }),
        h("span", { class: "value mono", text: configuration.device.identifier })),
      h("div", { class: "readout" },
        h("span", { class: "label", text: "Version" }),
        h("span", { class: "value", text: status.device.version })),
      h("div", { class: "readout" },
        h("span", { class: "label", text: "Daemon up" }),
        h("span", { class: "value", text: status.device.uptime })));
  }

  function connectorsCard() {
    const connectors = status.connectors || [];
    if (!connectors.length) {
      return h("div", { class: "card" },
        h("h2", { text: "Sockets" }),
        h("p", { class: "dim", text: "The kernel reports no display connectors. Inside a container that usually means /dev/dri was not passed through." }));
    }

    return h("div", { class: "card" },
      h("h2", { text: "Sockets" }),
      h("table", {},
        h("thead", {}, h("tr", {},
          h("th", { text: "Socket" }),
          h("th", { text: "Monitor" }),
          h("th", { text: "Best mode" }),
          h("th", { class: "right", text: "State" }))),
        h("tbody", {}, connectors.map((connector) => h("tr", {},
          h("td", { class: "mono", text: connector.name }),
          h("td", { text: connector.monitor || "—" }),
          h("td", { class: "mono", text: (connector.modes || [])[0] || "—" }),
          h("td", { class: "right" },
            h("span", { class: `pill ${connector.connected ? "good" : ""}`, text: connector.connected ? "plugged in" : "empty" })))))));
  }

  function displayCard() {
    const outputs = configuration.display.outputs || [];
    return h("div", { class: "card" },
      h("h2", { text: "Screen" }),
      h("p", { class: "dim", text: "An entry named * applies to every socket that no other entry names, which is why this works on a machine nobody has looked at." }),
      outputs.map((output) => h("div", { class: "row" },
        field("Socket", "text", output.name, (value) => { output.name = value; }, "A name like HDMI-1, or * for all"),
        field("Mode", "text", output.mode, (value) => { output.mode = value; }, "preferred, off, or 1920x1080"),
        field("Rotation", "text", output.rotate, (value) => { output.rotate = value; }, "normal, left, right, inverted"),
        field("Position", "text", output.position, (value) => { output.position = value; }, "0x0"))),
      h("div", { class: "row" },
        field("Force the drawing surface size", "text", configuration.display.framebuffer, (value) => { configuration.display.framebuffer = value; }, "Empty fits the screens; 1920x1080 for a television that lies"),
        h("div", {},
          checkbox("Show the mouse pointer", configuration.display.cursor, (value) => { configuration.display.cursor = value; })),
        field("Blank the screen after", "number", secondsOf(configuration.display.blankAfter), (value) => {
          const seconds = Math.max(0, parseInt(value, 10) || 0);
          configuration.display.blankAfter = `${seconds}s`;
        }, "Seconds of no input. 0 never blanks, which is what a wall display wants")),
      h("details", {},
        h("summary", { text: "Difficult hardware" }),
        h("div", {},
          field("Custom modeline", "text", configuration.display.modeline, (value) => { configuration.display.modeline = value; }, "For a television with a broken EDID, in xrandr --newmode format"),
          h("label", {},
            h("span", { text: "Extra X server configuration" }),
            textarea(configuration.display.xorgConfiguration, (value) => { configuration.display.xorgConfiguration = value; })))));
  }

  // Everything about the browser that is a decision rather than a detail. The
  // binary, the profile paths and the extra arguments stay in the file: they
  // are for somebody debugging, not for somebody setting a screen up.
  function browserCard() {
    return h("div", { class: "card" },
      h("h2", { text: "The browser" }),
      h("div", { class: "row" },
        h("div", {},
          checkbox("Dark", configuration.browser.darkMode, (value) => {
            configuration.browser.darkMode = value;
            draw();
          }),
          h("span", { class: "dim", text: "A dashboard on a wall in a dark room at full brightness is what people complain about first. Pages that offer a dark theme are asked for it." }),
          configuration.browser.darkMode
            ? h("div", { style: "margin-top:0.5rem" },
                checkbox("Darken pages that ignore it", configuration.browser.forceDarkContent, (value) => { configuration.browser.forceDarkContent = value; }),
                h("span", { class: "dim", text: "Some pages have a theme of their own, set in an account somewhere and defaulting to light, and take no notice of what the browser prefers. This inverts their colours anyway. It is not as good as a page's own dark theme, so leave it off unless the screen is still bright." }))
            : null),
        h("div", {},
          checkbox("Sandbox", configuration.browser.sandbox, (value) => { configuration.browser.sandbox = value; }),
          h("span", { class: "dim", text: "Leave on. Off removes the boundary between a page and this machine, and is only for a container that cannot be given the privileges the sandbox needs." }))),
      h("div", { class: "row" },
        h("div", {},
          checkbox("Accept certificates that do not verify", configuration.browser.ignoreCertificateErrors, (value) => { configuration.browser.ignoreCertificateErrors = value; }),
          h("span", { class: "dim", text: "For an appliance on a private network with its own certificate. It removes the protection TLS was there to give, on every page, so turn it on only for a network you control." })),
        h("div", {},
          checkbox("Forget everything on restart", configuration.browser.ephemeralCache, (value) => { configuration.browser.ephemeralCache = value; }),
          h("span", { class: "dim", text: "Starts with an empty profile every time. It cures a corrupted cache permanently, at the cost of signing in again after every restart." }))),
      h("div", { class: "row" },
        field("Scale", "number", configuration.browser.deviceScaleFactor, (value) => {
          const scale = parseFloat(value);
          configuration.browser.deviceScaleFactor = isNaN(scale) ? 1 : scale;
        }, "1 gives a page the pixels the screen actually has. Raise it for a screen somebody stands close to. 0 lets the browser decide from what the panel claims its size is, which on a television is often nonsense.")),
      h("div", { class: "row" },
        h("div", {},
          checkbox("Close windows this daemon did not open", configuration.browser.closeUnexpectedTabs, (value) => { configuration.browser.closeUnexpectedTabs = value; }),
          h("span", { class: "dim", text: "A page that opens a window gets one stacked in front of the dashboard, and with no window manager it stays there. Windows are given a moment to close themselves first, and what was closed is written to the log. Turn it off if a page here signs in through a popup." }))),
      h("details", { open: (configuration.browser.certificateAuthorities || []).length > 0 },
        h("summary", { text: "Certificates this device trusts" }),
        h("div", {},
          h("p", { class: "dim", text: "An appliance on a private network signs its own certificate and the browser refuses the page. Paste that certificate here and it is trusted, and everything else goes on being checked — which is what makes this better than accepting every certificate above." }),
          (configuration.browser.certificateAuthorities || []).map((authority, index) => h("div", {},
            h("label", {},
              h("span", { text: `Certificate ${index + 1}` }),
              textarea(authority, (value) => { configuration.browser.certificateAuthorities[index] = value; })),
            h("div", { class: "actions" },
              h("button", {
                class: "danger",
                onClick: () => {
                  configuration.browser.certificateAuthorities.splice(index, 1);
                  draw();
                },
              }, "Remove")))),
          h("div", { class: "actions" },
            h("button", {
              onClick: () => {
                configuration.browser.certificateAuthorities = configuration.browser.certificateAuthorities || [];
                configuration.browser.certificateAuthorities.push("");
                draw();
              },
            }, "Add a certificate")))),
      h("p", { class: "dim", text: "Changing any of these restarts the browser." }));
  }

  // The ladder the watchdog climbs. Each rung is tried only after the one
  // before it failed to help, which is why the numbers only ever go up.
  function watchdogCard() {
    const watchdog = configuration.watchdog;
    const rung = (label, name, hint) => field(label, "number", watchdog[name], (value) => {
      watchdog[name] = Math.max(0, parseInt(value, 10) || 0);
    }, hint);

    return h("div", { class: "card" },
      h("h2", { text: "When the screen stops changing" }),
      h("p", { class: "dim", text: "The daemon asks the page to prove it is still running — that the X server answers, that the page runs a line of JavaScript, and that it is still being drawn. A page can look perfect and be dead, so the last of those is the one that matters." }),
      h("div", { class: "row" },
        h("div", {},
          checkbox("Watch for a frozen screen", watchdog.enabled, (value) => {
            watchdog.enabled = value;
            draw();
          })),
        field("Check every", "number", secondsOf(watchdog.interval), (value) => {
          watchdog.interval = `${Math.max(1, parseInt(value, 10) || 1)}s`;
        }, "Seconds"),
        field("Give up on an answer after", "number", secondsOf(watchdog.timeout), (value) => {
          watchdog.timeout = `${Math.max(1, parseInt(value, 10) || 1)}s`;
        }, "Seconds")),
      watchdog.enabled ? h("details", {},
        h("summary", { text: "What it does, in order" }),
        h("div", { class: "row" },
          rung("Reload the page after", "failuresBeforeReload", "consecutive failures"),
          rung("Open a fresh tab after", "failuresBeforeRecreate", "consecutive failures"),
          rung("Throw the cache away after", "failuresBeforeClearCache", "consecutive failures"),
          rung("Restart the browser after", "failuresBeforeRestart", "consecutive failures"),
          rung("Restart the X server after", "failuresBeforeRestartDisplay", "consecutive failures; 0 never does"))) : null);
  }

  function inputCard() {
    const devices = (status.input || []).filter((one) => one.keyboard || one.pointer || one.touch);
    if (!devices.length) {
      return h("div", { class: "card" },
        h("h2", { text: "Things people touch" }),
        h("p", { class: "dim", text: "The kernel reports no keyboard, pointer or touchscreen. Inside a container that usually means /dev/input was not passed through." }));
    }

    const describe = (one) => {
      const kinds = [];
      if (one.touch && one.direct) kinds.push("touchscreen");
      else if (one.touch) kinds.push("touchpad");
      if (one.keyboard) kinds.push("keyboard");
      if (one.pointer && !one.touch) kinds.push("pointer");
      return kinds.join(", ") || "other";
    };

    return h("div", { class: "card" },
      h("h2", { text: "Things people touch" }),
      devices.map((one) => h("div", { class: "readout" },
        h("span", { class: "label truncate", text: one.name }),
        h("span", { class: "value dim", text: describe(one) }))));
  }

  function soundAndClockCard() {
    const clock = status.clock || {};
    const devices = status.sound || [];

    return h("div", { class: "card" },
      h("h2", { text: "Sound and time" }),
      h("div", { class: "row" },
        h("div", {},
          checkbox("Play sound", configuration.audio.enabled, (value) => { configuration.audio.enabled = value; })),
        field("Sound card", "text", configuration.audio.sink, (value) => { configuration.audio.sink = value; },
          devices.length ? `Empty lets ALSA choose. Available: ${devices.map((one) => one.alsaName || `plughw:${one.identifier}`).join(", ")}` : "This machine reports no sound cards"),
        field("Volume", "number", configuration.audio.volume, (value) => {
          configuration.audio.volume = Math.min(100, Math.max(0, parseInt(value, 10) || 0));
        }, "0 to 100"),
        field("Time servers", "text", (configuration.time.servers || []).join(", "), (value) => {
          configuration.time.servers = value.split(",").map((one) => one.trim()).filter(Boolean);
        })),
      devices.length
        ? devices.map((one) => h("div", { class: "readout" },
            h("span", { class: "label", text: one.name || one.identifier }),
            h("span", { class: "value dim" },
              h("span", { class: "mono", text: `plughw:${one.identifier}` }),
              " ",
              one.playback ? "out" : "",
              one.capture ? " in" : "")))
        : null,
      h("div", { class: "readout" },
        h("span", { class: "label", text: "Clock" }),
        h("span", { class: "value" },
          clock.enabled
            ? h("span", { class: `pill ${clock.synchronised ? "good" : "warn"}`, text: clock.synchronised ? `synchronised with ${clock.reference}` : "not synchronised yet" })
            : h("span", { class: "pill", text: "not managed here" }))),
      clock.enabled && clock.synchronised
        ? h("div", { class: "readout" },
            h("span", { class: "label", text: "Off by" }),
            h("span", { class: "value", text: `${(clock.offsetSeconds * 1000).toFixed(0)} ms` }))
        : null,
      clock.problem ? h("div", { class: "notice bad", text: clock.problem }) : null);
  }

  function remoteCard() {
    return h("div", { class: "card" },
      h("h2", { text: "Remote access" }),
      h("div", { class: "row" },
        field("VNC listens on", "text", configuration.vnc.listen, (value) => { configuration.vnc.listen = value; }, "127.0.0.1:5900 keeps it behind this interface"),
        field("VNC password", "password", configuration.vnc.password, (value) => { configuration.vnc.password = value; }, "Only needed if you move it off the loopback address"),
        h("div", {},
          checkbox("VNC viewers may only watch, not type", configuration.vnc.viewOnly, (value) => { configuration.vnc.viewOnly = value; }))),
      h("div", { class: "row" },
        field("This interface listens on", "text", configuration.web.listen, (value) => { configuration.web.listen = value; }, "Changing this needs a restart of the container"),
        field("Stay signed in for", "number", secondsOf(configuration.web.sessionLifetime), (value) => {
          const seconds = Math.max(60, parseInt(value, 10) || 60);
          configuration.web.sessionLifetime = `${seconds}s`;
        }, "Seconds. Long, because signing in to a screen on a wall is a trip across the building")));
  }

  function fleetCard() {
    const state = status.fleet || {};

    if (state.enrolled) {
      return h("div", { class: "card" },
        h("h2", { text: "Fleet management" }),
        h("div", { class: "readout" },
          h("span", { class: "label", text: "Service" }),
          h("span", { class: "value mono truncate", text: state.url })),
        h("div", { class: "readout" },
          h("span", { class: "label", text: "Connection" }),
          h("span", { class: "value" },
            h("span", { class: `pill ${state.connected ? "good" : "bad"}`, text: state.connected ? "connected" : "not connected" }))),
        h("div", { class: "readout" },
          h("span", { class: "label", text: "Requests served" }),
          h("span", { class: "value", text: String(state.streamsServed || 0) })),
        state.lastError ? h("div", { class: "notice bad", text: state.lastError }) : null,
        h("p", { class: "dim", text: "The device holds one connection out to the service; nothing is opened on the network here. What the service can do through it is exactly what this page can do." }),
        h("div", { class: "actions" },
          h("button", {
            class: "danger",
            onClick: async () => {
              try {
                await api.leaveFleet();
                draw("This device is no longer enrolled.");
              } catch (error) {
                draw(null, String(error.message || error));
              }
            },
          }, "Unenrol this device")));
    }

    const url = h("input", { type: "url", value: configuration.fleet.url || "https://cue.sh" });
    const token = h("input", { type: "password", placeholder: "The token from the service" });

    return h("div", { class: "card" },
      h("h2", { text: "Fleet management" }),
      h("p", { class: "dim", text: "Optional. Enrol this device with a management service to run it alongside others. Nothing is contacted until you do." }),
      h("div", { class: "row" },
        h("label", {}, h("span", { text: "Service" }), url),
        h("label", {}, h("span", { text: "Enrolment token" }), token)),
      state.lastError ? h("div", { class: "notice bad", text: state.lastError }) : null,
      h("div", { class: "actions" },
        h("button", {
          onClick: async () => {
            try {
              await api.enrolInFleet(url.value, token.value);
              draw("Enrolling. It may take a moment to connect.");
            } catch (error) {
              draw(null, String(error.message || error));
            }
          },
        }, "Enrol")));
  }

  function actionsCard() {
    const restart = async (program) => {
      try {
        await api.restart(program);
        draw(`Restarted ${program}.`);
      } catch (error) {
        draw(null, String(error.message || error));
      }
    };

    const address = h("input", { type: "url", placeholder: "https://example.com/" });

    return h("div", { class: "card" },
      h("h2", { text: "Do something now" }),
      h("div", { class: "actions", style: "margin-bottom:0.75rem" },
        h("button", { onClick: () => restart("chromium") }, "Restart the browser"),
        h("button", { onClick: () => restart("display") }, "Restart the X server"),
        h("button", { onClick: () => restart("vnc") }, "Restart the VNC server")),
      h("label", {}, h("span", { text: "Show an address once, without changing the playlist" }), address),
      h("div", { class: "actions" },
        h("button", {
          onClick: async () => {
            try {
              await api.navigate(address.value);
              draw("Showing it now. The next rotation puts the playlist back.");
            } catch (error) {
              draw(null, String(error.message || error));
            }
          },
        }, "Show it")));
  }

  function logCard() {
    const output = h("pre", { class: "log", text: "" });
    const load = async () => {
      output.textContent = "Loading…";
      try {
        output.textContent = (await api.xorgLog()) || "The X server has not written a log yet.";
        output.scrollTop = output.scrollHeight;
      } catch (error) {
        output.textContent = String(error.message || error);
      }
    };

    return h("div", { class: "card" },
      h("h2", { text: "X server log" }),
      h("p", { class: "dim", text: "When a screen stays black, the reason is in here." }),
      h("div", { class: "actions", style: "margin-bottom:0.75rem" }, h("button", { onClick: load }, "Read the end of it")),
      output);
  }

  function textarea(value, onChange) {
    const element = h("textarea", {}, value || "");
    element.addEventListener("input", () => onChange(element.value));
    return element;
  }

  load();
}
