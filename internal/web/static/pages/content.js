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
        }, "0 shows the first page and never moves on"),
        field("Largest video, in MB", "number",
          Math.round((configuration.playlist.maximumVideoSize || 0) / (1024 * 1024)), (value) => {
            const megabytes = Math.max(1, parseInt(value, 10) || 0);
            configuration.playlist.maximumVideoSize = megabytes * 1024 * 1024;
          }, "Uploads larger than this are refused, so one file cannot fill the disk")),
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
        }, "Add a page"),
        h("button", { onClick: () => chooseVideo(items) }, "Add a video"),
        uploading)));

    body.append(h("div", { class: "actions" },
      h("button", { class: "primary", onClick: save }, "Save"),
      h("button", { onClick: load }, "Discard changes")));
  }

  // uploading is the progress bar, kept between draws so that redrawing the
  // page while a video is on its way does not lose it.
  const uploading = h("div", { class: "uploading" });

  // chooseVideo asks for a file and sends it to the device.
  //
  // XMLHttpRequest rather than fetch, because fetch cannot report how far an
  // upload has got. A sixty megabyte video over wireless takes long enough
  // that somebody watching a button do nothing concludes it has hung, and the
  // next thing they do is press it again.
  function chooseVideo(items) {
    const chooser = h("input", { type: "file", accept: "video/*" });
    chooser.addEventListener("change", () => {
      const file = chooser.files && chooser.files[0];
      if (!file) return;
      uploadVideo(file, items);
    });
    chooser.click();
  }

  function uploadVideo(file, items) {
    clear(uploading);
    const bar = h("div", { class: "bar" });
    const label = h("span", { class: "dim", text: `Sending ${file.name}…` });
    uploading.append(h("div", { class: "progress" }, bar), label);

    const body = new FormData();
    body.append("file", file);

    const request = new XMLHttpRequest();
    request.open("POST", "/api/v1/videos");
    request.upload.addEventListener("progress", (event) => {
      if (!event.lengthComputable) return;
      const done = Math.round((event.loaded / event.total) * 100);
      bar.style.width = `${done}%`;
      label.textContent = `Sending ${file.name}… ${done}%`;
    });
    request.addEventListener("load", () => {
      if (request.status !== 200) {
        let reason = request.statusText;
        try {
          reason = JSON.parse(request.responseText).error || reason;
        } catch (error) {
          // The body was not JSON, so the status is all there is to say.
        }
        clear(uploading);
        uploading.append(h("p", { class: "bad", text: `Could not send that video: ${reason}` }));
        return;
      }
      const stored = JSON.parse(request.responseText);
      items.push({
        title: "",
        disabled: false,
        video: { file: stored.file, name: stored.name, sound: false },
      });
      configuration.playlist.items = items;
      clear(uploading);
      uploading.append(h("p", { class: "dim", text: `${stored.name} is on this device. Save to start showing it.` }));
      draw();
    });
    request.addEventListener("error", () => {
      clear(uploading);
      uploading.append(h("p", { class: "bad", text: "The upload did not reach the device." }));
    });
    request.send(body);
  }

  function itemCard(item, index, items) {
    const move = (offset) => {
      const target = index + offset;
      if (target < 0 || target >= items.length) return;
      [items[index], items[target]] = [items[target], items[index]];
      draw();
    };

    const video = item.video || null;

    return h("div", { class: "item" },
      h("header", {},
        h("span", { class: "handle", text: `${index + 1}.` }),
        h("span", { class: "title truncate",
          text: item.title || (video ? video.name : item.url) || "New page" }),
        video ? h("span", { class: "pill", text: "Video" }) : null,
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

      video
        ? h("div", {},
            h("div", { class: "row" },
              field("Name (optional)", "text", item.title, (value) => { item.title = value; },
                `The file is ${video.name}`)),
            h("div", { class: "row" },
              h("div", {},
                checkbox("Play this video with its sound", video.sound, (value) => { video.sound = value; }),
                checkbox("Skip this video for now", item.disabled, (value) => { item.disabled = value; }))),
            h("p", { class: "dim", text: "It plays full screen and the screen moves on the moment it ends, so it needs no time on screen setting. Sound also needs this device's own sound to be switched on, on the Device page." }))
        : h("div", {},
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
            dismissSection(item)));
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

// searchable is a dropdown for a list too long to scroll — the timezones, of
// which this machine has four hundred and eighty-two.
//
// It was an <input list> and a <datalist> first, which is three lines and
// which nobody can tell is a list: the control looks exactly like a text box
// until you happen to type something that matches. A setting offered from a
// list has to look like one, or it is a text box with a secret.
//
// So this is the control itself: a field, a button that says it opens, and a
// panel that filters as you type. Written out rather than pulled in, because
// the alternative was a component library, and a component library means
// React and a bundler in a project that has neither and whose whole interface
// is four pages of forms.
export function searchable(label, options, value, onChange, hint) {
  const input = h("input", {
    type: "text",
    value: value || "",
    autocomplete: "off",
    autocapitalize: "off",
    spellcheck: "false",
    role: "combobox",
    "aria-expanded": "false",
    "aria-autocomplete": "list",
  });
  const list = h("div", { class: "options", role: "listbox" });
  const panel = h("div", { class: "searchable" }, input, list);

  let open = false;
  let highlighted = -1;
  let shown = [];

  const matches = (text) => {
    const needle = text.trim().toLowerCase();
    if (!needle) return options.slice(0, 200);
    // Anything containing every word typed, so "new york" finds
    // America/New_York and "lon" finds Europe/London.
    const words = needle.split(/\s+/);
    return options.filter((option) => {
      const haystack = option.toLowerCase().replace(/_/g, " ");
      return words.every((word) => haystack.includes(word));
    }).slice(0, 200);
  };

  const choose = (option) => {
    input.value = option;
    onChange(option);
    close();
  };

  const close = () => {
    open = false;
    highlighted = -1;
    panel.classList.remove("open");
    input.setAttribute("aria-expanded", "false");
  };

  const paint = () => {
    clear(list);
    shown = matches(input.value);
    if (!shown.length) {
      list.append(h("div", { class: "option empty", text: "Nothing matches" }));
      return;
    }
    shown.forEach((option, index) => {
      const row = h("div", {
        class: `option${index === highlighted ? " highlighted" : ""}`,
        role: "option",
        // mousedown rather than click: clicking moves focus out of the input
        // first, and the blur handler would close the panel before the click
        // ever landed.
        onMousedown: (event) => {
          event.preventDefault();
          choose(option);
        },
      }, option);
      list.append(row);
    });
  };

  const show = () => {
    open = true;
    panel.classList.add("open");
    input.setAttribute("aria-expanded", "true");
    paint();
  };

  input.addEventListener("focus", show);
  input.addEventListener("input", () => {
    highlighted = -1;
    if (!open) show(); else paint();
    // What is typed is kept even before anything is picked, so a zone this
    // build does not list can still be set by hand.
    onChange(input.value.trim());
  });
  input.addEventListener("blur", () => setTimeout(close, 0));
  input.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!open) return show();
      highlighted += event.key === "ArrowDown" ? 1 : -1;
      if (highlighted < 0) highlighted = shown.length - 1;
      if (highlighted >= shown.length) highlighted = 0;
      paint();
      const row = list.children[highlighted];
      if (row && row.scrollIntoView) row.scrollIntoView({ block: "nearest" });
    } else if (event.key === "Enter") {
      if (open && highlighted >= 0 && shown[highlighted]) {
        event.preventDefault();
        choose(shown[highlighted]);
      }
    } else if (event.key === "Escape") {
      close();
    }
  });

  return h("label", { class: "searchable-field" },
    h("span", { text: label }),
    panel,
    hint ? h("span", { class: "dim", style: "margin-top:0.25rem", text: hint }) : null);
}
