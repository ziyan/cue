// The machine's own network: what it is connected to, and how to change it.
//
// This page exists for the moment a screen is carried into a room, plugged in
// and switched on. It has no keyboard and no network, and the one thing it
// must be able to do is join the wireless network of the room it is in.

import { h, clear, bytes } from "../dom.js";
import { api } from "../api.js";
import { field, checkbox, choice, secondsOf } from "./content.js";

export function network(main) {
  const body = h("div");
  main.append(body);

  let configuration = null;
  let state = null;
  let scans = {};

  const load = async () => {
    clear(body);
    body.append(h("p", { class: "dim", text: "Loading…" }));
    try {
      [configuration, state] = await Promise.all([api.configuration(), api.network()]);
      draw();
    } catch (error) {
      clear(body);
      body.append(h("div", { class: "notice bad", text: String(error.message || error) }));
    }
  };

  const save = async () => {
    try {
      configuration = await api.saveConfiguration(configuration);
      state = await api.network();
      draw("Saved. Changes are applied within half a minute.");
    } catch (error) {
      draw(null, String(error.message || error));
    }
  };

  function draw(good, bad) {
    clear(body);
    if (good) body.append(h("div", { class: "notice good", text: good }));
    if (bad) body.append(h("div", { class: "notice bad", text: bad }));

    if (!state.manageable && state.problem) {
      body.append(h("div", { class: "notice bad" },
        h("div", { text: state.problem }),
        h("div", { class: "dim", style: "margin-top:0.4rem", text: "The interfaces below are what this process can see, which is not the machine's network." })));
    }

    body.append(h("div", { class: "card" },
      h("h2", { text: "Managing the network" }),
      h("p", { class: "dim", text: "Off, and the machine keeps whatever network setup it already has — which for a screen plugged into a wired network is an address it was given without being asked, and nothing to do here. On, and this daemon sets the interfaces you name below: what a screen on a wireless network needs, and what a screen that has to sit at a fixed address needs." }),
      checkbox("Set the network from here", configuration.network.manage, (value) => {
        configuration.network.manage = value;
        draw();
      })));

    body.append(h("div", { class: "card" },
      h("h2", { text: "Setting up from a phone" }),
      h("p", { class: "dim", text: "A device with no network can run a temporary wireless network of its own and show a code on its screen. Scanning that code with a phone joins it and opens a page for choosing the real network, so a screen in a room with no ethernet can still be set up. The password for that temporary network appears only on the screen, so whoever sets the device up has to be able to see it." }),
      choice("When to offer it", [
        { value: "auto", label: "Only when this device has no network" },
        { value: "always", label: "Whenever the hardware allows" },
        { value: "off", label: "Never" },
      ], configuration.network.onboarding || "auto", (value) => {
        configuration.network.onboarding = value;
        draw();
      }, configuration.network.onboarding === "always"
        ? "Anybody who can see this screen can reconfigure it. Meant for trying the feature out."
        : null),
      field("Give up on the network after, in minutes", "number",
        Math.round(secondsOf(configuration.network.lostAfter) / 60), (value) => {
          const minutes = Math.max(1, parseInt(value, 10) || 0);
          configuration.network.lostAfter = `${minutes * 60}s`;
        }, "With nothing reachable for this long, the screen shows its setup code again. It keeps trying the real network meanwhile, so a router rebooting costs nothing.")));

    // Only the interfaces with hardware behind them. A machine running
    // containers has a Docker bridge, a veth for every running container and
    // whatever a VPN left behind. None of those is something to offer
    // somebody setting up a screen, so none of them is shown at all.
    //
    // One that is named in the configuration is shown whatever it is: an
    // interface an operator has already configured must never quietly
    // disappear from the page that configures it.
    const all = state.interfaces || [];
    const configured = new Set((configuration.network.interfaces || []).map((one) => one.name));
    const hardware = all.filter((one) => one.physical || configured.has(one.name));

    if (hardware.length) {
      body.append(...hardware.map(interfaceCard));
    } else {
      body.append(h("div", { class: "card" },
        h("h2", { text: "No network hardware" }),
        h("p", { class: "dim", text: all.length
          ? "This machine reports interfaces, but none of them has hardware behind it — they are bridges, container interfaces and tunnels. If this is running in a container of its own, it is seeing that container's network and not the machine's."
          : "The kernel reports no network interfaces at all." })));
    }

    body.append(h("div", { class: "actions" },
      h("button", { class: "primary", onClick: save }, "Save"),
      h("button", { onClick: load }, "Discard changes")));
  }

  // settingsFor finds this interface's entry in the configuration, creating
  // one the first time somebody changes something about it.
  function settingsFor(name, create) {
    configuration.network.interfaces = configuration.network.interfaces || [];
    let found = configuration.network.interfaces.find((one) => one.name === name);
    if (!found && create) {
      found = { name, method: "dhcp", nameservers: [] };
      configuration.network.interfaces.push(found);
    }
    return found;
  }

  function interfaceCard(one) {
    const settings = settingsFor(one.name, false);
    const managed = !!settings;

    const link = one.carrier ? "connected" : one.up ? "no cable" : "off";
    const tone = one.carrier ? "good" : "warn";

    return h("div", { class: "card" },
      h("h2", {},
        one.name,
        h("span", { class: "dim", text: ` · ${one.kind}` })),

      h("div", { class: "readout" },
        h("span", { class: "label", text: "State" }),
        h("span", { class: "value" }, h("span", { class: `pill ${tone}`, text: link }))),
      h("div", { class: "readout" },
        h("span", { class: "label", text: "Address" }),
        h("span", { class: "value mono", text: (one.addresses || []).join(", ") || "none" })),
      one.gateway ? h("div", { class: "readout" },
        h("span", { class: "label", text: "Gateway" }),
        h("span", { class: "value mono", text: one.gateway })) : null,
      (one.nameservers || []).length ? h("div", { class: "readout" },
        h("span", { class: "label", text: "Name servers" }),
        h("span", { class: "value mono truncate", text: one.nameservers.join(", ") })) : null,
      h("div", { class: "readout" },
        h("span", { class: "label", text: "Carried" }),
        h("span", { class: "value dim", text: `${bytes(one.receivedBytes)} in, ${bytes(one.transmittedBytes)} out` })),

      one.wireless ? wirelessStatus(one) : null,
      (state.errors || {})[one.name] ? h("div", { class: "notice bad", text: state.errors[one.name] }) : null,

      h("div", { class: "subsection" },
        h("h3", { text: managed ? "Set from here" : "Left as the machine set it up" }),
        h("div", {},
          checkbox("Set this interface from here", managed, (value) => {
            if (value) {
              settingsFor(one.name, true);
            } else {
              configuration.network.interfaces =
                (configuration.network.interfaces || []).filter((entry) => entry.name !== one.name);
            }
            draw();
          }),
          managed ? addressForm(one, settings) : null,
          managed && one.kind === "wireless" ? wirelessForm(one, settings) : null)));
  }

  function wirelessStatus(one) {
    const wireless = one.wireless;
    return h("div", {},
      h("div", { class: "readout" },
        h("span", { class: "label", text: "Wireless" }),
        h("span", { class: "value" },
          wireless.ssid
            ? h("span", {}, wireless.ssid, h("span", { class: "dim", text: ` · ${wireless.state.toLowerCase()}` }))
            : h("span", { class: "dim", text: wireless.state.toLowerCase() || "not joined" }))),
      wireless.signalStrength ? h("div", { class: "readout" },
        h("span", { class: "label", text: "Signal" }),
        h("span", { class: "value", text: `${wireless.signalStrength} dBm ${signalWord(wireless.signalStrength)}` })) : null);
  }

  function signalWord(strength) {
    if (strength >= -55) return "(strong)";
    if (strength >= -70) return "(usable)";
    return "(weak — the screen will drop out)";
  }

  function addressForm(one, settings) {
    const isStatic = settings.method === "static";

    return h("div", {},
      h("div", { class: "row" },
        h("label", {},
          h("span", { text: "Address" }),
          selector(["dhcp", "static"], settings.method || "dhcp", (value) => {
            settings.method = value;
            if (value !== "static") settings.address = "";
            draw();
          }))),
      isStatic ? h("div", { class: "row" },
        field("Address and prefix", "text", settings.address, (value) => { settings.address = value; }, "for example 192.0.2.10/24"),
        field("Gateway", "text", settings.gateway, (value) => { settings.gateway = value; }, "the router that reaches everything else")) : null,
      h("div", { class: "row" },
        field("Name servers", "text", (settings.nameservers || []).join(", "), (value) => {
          settings.nameservers = value.split(",").map((entry) => entry.trim()).filter(Boolean);
        }, "Empty uses whatever the network offers"),
        field("Search domain", "text", settings.searchDomain, (value) => { settings.searchDomain = value; })));
  }

  function wirelessForm(one, settings) {
    settings.wireless = settings.wireless || { ssid: "", passphrase: "" };

    const found = scans[one.name];
    const list = found
      ? (found.length
          ? h("div", {}, found.map((candidate) => h("div", { class: "readout" },
              h("span", { class: "label" },
                h("button", {
                  onClick: () => {
                    settings.wireless.ssid = candidate.ssid;
                    draw();
                  },
                }, candidate.ssid)),
              h("span", { class: "value dim", text: `${candidate.signalStrength} dBm · ${candidate.security}` }))))
          : h("p", { class: "dim", text: "Nothing was found. The radio may be off, or there may be nothing in range." }))
      : null;

    return h("div", {},
      h("div", { class: "row" },
        field("Network name", "text", settings.wireless.ssid, (value) => { settings.wireless.ssid = value; }),
        field("Password", "password", settings.wireless.passphrase, (value) => { settings.wireless.passphrase = value; },
          "Empty for an open network")),
      h("div", { class: "actions" },
        h("button", {
          onClick: async (event) => {
            const button = event.target;
            button.disabled = true;
            button.textContent = "Looking…";
            try {
              const answer = await api.scanWireless(one.name);
              scans[one.name] = answer.networks || [];
              draw();
            } catch (error) {
              draw(null, String(error.message || error));
            }
          },
        }, "Look for networks")),
      list ? h("div", { class: "card", style: "margin-top:0.75rem" },
        h("h2", { text: "Within reach" }), list) : null);
  }

  function selector(options, value, onChange) {
    const element = h("select", {});
    for (const option of options) {
      element.append(h("option", { value: option, selected: option === value }, option));
    }
    element.addEventListener("change", () => onChange(element.value));
    return element;
  }

  load();
}
