// The interface. Four pages, a sign-in gate and a first-run wizard, in plain
// modules with no build step: see internal/web/static/novnc/README.md for why
// this project has no JavaScript toolchain.

import { h, clear } from "./dom.js";
import { api, whenSignedOut } from "./api.js";
import { overview } from "./pages/overview.js";
import { content } from "./pages/content.js";
import { screen } from "./pages/screen.js";
import { device } from "./pages/device.js";

const pages = [
  { path: "", title: "Overview", render: overview },
  { path: "content", title: "Content", render: content },
  { path: "screen", title: "Screen", render: screen },
  { path: "device", title: "Device", render: device },
];

const root = document.getElementById("app");

// stopCurrentPage is whatever the page on screen needs doing when it is left:
// stopping a timer, closing a VNC connection. A page that leaves one running
// keeps polling the daemon forever.
let stopCurrentPage = null;

let state = { needsSetup: false, signedIn: false, device: {}, version: "" };

whenSignedOut(() => {
  state.signedIn = false;
  render();
});

async function start() {
  try {
    state = { ...state, ...(await api.setupState()) };
  } catch (error) {
    root.className = "";
    clear(root);
    root.append(h("div", { class: "gate" },
      h("div", { class: "card" },
        h("h1", { text: "Cannot reach this device" }),
        h("p", { class: "lead", text: String(error.message || error) }),
        h("button", { class: "primary", onClick: () => location.reload() }, "Try again"))));
    return;
  }
  window.addEventListener("hashchange", render);
  render();
}

function render() {
  if (stopCurrentPage) {
    stopCurrentPage();
    stopCurrentPage = null;
  }
  root.className = "";
  clear(root);

  if (state.needsSetup) {
    root.append(wizard());
    return;
  }
  if (!state.signedIn) {
    root.append(signIn());
    return;
  }

  const path = location.hash.replace(/^#\/?/, "");
  const page = pages.find((candidate) => candidate.path === path) || pages[0];

  const main = h("main", { class: page.path === "screen" ? "wide" : "" });
  root.append(h("div", { class: "shell" }, chrome(page), main));

  stopCurrentPage = page.render(main) || null;
}

function chrome(active) {
  return h("header", { class: "bar" },
    h("div", { class: "brand" },
      h("strong", { text: state.device.name || "Cue" }),
      h("span", { class: "mono", text: state.device.identifier || "" })),
    h("nav", { class: "tabs" },
      pages.map((page) => h("a", {
        href: `#/${page.path}`,
        class: page === active ? "active" : "",
        text: page.title,
      }))),
    h("button", {
      onClick: async () => {
        await api.signOut();
        state.signedIn = false;
        render();
      },
    }, "Sign out"));
}

// --- the gate ---------------------------------------------------------------

function signIn() {
  const password = h("input", { type: "password", autofocus: true, autocomplete: "current-password" });
  const problem = h("div", { class: "notice bad", style: "display:none" });

  const submit = async (event) => {
    event.preventDefault();
    problem.style.display = "none";
    try {
      await api.signIn(password.value);
      state.signedIn = true;
      render();
    } catch (error) {
      problem.textContent = String(error.message || error);
      problem.style.display = "";
    }
  };

  return h("div", { class: "gate" },
    h("form", { class: "card", onSubmit: submit },
      h("h1", { text: state.device.name || "Cue" }),
      h("p", { class: "lead", text: "Sign in to see and change what this screen shows." }),
      problem,
      h("label", {}, h("span", { text: "Password" }), password),
      h("div", { class: "actions" }, h("button", { class: "primary", type: "submit" }, "Sign in"))));
}

// --- the first run ----------------------------------------------------------

function wizard() {
  const name = h("input", { type: "text", value: state.device.name || "", autofocus: true });
  const location_ = h("input", { type: "text", placeholder: "Reception, second floor" });
  const timezone = h("input", {
    type: "text",
    value: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
  });
  const password = h("input", { type: "password", autocomplete: "new-password" });
  const again = h("input", { type: "password", autocomplete: "new-password" });
  const problem = h("div", { class: "notice bad", style: "display:none" });

  const submit = async (event) => {
    event.preventDefault();
    problem.style.display = "none";

    if (password.value.length < 8) {
      problem.textContent = "The password must be at least eight characters.";
      problem.style.display = "";
      return;
    }
    if (password.value !== again.value) {
      problem.textContent = "The two passwords are not the same.";
      problem.style.display = "";
      return;
    }

    try {
      await api.setup({
        name: name.value,
        location: location_.value,
        timezone: timezone.value,
        password: password.value,
      });
      state.needsSetup = false;
      state.signedIn = true;
      state.device.name = name.value;
      location.hash = "#/content";
      render();
    } catch (error) {
      problem.textContent = String(error.message || error);
      problem.style.display = "";
    }
  };

  return h("div", { class: "gate" },
    h("form", { class: "card", onSubmit: submit },
      h("h1", { text: "Set up this screen" }),
      h("p", { class: "lead", text: "This takes a moment and happens once. The password protects everything on this device, including the live view of the screen." }),
      problem,
      h("label", {}, h("span", { text: "Name" }), name),
      h("label", {}, h("span", { text: "Where it is (optional)" }), location_),
      h("label", {}, h("span", { text: "Timezone" }), timezone),
      h("label", {}, h("span", { text: "Password" }), password),
      h("label", {}, h("span", { text: "Password again" }), again),
      h("div", { class: "actions" }, h("button", { class: "primary", type: "submit" }, "Finish"))));
}

start();
