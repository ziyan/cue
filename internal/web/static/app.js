// The interface. Four pages, a sign-in gate and a first-run wizard, in plain
// modules with no build step: see internal/web/static/novnc/README.md for why
// this project has no JavaScript toolchain.

import { h, clear, svg } from "./dom.js";
import { api, whenSignedOut } from "./api.js";
import { overview } from "./pages/overview.js";
import { content } from "./pages/content.js";
import { screen } from "./pages/screen.js";
import { device, display, browserPage, health, access, logs } from "./pages/device.js";
import { network } from "./pages/network.js";
import { upgrade } from "./pages/upgrade.js";

// The pages, in groups. Eleven entries in one undivided list is a wall; the
// groups say which errand each row belongs to, so somebody looking for the
// watchdog knows to look under the settings rather than reading all eleven.
const pages = [
  { path: "", title: "Overview", render: overview },
  { path: "content", title: "Content", render: content },
  { path: "screen", title: "Screen", render: screen },
  { path: "network", title: "Network", render: network },

  { group: "Settings" },
  { path: "device", title: "Device", render: device },
  { path: "display", title: "Display", render: display },
  { path: "browser", title: "Browser", render: browserPage },
  { path: "health", title: "Health", render: health },
  { path: "access", title: "Access", render: access },

  { group: "This machine" },
  { path: "logs", title: "Logs", render: logs },
  { path: "upgrade", title: "Upgrade", render: upgrade },
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
  applyTheme();
  // Remembered from last time, on anything wider than a phone.
  try {
    if (localStorage.getItem("cue.sidebar") === "collapsed" && window.innerWidth > 720) {
      document.body.classList.add("sidebar-collapsed");
    }
  } catch (error) { /* private window */ }

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
  window.addEventListener("device-renamed", (event) => {
    if (!event.detail || state.device.name === event.detail.name) return;
    state.device.name = event.detail.name;
    render();
  });
  render();
}

// nameTheDocument puts the device's name in the browser tab.
//
// Somebody looking after more than one of these has a tab open on each, and
// tabs are narrow: "Cue" on all of them tells them nothing, and the name is
// the only thing that distinguishes one screen from another. The page a tab is
// on comes second, because it changes and the device does not.
function nameTheDocument(page) {
  const name = state.device.name || "Cue";
  document.title = page && page.title && page.path !== "" ? `${name} — ${page.title}` : name;
}

function render() {
  if (stopCurrentPage) {
    stopCurrentPage();
    stopCurrentPage = null;
  }
  root.className = "";
  clear(root);
  nameTheDocument(null);

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
  // The sidebar covers the page on a phone, so pressing the page behind it
  // should put it away -- which is what everybody expects of a panel like it.
  main.addEventListener("click", closeSidebarOnAPhone);
  root.append(h("div", { class: "shell" }, chrome(page),
    h("div", { class: "sheet" }, main)));

  nameTheDocument(page);

  stopCurrentPage = page.render(main) || null;
}

// The icons. One line each, drawn with svg() so that nothing here is markup.
const icons = {
  "": () => svg("M3 10.5 12 3l9 7.5", "M5 9.5V21h14V9.5"),
  content: () => svg("M4 5h16v14H4z", "M4 9h16", "M10 13.5l4 2-4 2z"),
  screen: () => svg("M3 4h18v12H3z", "M8 20h8", "M12 16v4"),
  network: () => svg("M12 19h.01", "M5 12.5a9 9 0 0 1 14 0", "M8.5 15.5a5 5 0 0 1 7 0"),
  device: () => svg("M5 4h14v16H5z", "M9 8h6", "M9 12h6", "M9 16h3"),
  display: () => svg("M4 5h16v11H4z", "M9 20h6", "M12 16v4"),
  browser: () => svg("M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "M3.5 9h17", "M3.5 15h17",
    "M12 3c2.2 2.6 3.4 5.6 3.4 9s-1.2 6.4-3.4 9c-2.2-2.6-3.4-5.6-3.4-9S9.8 5.6 12 3z"),
  health: () => svg("M3 12h4l2-5 3 10 2.5-5H21"),
  access: () => svg("M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z",
    "M6.5 6.5a7.8 7.8 0 0 0 0 11", "M17.5 6.5a7.8 7.8 0 0 1 0 11"),
  logs: () => svg("M5 4h11l3 3v13H5z", "M9 11h6", "M9 15h6"),
  upgrade: () => svg("M12 20V6", "M6 12l6-6 6 6"),
  default: () => svg("M12 12h.01"),

  menu: () => svg("M4 7h16", "M4 12h16", "M4 17h16"),
  chevron: () => svg("M9 6l6 6-6 6"),
  tick: () => svg("M5 12.5l4.5 4.5L19 7.5"),
  user: () => svg("M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8z", "M4 20c1.5-3.5 4.5-5 8-5s6.5 1.5 8 5"),
  globe: () => svg("M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z", "M3 12h18",
    "M12 3c2.5 2.7 3.8 5.7 3.8 9s-1.3 6.3-3.8 9c-2.5-2.7-3.8-5.7-3.8-9S9.5 5.7 12 3z"),
  light: () => svg("M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10z", "M12 2v2", "M12 20v2",
    "M2 12h2", "M20 12h2", "M5 5l1.5 1.5", "M17.5 17.5L19 19", "M19 5l-1.5 1.5", "M6.5 17.5L5 19"),
  dark: () => svg("M20 14.5A8.5 8.5 0 1 1 9.5 4a7 7 0 0 0 10.5 10.5z"),
  system: () => svg("M3 5h18v11H3z", "M8 20h8", "M12 5v11"),
};

// The shell: a sidebar of pages, and a bar across the top carrying where you
// are on the left and the three things about you on the right.
//
// It was a row of tabs. A row does not have room: at six pages it was 167
// pixels wider than a phone, so Device and Upgrade sat off the right-hand edge
// with nothing to say they were there. render() used to scroll the current tab
// into view to make up for it, which treated the symptom. A column has room
// for as many pages as this program grows to.
function chrome(active) {
  return [sidebar(active), topBar(active)];
}

function sidebar(active) {
  return h("aside", { class: "sidebar", id: "sidebar" },
    h("div", { class: "brand" },
      h("img", { class: "mark", src: "/favicon.svg", alt: "" }),
      h("span", { class: "name", text: state.device.name || "Cue" })),
    h("nav", {},
      pages.map((page) => page.group
        // A rule and a small heading, the way the portal does it. The rule
        // stays when the sidebar is collapsed to its icons and the heading
        // goes: a heading in a 60 pixel column is a smear.
        ? h("div", { class: "group" }, h("span", { class: "group-name", text: page.group }))
        : h("a", {
            href: `#/${page.path}`,
            class: page === active ? "active" : "",
            title: page.title,
            onClick: closeSidebarOnAPhone,
          },
            h("span", { class: "icon" }, (icons[page.path] || icons.default)()),
            h("span", { class: "label", text: page.title })))));

}

function topBar(active) {
  return h("header", { class: "bar" },
    h("button", {
      class: "icon-button",
      "aria-label": "Menu",
      onClick: toggleSidebar,
    }, h("span", { class: "icon" }, icons.menu())),

    // Where you are. The tab row was doing this badly: the tab you were on was
    // often the one off the end of it.
    h("nav", { class: "breadcrumb", "aria-label": "Breadcrumb" },
      h("span", { class: "here", text: state.device.name || "Cue" }),
      h("span", { class: "sep" }, icons.chevron()),
      h("span", { class: "page", text: active ? active.title : "" })),

    h("div", { class: "bar-right" },
      languageButton(),
      themeButton(),
      userMenu()));
}

// --- the three on the right -------------------------------------------------

function themeButton() {
  // A menu with the three named, rather than a button that cycles through
  // them. A cycling button never says what the next press will do, and
  // "follow the system" is not a state anybody guesses is in there.
  const choices = [
    { key: "light", name: "Light" },
    { key: "dark", name: "Dark" },
    { key: "system", name: "Same as this device" },
  ];

  const menu = h("div", { class: "menu" },
    choices.map((choice) => h("button", {
      class: "menu-item" + (currentTheme() === choice.key ? " on" : ""),
      onClick: () => {
        menu.hidden = true;
        setTheme(choice.key);
        render();
      },
    },
      h("span", { class: "icon" }, icons[choice.key]()),
      h("span", { class: "menu-label", text: choice.name }),
      currentTheme() === choice.key ? h("span", { class: "icon tick" }, icons.tick()) : null)));
  menu.hidden = true;

  const button = h("button", {
    class: "icon-button",
    "aria-label": "Light or dark",
    "aria-haspopup": "true",
    title: "Light or dark",
    onClick: (event) => {
      event.stopPropagation();
      menu.hidden = !menu.hidden;
    },
  }, h("span", { class: "icon" }, (icons[currentTheme()] || icons.system)()));

  document.addEventListener("click", () => { menu.hidden = true; });
  return h("div", { class: "menu-holder" }, button, menu);
}

function userMenu() {
  const menu = h("div", { class: "menu" },
    h("div", { class: "menu-head" },
      h("div", { class: "menu-name", text: state.device.name || "Cue" }),
      h("div", { class: "menu-dim mono", text: state.device.identifier || "" })),
    h("button", {
      class: "menu-item",
      onClick: async () => {
        await api.signOut();
        state.signedIn = false;
        render();
      },
    }, "Sign out"));
  menu.hidden = true;

  const button = h("button", {
    class: "icon-button",
    "aria-label": "Account",
    "aria-haspopup": "true",
    onClick: (event) => {
      event.stopPropagation();
      menu.hidden = !menu.hidden;
    },
  }, h("span", { class: "icon" }, icons.user()));

  // Anywhere else closes it, which is what everybody expects of a menu.
  document.addEventListener("click", () => { menu.hidden = true; });

  return h("div", { class: "menu-holder" }, button, menu);
}

// --- the sidebar on a phone -------------------------------------------------

function toggleSidebar(event) {
  if (event) event.stopPropagation();
  document.body.classList.toggle("sidebar-open");
  // On anything wider than a phone the button collapses the sidebar to its
  // icons instead of hiding it, and that choice is worth remembering.
  if (window.innerWidth > 720) {
    const collapsed = document.body.classList.toggle("sidebar-collapsed");
    document.body.classList.remove("sidebar-open");
    try { localStorage.setItem("cue.sidebar", collapsed ? "collapsed" : "open"); } catch (error) { /* private window */ }
  }
}

function closeSidebarOnAPhone() {
  document.body.classList.remove("sidebar-open");
}

// --- light and dark ---------------------------------------------------------
//
// Three states, not two. A toggle can only say light or dark, and a screen set
// up in a room that darkens in the evening should be able to say "whatever the
// machine says" -- which is what it did before there was a control at all, and
// must go on being able to do.
//
// Remembered in the browser it was set in rather than on the device: this is
// about the person looking, and two people looking after the same screen from
// different laptops need not agree.

function currentTheme() {
  try {
    const chosen = localStorage.getItem("cue.theme");
    if (chosen === "light" || chosen === "dark") return chosen;
  } catch (error) {
    // A private window refuses storage. Follow the system, as before.
  }
  return "system";
}

function setTheme(theme) {
  try {
    if (theme === "system") localStorage.removeItem("cue.theme");
    else localStorage.setItem("cue.theme", theme);
  } catch (error) { /* nothing to remember it in */ }
  applyTheme();
}

// applyTheme puts the choice where the stylesheet can see it. Exported through
// the module's own start, and called again whenever it changes.
export function applyTheme() {
  const theme = currentTheme();
  if (theme === "system") document.documentElement.removeAttribute("data-theme");
  else document.documentElement.setAttribute("data-theme", theme);
}


// --- language ---------------------------------------------------------------

// The language this screen speaks -- the one it shows on its own display, in
// the menu somebody standing at it opens. This interface is written in English
// only, so the control says what it changes rather than pretending to change
// this page.
function languageButton() {
  const languages = [
    { tag: "en", name: "English" },
    { tag: "zh", name: "\u4e2d\u6587" },
    { tag: "ja", name: "\u65e5\u672c\u8a9e" },
  ];

  const menu = h("div", { class: "menu" },
    h("div", { class: "menu-head" },
      h("div", { class: "menu-name", text: "Screen language" }),
      h("div", { class: "menu-dim", text: "What the menu on the screen itself speaks" })),
    languages.map((language) => h("button", {
      class: "menu-item" + (state.device.language === language.tag ? " on" : ""),
      onClick: async () => {
        menu.hidden = true;
        try {
          const configuration = await api.configuration();
          configuration.device.language = language.tag;
          await api.saveConfiguration(configuration);
          state.device.language = language.tag;
        } catch (error) {
          // Saying nothing here would be worse than saying it plainly.
          window.alert(String(error.message || error));
        }
        render();
      },
    },
      h("span", { class: "menu-label", text: language.name }),
      state.device.language === language.tag ? h("span", { class: "icon tick" }, icons.tick()) : null)));
  menu.hidden = true;

  const button = h("button", {
    class: "icon-button",
    "aria-label": "Screen language",
    "aria-haspopup": "true",
    onClick: (event) => {
      event.stopPropagation();
      menu.hidden = !menu.hidden;
    },
  }, h("span", { class: "icon" }, icons.globe()));

  document.addEventListener("click", () => { menu.hidden = true; });
  return h("div", { class: "menu-holder" }, button, menu);
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
