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

    // Until the button exists, and on any device not set up for it, the page
    // says exactly what to run instead of leaving somebody to work it out.
    body.append(h("div", { class: "card" },
      h("h2", { text: "Taking it" }),
      state.canApply
        ? h("p", { class: "lead", text: "This device is set up to upgrade itself." })
        : h("p", { class: "lead", text: state.whyNot }),
      h("p", { class: "dim", text: "On the machine itself:" }),
      h("pre", { class: "commands", text: `docker pull ${state.image}` }),
      h("p", { class: "dim", text: "then start it again with the same flags as before, using the new image." })));
  };

  const link = (url) => h("p", {},
    h("a", { href: url, target: "_blank", rel: "noreferrer noopener", text: "Read it on GitHub" }));

  load();
  return () => {};
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
