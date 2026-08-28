// Whether there is a newer cue than the one running here, and what changed in
// it.
//
// The notes are rendered from Markdown by hand, and only the three shapes the
// release notes actually use: a heading, a list item, and a paragraph.
// Everything goes in through textContent, never innerHTML — this text arrives
// from the network, and a release page is not a reason to hand somebody else's
// bytes to the parser.

import { h, clear } from "../dom.js";
import { api } from "../api.js";

export function upgrade(main) {
  const body = h("div");
  main.append(body);

  // While an upgrade is running the page asks again every few seconds, so that
  // the stage moves and the button does not come back. Cleared when the page
  // is left, or it goes on polling a daemon nobody is looking at.
  let watching = null;
  const stopWatching = () => {
    if (watching) clearInterval(watching);
    watching = null;
  };

  const load = async () => {
    clear(body);
    body.append(h("p", { class: "dim", text: "Looking…" }));
    try {
      draw(await api.upgrade());
    } catch (error) {
      clear(body);
      body.append(h("div", { class: "notice bad", text: String(error.message || error) }));
    }
  };

  const draw = (state) => {
    clear(body);

    const progress = state.progress || {};

    // An upgrade in progress is the whole page. Nothing else on it can be
    // acted on while the device is replacing itself, and showing the button
    // again is how somebody presses it twice.
    if (progress.running) {
      if (!watching) watching = setInterval(quietly, 3000);
      body.append(h("div", { class: "card" },
        h("h2", { text: `Updating to ${progress.version || state.latest}` }),
        h("p", { class: "lead", text: progress.stage || "Working" }),
        h("p", { class: "dim", text: progress.startedAt ? `Started ${when(progress.startedAt)}` : "" }),
        h("div", { class: "bar" }, h("i")),
        h("p", { class: "dim", text: "The screen goes blank and comes back on its own. This page stops answering while the daemon restarts — it will come back too." })));
      return;
    }

    stopWatching();

    // A failed attempt is worth saying on the way in, rather than showing an
    // interface that looks as though nothing was ever tried.
    if (progress.trouble) {
      body.append(h("div", { class: "card" },
        h("h2", { text: "The last update did not finish" }),
        h("div", { class: "notice bad", text: progress.trouble }),
        h("p", { class: "dim", text: "This device is still running what it was running before." })));
    }

    body.append(h("div", { class: "card" },
      h("h2", { text: "This device" }),
      h("p", { class: "lead", text: `Running ${state.running}` }),
      state.checkedAt
        ? h("p", { class: "dim", text: `Last checked ${when(state.checkedAt)}` })
        : h("p", { class: "dim", text: "Not checked yet" }),
      // A failed check is shown rather than swallowed. A page that quietly
      // shows nothing looks exactly like a page saying you are up to date.
      state.trouble
        ? h("p", { class: "notice", text: `The last check did not work: ${state.trouble}` })
        : null));

    if (!state.latest) {
      body.append(h("div", { class: "card" },
        h("p", { class: "lead", text: "Nothing is known about newer releases yet." })));
      return;
    }

    if (!state.newer) {
      body.append(h("div", { class: "card" },
        h("h2", { text: "Up to date" }),
        h("p", { class: "lead", text: `${state.latest} is the newest release.` }),
        state.url ? link(state.url) : null));
      return;
    }

    body.append(h("div", { class: "card" },
      h("h2", { text: `${state.latest} is available` }),
      h("p", { class: "lead", text: `This device is running ${state.running}.` }),
      state.publishedAt
        ? h("p", { class: "dim", text: `Released ${when(state.publishedAt)}` })
        : null,
      state.notes ? h("div", { class: "notes" }, ...notes(state.notes)) : null,
      state.url ? link(state.url) : null));

    body.append(state.canApply ? theButton(state) : byHand(state));
  };

  // The button, on a device set up to allow it. Asking first, because this
  // takes the screen away for about a minute and the person pressing it may
  // not be the person standing in front of it.
  const theButton = (state) => {
    const card = h("div", { class: "card" });

    const start = async () => {
      clear(card);
      card.append(
        h("h2", { text: "Updating" }),
        h("p", { class: "lead", text: `Fetching ${state.image}. This takes a few minutes.` }),
        h("p", { class: "dim", text: "The screen will go blank and come back on its own. This page will stop answering while the daemon restarts; reload it in a minute." }));
      try {
        await api.applyUpgrade();
      } catch (error) {
        clear(card);
        card.append(
          h("h2", { text: "The update could not start" }),
          h("div", { class: "notice bad", text: String(error.message || error) }),
          h("p", { class: "dim", text: "Nothing has changed: the container is still the one it was." }),
          h("button", { class: "primary", onClick: load }, "Try again"));
      }
    };

    const ask = () => {
      clear(card);
      card.append(
        h("h2", { text: `Update to ${state.latest}?` }),
        h("p", { class: "lead", text: "The screen goes blank for about a minute and comes back on its own. The playlist, the password and the network settings are kept." }),
        h("p", { class: "dim", text: "If the new version does not start, this device puts the old one back by itself." }),
        h("div", { class: "row" },
          h("button", { class: "primary", onClick: start }, `Update to ${state.latest}`),
          h("button", { onClick: () => { clear(card); card.append(offer()); } }, "Not now")));
    };

    const offer = () => h("div", {},
      h("h2", { text: "Install it" }),
      h("p", { class: "lead", text: "This device can update itself." }),
      h("button", { class: "primary", onClick: ask }, `Update to ${state.latest}`));

    card.append(offer());
    return card;
  };

  // On any device not set up for it, the page says exactly what to run rather
  // than leaving somebody to work it out.
  const byHand = (state) => h("div", { class: "card" },
    h("h2", { text: "Install it yourself" }),
    h("p", { class: "lead", text: state.whyNot }),
    h("p", { class: "dim", text: "On the machine itself:" }),
    h("pre", { class: "commands", text: `docker pull ${state.image}` }),
    h("p", { class: "dim", text: "then start it again with the same flags as before, using the new image." }));

  // quietly refreshes without the "Looking…" flicker, for the poll.
  const quietly = async () => {
    try {
      draw(await api.upgrade());
    } catch (error) {
      // A daemon that has stopped answering is the ordinary case here: it is
      // being replaced. Keep the last thing shown rather than replacing it
      // with an error somebody would read as a failure.
    }
  };

  const link = (url) => h("p", {},
    h("a", { href: url, target: "_blank", rel: "noreferrer noopener", text: "Read it on GitHub" }));

  load();
  return stopWatching;
}

// notes turns the release body into elements. Three shapes, because those are
// the three the changelog uses: "### Heading", "- an item", and a paragraph.
// Anything else is shown as the paragraph it looks like rather than dropped,
// so a note nobody anticipated is still readable.
function notes(markdown) {
  const out = [];
  let list = null;

  for (const raw of String(markdown).split("\n")) {
    const line = raw.trimEnd();

    if (line.startsWith("### ")) {
      list = null;
      out.push(h("h3", { text: line.slice(4).trim() }));
      continue;
    }
    if (line.startsWith("- ") || line.startsWith("* ")) {
      if (!list) {
        list = h("ul");
        out.push(list);
      }
      list.append(h("li", { text: line.slice(2).trim() }));
      continue;
    }
    if (line.trim() === "") {
      list = null;
      continue;
    }
    // A wrapped continuation of the item above belongs to it.
    if (list && (raw.startsWith("  ") || raw.startsWith("\t"))) {
      const last = list.lastElementChild;
      if (last) {
        last.textContent += " " + line.trim();
        continue;
      }
    }
    list = null;
    out.push(h("p", { text: line.trim() }));
  }
  return out;
}

// when says how long ago, because an operator cares whether this is minutes
// old or a fortnight, not what the clock said.
function when(stamp) {
  const at = new Date(stamp);
  if (Number.isNaN(at.getTime())) return "at an unknown time";

  const seconds = Math.max(0, (Date.now() - at.getTime()) / 1000);
  if (seconds < 90) return "just now";
  if (seconds < 3600) return `${Math.round(seconds / 60)} minutes ago`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)} hours ago`;
  return `${Math.round(seconds / 86400)} days ago`;
}
