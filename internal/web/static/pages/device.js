// Everything about the device itself rather than about what it is showing:
// the screens attached to it, the settings that need a restart, and the two
// logs worth reading from a distance.

import { h, clear } from "../dom.js";
import { api } from "../api.js";
import { field, checkbox } from "./content.js";

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

    body.append(identityCard(), connectorsCard(), displayCard(), soundAndClockCard(), remoteCard(), actionsCard(), logCard());

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
          checkbox("Show the mouse pointer", configuration.display.cursor, (value) => { configuration.display.cursor = value; }))),
      h("details", {},
        h("summary", { text: "Difficult hardware" }),
        h("div", {},
          field("Custom modeline", "text", configuration.display.modeline, (value) => { configuration.display.modeline = value; }, "For a television with a broken EDID, in xrandr --newmode format"),
          h("label", {},
            h("span", { text: "Extra X server configuration" }),
            textarea(configuration.display.xorgConfiguration, (value) => { configuration.display.xorgConfiguration = value; })))));
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
        field("This interface listens on", "text", configuration.web.listen, (value) => { configuration.web.listen = value; }, "Changing this needs a restart of the container")));
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
