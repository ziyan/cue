// Attaching this device to an account on the hosted service.
//
// The page has three states and shows exactly one of them: not linked and
// offering to start, showing a code and waiting, or linked and saying to what.
// A person who arrives at it should be able to tell which without reading.
//
// While a code is up the page polls, because the thing it is waiting for
// happens on somebody's phone and there is nothing here that would otherwise
// notice.

import { h, clear } from "../dom.js";
import { api } from "../api.js";
import { field } from "./content.js";

// pollInterval is how often the page asks whether the link has completed.
// Somebody is watching it, so it is short; the daemon is asking the service on
// its own schedule regardless, so this only decides how quickly the screen
// catches up.
const pollInterval = 1500;

export function service(main) {
  const body = h("div");
  main.append(body);

  let configuration = null;
  let state = null;
  let timer = null;

  // Returned to the shell, which calls it when this page is left. A page that
  // leaves a timer running keeps asking about a device nobody is looking at.
  const stopPolling = () => {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  };

  const poll = async () => {
    try {
      const latest = await api.link();
      // Only redraw when something changed. A page that rebuilds itself every
      // second and a half loses the cursor out of a text field.
      if (JSON.stringify(latest) === JSON.stringify(state)) return;
      state = latest;
      if (!state.pending) stopPolling();
      draw();
    } catch (error) {
      stopPolling();
      draw(null, String(error.message || error));
    }
  };

  const startPolling = () => {
    stopPolling();
    timer = setInterval(poll, pollInterval);
  };

  const load = async () => {
    clear(body);
    body.append(h("p", { class: "dim", text: "Loading…" }));
    try {
      [configuration, state] = await Promise.all([api.configuration(), api.link()]);
      draw();
      if (state.pending) startPolling();
    } catch (error) {
      clear(body);
      body.append(h("div", { class: "notice bad", text: String(error.message || error) }));
    }
  };

  const saveAddress = async () => {
    try {
      configuration = await api.saveConfiguration(configuration);
      state = await api.link();
      draw("Saved.");
    } catch (error) {
      draw(null, String(error.message || error));
    }
  };

  const start = async () => {
    try {
      state = await api.startLink();
      draw();
      startPolling();
    } catch (error) {
      draw(null, String(error.message || error));
    }
  };

  const abandon = async () => {
    stopPolling();
    try {
      state = await api.abandonLink();
      draw();
    } catch (error) {
      draw(null, String(error.message || error));
    }
  };

  const forget = async () => {
    stopPolling();
    try {
      state = await api.forgetLink();
      draw("This device is no longer linked.");
    } catch (error) {
      draw(null, String(error.message || error));
    }
  };

  function draw(good, bad) {
    clear(body);
    if (good) body.append(h("div", { class: "notice good", text: good }));
    if (bad) body.append(h("div", { class: "notice bad", text: bad }));
    if (state && state.error) body.append(h("div", { class: "notice bad", text: state.error }));

    body.append(h("section", {},
      h("h2", { text: "Service" }),
      h("p", {
        class: "dim",
        text: "Where this device reports to. Leave it empty and the device works " +
          "entirely on its own, which is the normal state.",
      }),
      field("Address", "url", configuration.service.address,
        (value) => { configuration.service.address = value; },
        "For example https://example.com"),
      h("div", { class: "actions" },
        h("button", { text: "Save", onclick: saveAddress }))));

    if (state.linked) {
      body.append(drawLinked());
      return;
    }
    if (state.pending) {
      body.append(drawPending());
      return;
    }
    body.append(drawUnlinked());
  }

  function drawLinked() {
    // The identifier the service gave this device, shown because it is what
    // somebody looking at the two systems side by side matches them up on.
    const deviceId = configuration.service.deviceId;
    return h("section", {},
      h("h2", { text: "Linked" }),
      h("p", { text: `This device is attached to ${state.account || "an account"}.` }),
      deviceId ? h("p", { class: "dim" },
        h("span", { text: "Known there as " }),
        h("span", { class: "mono", text: deviceId })) : null,
      h("p", {
        class: "dim",
        text: "Unlinking forgets the credential and nothing else. The device keeps " +
          "its name, its screens and everything it is showing.",
      }),
      h("div", { class: "actions" },
        h("button", { class: "bad", text: "Unlink", onclick: forget })));
  }

  function drawPending() {
    // A cache-buster, because the code changes when an attempt does and the
    // browser has no way to know that from the address alone.
    const source = `/api/v1/link/code.svg?at=${encodeURIComponent(state.expiresAt || "")}`;
    return h("section", {},
      h("h2", { text: "Scan to link" }),
      h("p", { text: "Open this on a phone, sign in, and authorise the device." }),
      h("img", { src: source, alt: "Linking code", class: "code" }),
      h("p", {},
        h("a", { href: state.url, target: "_blank", rel: "noreferrer", text: state.url })),
      h("p", { class: "dim", text: "This page will say so as soon as it is authorised." }),
      h("div", { class: "actions" },
        h("button", { text: "Cancel", onclick: abandon })));
  }

  function drawUnlinked() {
    const configured = Boolean((configuration.service.address || "").trim());
    return h("section", {},
      h("h2", { text: "Not linked" }),
      h("p", {
        text: configured
          ? "Linking this device attaches it to an account, so it can be watched from there."
          : "Set an address above before linking.",
      }),
      h("div", { class: "actions" },
        h("button", { text: "Link this device", disabled: !configured, onclick: start })));
  }

  load();
  return stopPolling;
}
