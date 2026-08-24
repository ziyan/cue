// What the screen shows: the playlist, and the two rules that keep a page
// usable without anybody standing in front of it.

import { h, clear } from "../dom.js";
import { api } from "../api.js";

export function content(main) {
  const body = h("div");
  main.append(body);

  let configuration = null;

  const load = async () => {
    clear(body);
    body.append(h("p", { class: "dim", text: "Loading…" }));
    try {
      configuration = await api.configuration();
      draw();
    } catch (error) {
      clear(body);
      body.append(h("div", { class: "notice bad", text: String(error.message || error) }));
    }
  };

  const save = async () => {
    try {
      configuration = await api.saveConfiguration(configuration);
      draw("Saved. The screen is already following the new playlist.");
    } catch (error) {
      draw(null, String(error.message || error));
    }
  };

  function draw(good, bad) {
    clear(body);

    if (good) body.append(h("div", { class: "notice good", text: good }));
    if (bad) body.append(h("div", { class: "notice bad", text: bad }));

    body.append(h("div", { class: "card" },
      h("h2", { text: "Rotation" }),
      h("div", { class: "row" },
        field("Seconds on each page", "number", secondsOf(configuration.playlist.interval), (value) => {
          configuration.playlist.interval = `${Math.max(0, parseInt(value, 10) || 0)}s`;
        }, "0 shows the first page and never moves on")),
    ));

    const items = configuration.playlist.items || [];
    body.append(h("div", { class: "card" },
      h("h2", { text: `Pages (${items.length})` }),
      items.length ? items.map((item, index) => itemCard(item, index, items)) : h("p", { class: "dim", text: "Nothing yet. Add a page and this screen will start showing it." }),
      h("div", { class: "actions" },
        h("button", {
          onClick: () => {
            items.push({ url: "", title: "", reload: false, disabled: false });
            configuration.playlist.items = items;
            draw();
          },
        }, "Add a page"))));

    body.append(h("div", { class: "actions" },
      h("button", { class: "primary", onClick: save }, "Save"),
      h("button", { onClick: load }, "Discard changes")));
  }

  function itemCard(item, index, items) {
    const move = (offset) => {
      const target = index + offset;
      if (target < 0 || target >= items.length) return;
      [items[index], items[target]] = [items[target], items[index]];
      draw();
    };

    return h("div", { class: "item" },
      h("header", {},
        h("span", { class: "handle", text: `${index + 1}.` }),
        h("span", { class: "title truncate", text: item.title || item.url || "New page" }),
        item.identifier
          ? h("button", { onClick: () => api.show(item.identifier).catch(() => {}) }, "Show now")
          : null,
        h("button", { onClick: () => move(-1), disabled: index === 0 }, "↑"),
        h("button", { onClick: () => move(1), disabled: index === items.length - 1 }, "↓"),
        h("button", {
          class: "danger",
          onClick: () => {
            items.splice(index, 1);
            draw();
          },
        }, "Remove")),

      h("div", { class: "row" },
        field("Address", "url", item.url, (value) => { item.url = value; }),
        field("Name (optional)", "text", item.title, (value) => { item.title = value; })),

      h("div", { class: "row" },
        field("Seconds on screen", "number", secondsOf(item.duration), (value) => {
          const seconds = Math.max(0, parseInt(value, 10) || 0);
          item.duration = seconds ? `${seconds}s` : "0s";
        }, "Empty uses the rotation setting above"),
        h("div", {},
          checkbox("Reload each time it comes round", item.reload, (value) => { item.reload = value; }),
          checkbox("Skip this page for now", item.disabled, (value) => { item.disabled = value; }))),

      loginSection(item),
      dismissSection(item));
  }

  function loginSection(item) {
    const enabled = !!item.login;

    const fields = enabled ? h("div", {},
      h("p", { class: "dim", text: "Checked every few seconds, not just when the page opens — which is the point. A dashboard that expires its session drops back to a login form hours later, and this puts it back." }),
      h("div", { class: "row" },
        field("Recognise the login page by address matching", "text", item.login.whenUrlMatches, (value) => { item.login.whenUrlMatches = value; }, "A regular expression, for example /login"),
        field("…or by this element existing", "text", item.login.whenSelectorExists, (value) => { item.login.whenSelectorExists = value; }, "A CSS selector only the login page has")),
      h("div", { class: "row" },
        field("Username field", "text", item.login.usernameSelector, (value) => { item.login.usernameSelector = value; }, "CSS selector"),
        field("Password field", "text", item.login.passwordSelector, (value) => { item.login.passwordSelector = value; }, "CSS selector"),
        field("Button to click", "text", item.login.submitSelector, (value) => { item.login.submitSelector = value; }, "Empty presses Enter instead")),
      h("div", { class: "row" },
        field("Username", "text", item.login.username, (value) => { item.login.username = value; }),
        field("Password", "password", item.login.password, (value) => { item.login.password = value; }, "Never shown again once saved")),
      h("div", { class: "row" },
        field("Also click these first", "text", (item.login.alsoClick || []).join(", "), (value) => {
          item.login.alsoClick = value.split(",").map((one) => one.trim()).filter(Boolean);
        }, "CSS selectors, clicked after the fields are filled. A “remember me” box here is the difference between signing in every few hours and every few weeks")),
      h("div", { class: "row" },
        field("Signed in when the address matches", "text", item.login.expectUrlMatches, (value) => { item.login.expectUrlMatches = value; }, "Optional, but it is how a wrong password gets reported"),
        field("Wait at least this long between attempts", "number", secondsOf(item.login.minimumInterval), (value) => {
          const seconds = Math.max(0, parseInt(value, 10) || 0);
          item.login.minimumInterval = seconds ? `${seconds}s` : "0s";
        }, "Stops a wrong password locking the account out"))) : null;

    return h("details", { open: enabled },
      h("summary", { text: enabled ? "Signing in — on" : "Signing in — off" }),
      h("div", {},
        checkbox("Keep this page signed in", enabled, (value) => {
          item.login = value ? {
            whenUrlMatches: "/login",
            whenSelectorExists: "",
            usernameSelector: "input[name=username]",
            passwordSelector: "input[name=password]",
            submitSelector: "",
            alsoClick: [],
            username: "",
            password: "",
            expectUrlMatches: "",
            minimumInterval: "60s",
          } : null;
          draw();
        }),
        fields));
  }

  function dismissSection(item) {
    const rules = item.dismiss || [];

    return h("details", { open: rules.length > 0 },
      h("summary", { text: rules.length ? `Getting rid of things — ${rules.length} rule${rules.length === 1 ? "" : "s"}` : "Getting rid of things — none" }),
      h("div", {},
        h("p", { class: "dim", text: "Cookie banners, “what’s new” announcements, survey invitations: anything that appears on top of the page and stays there. On a screen nobody touches, one of these covers the dashboard for weeks." }),
        rules.map((rule, index) => h("div", { class: "row" },
          field("Element", "text", rule.selector, (value) => { rule.selector = value; }, "CSS selector for the thing to click"),
          field("Only if its text matches", "text", rule.whenTextMatches, (value) => { rule.whenTextMatches = value; }, "Optional regular expression"),
          h("div", {},
            checkbox("Hide it instead of clicking", rule.hide, (value) => { rule.hide = value; }),
            h("button", {
              class: "danger",
              onClick: () => {
                rules.splice(index, 1);
                draw();
              },
            }, "Remove")))),
        h("div", { class: "actions" },
          h("button", {
            onClick: () => {
              item.dismiss = rules;
              rules.push({ selector: "", whenTextMatches: "", hide: false });
              draw();
            },
          }, "Add a rule"))));
  }

  load();
}

// --- small shared controls ---------------------------------------------------

export function field(label, type, value, onChange, hint) {
  const input = h("input", { type, value: value === undefined || value === null ? "" : value });
  input.addEventListener("input", () => onChange(input.value));
  return h("label", {},
    h("span", { text: label }),
    input,
    hint ? h("span", { class: "dim", style: "margin-top:0.25rem", text: hint }) : null);
}

export function checkbox(label, value, onChange) {
  const input = h("input", { type: "checkbox", checked: value });
  input.addEventListener("change", () => onChange(input.checked));
  return h("label", { class: "inline" }, input, h("span", { text: label }));
}

// secondsOf turns "30s", "1m30s" or "0s" into a number of seconds for a form
// field. The daemon writes durations as text because that is what reads
// sensibly in the configuration file.
export function secondsOf(value) {
  if (!value) return "";
  const match = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?)s)?$/.exec(String(value));
  if (!match) return "";
  const total = (parseInt(match[1] || 0, 10) * 3600) + (parseInt(match[2] || 0, 10) * 60) + Math.round(parseFloat(match[3] || 0));
  return total === 0 ? "" : String(total);
}

// choice is a plain dropdown for a setting with a known, short list of
// answers: a rotation, a mode, a log level.
//
// Typing a value into a box is how a setting gets a typo in it, and a typo in
// a rotation is a screen that comes up sideways or an X server that will not
// start. Where the answers are known, they are offered.
export function choice(label, options, value, onChange, hint) {
  const element = h("select", {});
  const values = options.map((option) => (typeof option === "string" ? option : option.value));

  for (const option of options) {
    const optionValue = typeof option === "string" ? option : option.value;
    const optionLabel = typeof option === "string" ? option : option.label;
    element.append(h("option", { value: optionValue, selected: optionValue === value }, optionLabel));
  }

  // A value the device already has but this list does not — a mode a monitor
  // stopped offering, a timezone from a newer database — is added rather than
  // silently replaced by whatever happens to be first.
  if (value && !values.includes(value)) {
    element.prepend(h("option", { value, selected: true }, `${value} (not offered now)`));
  }

  element.addEventListener("change", () => onChange(element.value));
  return h("label", {},
    h("span", { text: label }),
    element,
    hint ? h("span", { class: "dim", style: "margin-top:0.25rem", text: hint }) : null);
}

// searchable is a dropdown for a list too long to scroll: the timezones, of
// which there are hundreds.
//
// It is an input with a datalist rather than anything clever, so it filters as
// you type, still accepts a value that is not on the list, and needs no
// keyboard handling of its own — which matters, because this interface is
// sometimes driven by a finger on the screen it is configuring.
export function searchable(label, options, value, onChange, hint) {
  const listName = "list-" + label.toLowerCase().replace(/[^a-z0-9]+/g, "-");
  const input = h("input", { type: "text", value: value || "", list: listName, autocomplete: "off" });
  const list = h("datalist", { id: listName });
  for (const option of options) {
    list.append(h("option", { value: option }));
  }
  input.addEventListener("change", () => onChange(input.value.trim()));
  input.addEventListener("input", () => onChange(input.value.trim()));

  return h("label", {},
    h("span", { text: label }),
    input,
    list,
    hint ? h("span", { class: "dim", style: "margin-top:0.25rem", text: hint }) : null);
}
