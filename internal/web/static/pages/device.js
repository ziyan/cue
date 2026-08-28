// The device's own settings, as several pages rather than one.
//
// They were one page: forty-two fields in ten cards, three and a half thousand
// pixels tall, with six of the cards hiding further settings inside collapsed
// boxes labelled "Less often needed" and "Difficult hardware" -- and one
// labelled nothing at all. Finding a setting meant opening every box to see
// whether it was in there.
//
// Every page here shares one load and one save, because they are all views of
// the same configuration document: saving on any of them sends the whole thing,
// as it always did.

import { h, clear } from "../dom.js";
import { api } from "../api.js";
import { warnBeforeLeaving } from "../leaving.js";
import { field, checkbox, secondsOf, choice, searchable } from "./content.js";

// settingsPage builds one of these pages from the cards it should carry.
function settingsPage(wanted) {
  return function (main) {
  const body = h("div");
  main.append(body);

  let configuration = null;
  let timezones = [];
  let status = null;

  // The configuration as it was when it arrived, or as it was last saved.
  // Everything about whether there is anything to save is decided by comparing
  // against this rather than by watching for edits: a field that is typed into
  // and then typed back is not a change, and offering to discard nothing is a
  // button that does nothing.
  let asItArrived = "";
  // Nothing loaded is not unsaved work. Without the first test this said yes
  // while the page was still fetching, so moving to another page during the
  // half second it takes asked whether to throw away changes nobody had made.
  const changed = () => configuration !== null && JSON.stringify(configuration) !== asItArrived;

  warnBeforeLeaving(() => changed());

  const load = async () => {
    clear(body);
    body.append(h("p", { class: "dim", text: "Loading…" }));
    try {
      [configuration, status, timezones] = await Promise.all([
        api.configuration(), api.status(), api.timezones().catch(() => []),
      ]);
      asItArrived = JSON.stringify(configuration);
      draw();
    } catch (error) {
      clear(body);
      body.append(h("div", { class: "notice bad", text: String(error.message || error) }));
    }
  };

  const save = async () => {
    try {
      configuration = await api.saveConfiguration(configuration);
      asItArrived = JSON.stringify(configuration);
      // The shell keeps the name — it is in the header and in the browser tab
      // — and would otherwise show the old one until somebody reloaded.
      window.dispatchEvent(new CustomEvent("device-renamed", {
        detail: { name: configuration.device.name },
      }));
      draw("Saved.");
    } catch (error) {
      draw(null, String(error.message || error));
    }
  };

  // A switch that reveals or hides other fields still has to finish moving
  // before the page is rebuilt under it. Rebuilding immediately replaces the
  // switch with a new element already in its new position, so it jumps rather
  // than slides -- the transition is defined and never runs.
  const drawAfterTheSwitch = () => setTimeout(() => draw(), 180);

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

    const cards = {
      identity: identityCard, connectors: connectorsCard, display: displayCard,
      browser: browserCard, watchdog: watchdogCard, input: inputCard,
      sound: soundCard, clock: clockCard, remote: remoteCard,
      actions: actionsCard, log: logCard,
    };
    body.append(...wanted.map((name) => cards[name]()));

    if (wanted.some((name) => name !== "log" && name !== "actions")) {
      const saveButton = h("button", { class: "primary", onClick: save }, "Save");
      const discardButton = h("button", { onClick: load }, "Discard changes");

      // Both follow whether there is anything to act on. Watching for input
      // rather than redrawing on every keystroke: redrawing would take the
      // cursor out of the field somebody is typing in.
      const followTheForm = () => {
        const anything = changed();
        saveButton.disabled = !anything;
        discardButton.hidden = !anything;
      };
      followTheForm();
      body.addEventListener("input", followTheForm);
      body.addEventListener("change", followTheForm);

      body.append(h("div", { class: "actions" }, saveButton, discardButton));
    }
  }

  function identityCard() {
    return h("div", { class: "card" },
      h("h2", { text: "This device" }),
      h("div", { class: "row" },
        field("Name", "text", configuration.device.name, (value) => { configuration.device.name = value; }),
        field("Where it is", "text", configuration.device.location, (value) => { configuration.device.location = value; }),
        choice("Language on the screen", [
          { value: "", label: "English" },
          { value: "zh", label: "中文" },
          { value: "ja", label: "日本語" },
        ], configuration.device.language || "", (value) => {
          configuration.device.language = value;
        }, "What the menu somebody opens at the screen is written in. Whoever is standing there can also change it from that menu, and the choice comes back here.")),
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
            h("span", { class: `pill ${connector.connected ? "good" : ""}`, text: connector.connected ? "plugged in" : "empty" })))))),
      connectors.filter((one) => one.display).map(monitorDetail));
  }

  // What the monitor says about itself, which is where most hard questions
  // about one of these screens are answered: why a page is scaled, whether
  // the mode being driven is the panel's own, which socket the television is
  // on.
  function monitorDetail(connector) {
    const monitor = connector.display;
    const density = monitor.widthMillimetres && monitor.preferredMode
      ? Math.round(parseInt(monitor.preferredMode, 10) / (monitor.widthMillimetres / 25.4))
      : 0;

    const row = (label, value) => value
      ? h("div", { class: "readout" },
          h("span", { class: "label", text: label }),
          h("span", { class: "value mono", text: String(value) }))
      : null;

    return h("details", {},
      h("summary", {}, connector.name, h("span", { class: "dim", text: ` · what the monitor reports about itself` })),
      h("div", {},
        row("Maker", monitor.manufacturer),
        row("Model", monitor.model),
        row("Serial", monitor.serial),
        row("Made", monitor.year),
        row("Panel", monitor.widthMillimetres ? `${monitor.widthMillimetres} x ${monitor.heightMillimetres} mm` : ""),
        row("Native mode", monitor.preferredMode),
        row("Density", density ? `${density} dpi` : ""),
        row("Input", monitor.digital ? "digital" : "analogue"),
        row("EDID version", monitor.version),
        density > 0 ? h("p", { class: "dim", text: "Density is what a browser would scale the page by if it were asked. Cue does not ask: browser.deviceScaleFactor decides, so a monitor that reports its size wrongly cannot shrink the dashboard into a corner." }) : null,
        (connector.modes || []).length
          ? h("details", {},
              h("summary", { text: `${connector.modes.length} modes offered` }),
              h("div", { class: "mono dim", text: connector.modes.join("  ") }))
          : null));
  }

  function displayCard() {
    const outputs = configuration.display.outputs || [];
    return h("div", { class: "card" },
      h("h2", { text: "Screen" }),
      h("p", { class: "dim", text: "An entry named * applies to every socket that no other entry names, which is why this works on a machine nobody has looked at." }),
      // Two questions per screen: how big, and which way up. Those are the
      // ones anybody actually asks. Everything else this section can express
      // is below, folded away, because a page of knobs makes the two that
      // matter harder to find rather than easier.
      outputs.map((output) => h("div", { class: "row" },
        choice("Socket", socketOptions(), output.name, (value) => { output.name = value; },
          "* means every socket on the machine"),
        choice("Size", modeOptions(output.name), output.mode, (value) => { output.mode = value; },
          "preferred is the monitor's own native size, and is nearly always right"),
        choice("Which way up", ["normal", "left", "right", "inverted"], output.rotate || "normal", (value) => { output.rotate = value; }))),

      h("div", { class: "row" },
        field("Blank the screen after", "number", secondsOf(configuration.display.blankAfter), (value) => {
          const seconds = Math.max(0, parseInt(value, 10) || 0);
          configuration.display.blankAfter = `${seconds}s`;
        }, "Seconds of no input. 0 never blanks, which is what a wall display wants")),

      h("div", { class: "subsection" },
        h("h3", { text: "Colour, size and the console" }),
        h("div", {},
          outputs.map((output) => h("div", { class: "row" },
            field(`Where ${output.name} sits`, "text", output.position, (value) => { output.position = value; },
              "0x0. Only means anything with more than one screen"))),
          h("div", { class: "row" },
            field("Force the drawing surface size", "text", configuration.display.framebuffer, (value) => { configuration.display.framebuffer = value; }, "Empty fits the screens; 1920x1080 for a television that lies about its size"),
            h("div", {},
              h("label", {},
                h("span", { text: "Mouse pointer" }),
                selector(["auto", "hidden", "always"], configuration.display.cursor || "auto", (value) => {
                  configuration.display.cursor = value;
                }),
                h("span", { class: "dim", style: "margin-top:0.25rem", text: "Auto shows it while somebody is moving it and hides it again when they stop. Hidden means the screen has no pointer at all, which makes a touchscreen or a mouse impossible to aim." })),
              (configuration.display.cursor || "auto") === "auto"
                ? field("Hide it again after", "number", secondsOf(configuration.display.cursorIdleTimeout), (value) => {
                    const seconds = Math.max(1, parseInt(value, 10) || 3);
                    configuration.display.cursorIdleTimeout = `${seconds}s`;
                  }, "Seconds of not moving")
                : null),
            h("div", {},
              checkbox("Show the Cue mark before the browser has drawn", configuration.display.wallpaper, (value) => { configuration.display.wallpaper = value; }),
              h("span", { class: "dim", text: "What the screen shows while it is starting, and if the browser goes away. Off leaves whatever the X server does, which on a wall is indistinguishable from a machine that failed to boot." }))))),

      h("div", { class: "subsection" },
        h("h3", { text: "Graphics driver and screen size" }),
        h("div", {},
          field("Custom modeline", "text", configuration.display.modeline, (value) => { configuration.display.modeline = value; }, "For a television with a broken EDID, in xrandr --newmode format"),
          h("label", {},
            h("span", { text: "Extra X server configuration" }),
            textarea(configuration.display.xorgConfiguration, (value) => { configuration.display.xorgConfiguration = value; })))));
  }

  // Everything about the browser that is a decision rather than a detail. The
  // binary, the profile paths and the extra arguments stay in the file: they
  // are for somebody debugging, not for somebody setting a screen up.
  function selector(options, value, onChange) {
    const element = h("select", {});
    for (const option of options) {
      element.append(h("option", { value: option, selected: option === value }, option));
    }
    element.addEventListener("change", () => onChange(element.value));
    return element;
  }

  function browserCard() {
    return h("div", { class: "card" },
      h("h2", { text: "Browser" }),
      h("div", { class: "row" },
        h("div", {},
          checkbox("Ask pages for their dark version", configuration.browser.darkMode, (value) => {
            configuration.browser.darkMode = value;
            drawAfterTheSwitch();
          }),
          h("span", { class: "dim", text: "A dashboard on a wall in a dark room at full brightness is what people complain about first. Pages that offer a dark theme are asked for it." }),
          configuration.browser.darkMode
            ? h("div", { style: "margin-top:0.5rem" },
                checkbox("Darken pages that ignore it", configuration.browser.forceDarkContent, (value) => { configuration.browser.forceDarkContent = value; }),
                h("span", { class: "dim", text: "Some pages have a theme of their own, set in an account somewhere and defaulting to light, and take no notice of what the browser prefers. This inverts their colours anyway. It is not as good as a page's own dark theme, so leave it off unless the screen is still bright." }))
            : null),
        h("div", {},
          checkbox("Keep the browser sandbox on", configuration.browser.sandbox, (value) => { configuration.browser.sandbox = value; }),
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
      h("h2", { text: "Watchdog" }),
      h("p", { class: "dim", text: "The daemon asks the page to prove it is still running — that the X server answers, that the page runs a line of JavaScript, and that it is still being drawn. A page can look perfect and be dead, so the last of those is the one that matters." }),
      h("div", { class: "row" },
        h("div", {},
          checkbox("Watch for a frozen screen", watchdog.enabled, (value) => {
            watchdog.enabled = value;
            drawAfterTheSwitch();
          })),
        field("Check every", "number", secondsOf(watchdog.interval), (value) => {
          watchdog.interval = `${Math.max(1, parseInt(value, 10) || 1)}s`;
        }, "Seconds"),
        field("Give up on an answer after", "number", secondsOf(watchdog.timeout), (value) => {
          watchdog.timeout = `${Math.max(1, parseInt(value, 10) || 1)}s`;
        }, "Seconds")),
      watchdog.enabled ? h("div", { class: "subsection" },
        h("h3", { text: "What it tries, in order" }),
        h("div", { class: "row" },
          rung("Reload the page after", "failuresBeforeReload", "consecutive failures"),
          rung("Open a fresh tab after", "failuresBeforeRecreate", "consecutive failures"),
          rung("Throw the cache away after", "failuresBeforeClearCache", "consecutive failures"),
          rung("Restart the browser after", "failuresBeforeRestart", "consecutive failures"),
          rung("Restart the X server after", "failuresBeforeRestartDisplay", "consecutive failures; 0 never does"))) : null);
  }

  // Every socket the machine has, plus the wildcard.
  function socketOptions() {
    const names = (status.connectors || []).map((one) => one.name);
    return ["*", ...names];
  }

  // The modes the monitor on that socket actually advertises. A mode typed by
  // hand that the monitor does not have is a black screen, and the monitor
  // has already said what it can do.
  function modeOptions(name) {
    const base = ["preferred", "off"];
    const connectors = (status.connectors || []).filter(
      (one) => name === "*" || !name || one.name === name);
    const modes = [];
    for (const connector of connectors) {
      for (const mode of connector.modes || []) {
        if (!modes.includes(mode)) modes.push(mode);
      }
    }
    return [...base, ...modes];
  }

  function inputCard() {
    const devices = (status.input || []).filter((one) => one.keyboard || one.pointer || one.touch);
    if (!devices.length) {
      return h("div", { class: "card" },
        h("h2", { text: "Keyboard, mouse and touch" }),
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
      h("h2", { text: "Keyboard, mouse and touch" }),
      devices.map((one) => h("div", { class: "readout" },
        h("span", { class: "label truncate", text: one.name }),
        h("span", { class: "value dim", text: describe(one) }))));
  }

  function soundCard() {
    const devices = status.sound || [];

    return h("div", { class: "card" },
      h("h2", { text: "Sound" }),
      h("div", { class: "row" },
        h("div", {},
          checkbox("Play sound", configuration.audio.enabled, (value) => { configuration.audio.enabled = value; })),
        field("Sound card", "text", configuration.audio.sink, (value) => { configuration.audio.sink = value; },
          devices.length ? `Empty lets ALSA choose. Available: ${devices.map((one) => one.alsaName || `plughw:${one.identifier}`).join(", ")}` : "This machine reports no sound cards"),
        field("Volume", "number", configuration.audio.volume, (value) => {
          configuration.audio.volume = Math.min(100, Math.max(0, parseInt(value, 10) || 0));
        }, "0 to 100")),
      devices.length
        ? devices.map((one) => h("div", { class: "readout" },
            h("span", { class: "label", text: one.name || one.identifier }),
            h("span", { class: "value dim" },
              h("span", { class: "mono", text: `plughw:${one.identifier}` }),
              " ",
              one.playback ? "out" : "",
              one.capture ? " in" : "")))
        : null);
  }

  // Time was three fields interleaved with three about sound, in one card
  // called "Sound and the clock" on a page called Access. The time servers
  // were in there and nobody could find them, which is a fair test of whether
  // a place is the right one.
  function clockCard() {
    const clock = status.clock || {};

    return h("div", { class: "card" },
      h("h2", { text: "Time" }),
      h("div", { class: "row" },
        searchable("Timezone", timezones, configuration.device.timezone, (value) => {
          configuration.device.timezone = value;
        }, "What the screen and these logs call the time. It does not change the machine's own setting, which lives outside the container."),
        h("div", {},
          checkbox("Keep this device's clock", configuration.time.enabled, (value) => {
            configuration.time.enabled = value;
            drawAfterTheSwitch();
          }),
          h("span", { class: "dim", text: "On by default: a clock wrong by minutes makes every HTTPS dashboard refuse to load. Turn it off where the machine already runs chrony or systemd-timesyncd — two time daemons correcting one clock against each other is worse than neither." }))),
      configuration.time.enabled
        ? h("div", { class: "row" },
            field("Time servers", "text", (configuration.time.servers || []).join(", "), (value) => {
              configuration.time.servers = value.split(",").map((one) => one.trim()).filter(Boolean);
            }, "Separated by commas. Empty uses pool.ntp.org."))
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
        : null);
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
        choice("Log level", ["DEBUG", "INFO", "NOTICE", "WARNING", "ERROR", "CRITICAL"],
          (configuration.log.level || "NOTICE").toUpperCase(), (value) => { configuration.log.level = value; },
          "NOTICE is right for a screen. INFO includes everything the X server says, which is a great deal."),
        field("This interface listens on", "text", configuration.web.listen, (value) => { configuration.web.listen = value; }, "Changing this needs a restart of the container"),
        field("Stay signed in for", "number", secondsOf(configuration.web.sessionLifetime), (value) => {
          const seconds = Math.max(60, parseInt(value, 10) || 60);
          configuration.web.sessionLifetime = `${seconds}s`;
        }, "Seconds. Long, because signing in to a screen on a wall is a trip across the building")));
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
      h("h2", { text: "Restart something" }),
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
    const output = h("div", { class: "log" });
    let onlyProblems = false;
    let entries = null;

    const paint = () => {
      clear(output);
      if (!entries) return;

      const shown = onlyProblems
        ? entries.filter((one) => one.severity === "error" || one.severity === "warning")
        : entries;

      if (!shown.length) {
        output.append(h("div", { class: "dim", text: onlyProblems ? "Nothing the server called a warning or an error." : "The X server has not written a log yet." }));
        return;
      }

      for (const entry of shown) {
        output.append(h("div", { class: `log-line ${entry.severity || ""}` },
          h("span", { class: "log-time mono", text: timeOf(entry) }),
          entry.severity ? h("span", { class: `log-tag ${entry.severity}`, text: entry.severity }) : null,
          h("span", { class: "log-text", text: entry.text })));
      }
      output.scrollTop = output.scrollHeight;
    };

    // The server stamps its lines with the kernel's monotonic clock. The
    // daemon converts them, using the one line where the server prints a wall
    // clock beside one; where there is no such line — a tail that starts past
    // the header — the raw reading is shown rather than an invented time.
    const timeOf = (entry) => {
      if (entry.at) {
        const when = new Date(entry.at);
        return when.toLocaleTimeString(undefined, { hour12: false }) +
          "." + String(when.getMilliseconds()).padStart(3, "0");
      }
      if (entry.monotonic) return `+${entry.monotonic.toFixed(3)}`;
      return "";
    };

    const load = async () => {
      clear(output);
      output.append(h("div", { class: "dim", text: "Loading…" }));
      try {
        entries = await api.xorgLog();
        paint();
      } catch (error) {
        clear(output);
        output.append(h("div", { class: "notice bad", text: String(error.message || error) }));
      }
    };

    // Read straight away. This was behind a button when the log was one card
    // among ten on a page about everything; on a page whose only subject is the
    // log, asking somebody to press "read the log" after they have opened the
    // log is a step that does nothing but delay.
    load();

    return h("div", { class: "card" },
      h("h2", { text: "X server log" }),
      h("p", { class: "dim", text: "When a screen stays black, the reason is in here. The server's own timestamps are seconds since the machine booted; these are the real times." }),
      h("div", { class: "actions", style: "margin-bottom:0.75rem" },
        h("button", { onClick: load }, "Refresh"),
        h("button", {
          onClick: () => {
            onlyProblems = !onlyProblems;
            paint();
          },
        }, "Warnings and errors only"),
        h("a", { href: "/api/v1/logs/xorg?format=text", target: "_blank", class: "dim" }, "as the server wrote it")),
      output);
  }

  function textarea(value, onChange) {
    const element = h("textarea", {}, value || "");
    element.addEventListener("input", () => onChange(element.value));
    return element;
  }

  load();
  };
}

// The pages, and what each one carries. Split by what somebody is trying to do
// rather than by which part of the daemon owns the setting: "the screen looks
// wrong" is one errand, "the browser will not load our dashboard" is another,
// and they used to be sixteen hundred pixels apart on the same page.
export const device = settingsPage(["identity"]);
export const display = settingsPage(["connectors", "display"]);
export const browserPage = settingsPage(["browser"]);
export const health = settingsPage(["watchdog", "actions"]);
export const access = settingsPage(["input", "sound", "remote"]);
export const time = settingsPage(["clock"]);
export const logs = settingsPage(["log"]);
