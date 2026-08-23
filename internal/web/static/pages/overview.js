// The page somebody opens when they want to know whether the screen is
// working. Everything on it answers that question and nothing else.

import { h, clear, bytes, duration, ago, percentage } from "../dom.js";
import { api } from "../api.js";

const refreshInterval = 3000;

export function overview(main) {
  const body = h("div");
  main.append(body);

  let stopped = false;

  // The screenshot element is made once and kept, and every redraw puts this
  // same element back into the page.
  //
  // The page is rebuilt from scratch every few seconds, and a fresh <img> has
  // nothing in it until its picture arrives: the card went blank, then filled
  // in, three times a minute, for as long as anybody watched it. On a 2560x1440
  // screen the picture is large enough for that gap to be the most obvious
  // thing on the page.
  const screenshot = h("img", {
    class: "screenshot",
    alt: "What the screen is showing",
  });
  let screenshotFailed = false;

  // And the new picture is decoded before it is shown, so that swapping it in
  // replaces one complete image with another rather than clearing the first.
  const refreshScreenshot = () => {
    const incoming = new Image();
    incoming.onload = () => {
      if (stopped) return;
      screenshot.src = incoming.src;
      screenshotFailed = false;
      screenshot.classList.remove("stale");
    };
    incoming.onerror = () => {
      if (stopped) return;
      // Keep whatever is already there. A screenshot that failed once is
      // usually a browser that is restarting, and a card that empties itself
      // says less than the last picture and a note that it is old.
      screenshotFailed = true;
      if (screenshot.src) screenshot.classList.add("stale");
    };
    incoming.src = `/api/v1/screenshot.png?small=1&at=${Date.now()}`;
  };

  const refresh = async () => {
    if (stopped) return;
    refreshScreenshot();
    try {
      const status = await api.status();
      if (!stopped) draw(body, status, screenshot, () => screenshotFailed && !screenshot.src);
    } catch (error) {
      if (!stopped) {
        clear(body);
        body.append(h("div", { class: "notice bad", text: String(error.message || error) }));
      }
    }
  };

  refresh();
  const timer = setInterval(refresh, refreshInterval);

  return () => {
    stopped = true;
    clearInterval(timer);
  };
}

function draw(body, status, screenshot, hasNoPicture) {
  clear(body);

  body.append(
    h("div", { class: "grid" },
      screenshotCard(status, screenshot, hasNoPicture),
      h("div", {},
        programsCard(status),
        watchdogCard(status))),
    h("div", { class: "grid" },
      machineCard(status.machine),
      displayCard(status)));
}

// The screenshot is the single most useful thing on this page: it answers
// "what is it showing" without a VNC connection and without leaving the desk.
function screenshotCard(status, image, hasNoPicture) {
  const showing = status.browser.currentTitle || status.browser.currentUrl || "nothing yet";

  return h("div", { class: "card" },
    h("h2", { text: "On the screen" }),
    hasNoPicture()
      ? h("div", { class: "notice", text: "No picture yet — the browser is still starting." })
      : image,
    h("div", { class: "readout" },
      h("span", { class: "label", text: "Showing" }),
      h("span", { class: "value truncate", text: showing })),
    status.browser.currentUrl
      ? h("div", { class: "readout" },
          h("span", { class: "label", text: "Address" }),
          h("span", { class: "value mono truncate", text: status.browser.currentUrl }))
      : null);
}

function programsCard(status) {
  const rows = status.programs.map((program) => {
    const tone = program.state === "running" ? "good" : program.state === "backoff" ? "bad" : "warn";
    return h("div", { class: "readout" },
      h("span", { class: "label" },
        program.name,
        program.restarts > 0 ? h("span", { class: "dim", text: ` · ${program.restarts} restart${program.restarts === 1 ? "" : "s"}` }) : null),
      h("span", { class: "value" },
        h("span", { class: `pill ${tone}`, text: program.state }),
        " ",
        h("span", { class: "dim", text: program.startedAt ? ago(program.startedAt) : "" })));
  });

  const failing = status.programs.filter((program) => program.lastError);

  return h("div", { class: "card" },
    h("h2", { text: "Programs" }),
    rows.length ? rows : h("p", { class: "dim", text: "Nothing is running yet." }),
    failing.map((program) => h("div", { class: "notice bad", text: `${program.name}: ${program.lastError}` })));
}

function watchdogCard(status) {
  const watchdog = status.watchdog;
  if (!watchdog.enabled) {
    return h("div", { class: "card" },
      h("h2", { text: "Watchdog" }),
      h("p", { class: "dim", text: "Switched off. A frozen screen will stay frozen." }));
  }

  const healthy = watchdog.consecutiveFailures === 0;

  return h("div", { class: "card" },
    h("h2", { text: "Watchdog" }),
    h("div", { class: "readout" },
      h("span", { class: "label", text: "Now" }),
      h("span", { class: "value" },
        h("span", {
          class: `pill ${healthy ? "good" : "bad"}`,
          text: watchdog.suspended ? "paused" : healthy ? "answering" : `${watchdog.consecutiveFailures} failed probes`,
        }))),
    h("div", { class: "readout" },
      h("span", { class: "label", text: "Last answer" }),
      h("span", { class: "value", text: ago(watchdog.lastSuccessAt) })),
    h("div", { class: "readout" },
      h("span", { class: "label", text: "Rescued" }),
      h("span", { class: "value", text: `${watchdog.remediesApplied} time${watchdog.remediesApplied === 1 ? "" : "s"}` })),
    watchdog.lastRemedy
      ? h("div", { class: "readout" },
          h("span", { class: "label", text: "Last action" }),
          h("span", { class: "value", text: `${watchdog.lastRemedy}, ${ago(watchdog.lastRemedyAt)}` }))
      : null,
    watchdog.lastFailure ? h("div", { class: "notice bad", text: watchdog.lastFailure }) : null);
}

function machineCard(machine) {
  const memoryUsed = percentage(machine.memory.used, machine.memory.total);
  const hottest = (machine.thermal || []).reduce(
    (best, sensor) => (best && best.celsius >= sensor.celsius ? best : sensor), null);

  return h("div", { class: "card" },
    h("h2", { text: "This machine" }),
    meter("Processor", `${machine.cpu.usagePercent.toFixed(0)}%`, machine.cpu.usagePercent),
    meter("Memory", `${bytes(machine.memory.used)} of ${bytes(machine.memory.total)}`, memoryUsed),
    (machine.disks || []).map((disk) =>
      meter(`Disk ${disk.path}`, `${bytes(disk.used)} of ${bytes(disk.total)}`, percentage(disk.used, disk.total))),
    h("div", { class: "readout" },
      h("span", { class: "label", text: "Load" }),
      h("span", { class: "value", text: machine.loadAverage.map((value) => value.toFixed(2)).join("  ") })),
    hottest
      ? h("div", { class: "readout" },
          h("span", { class: "label", text: "Temperature" }),
          h("span", { class: "value", text: `${hottest.celsius.toFixed(0)} °C (${hottest.name})` }))
      : null,
    h("div", { class: "readout" },
      h("span", { class: "label", text: "Machine up" }),
      h("span", { class: "value", text: duration(machine.uptime) })),
    h("div", { class: "readout" },
      h("span", { class: "label", text: "Processor" }),
      h("span", { class: "value dim truncate", text: machine.cpu.model || `${machine.cpu.count} cores` })));
}

function meter(label, value, percent) {
  const tone = percent >= 90 ? "bad" : percent >= 75 ? "warn" : "";
  return h("div", {},
    h("div", { class: "readout" },
      h("span", { class: "label", text: label }),
      h("span", { class: "value", text: value })),
    h("div", { class: `meter ${tone}` }, h("i", { style: `width:${percent.toFixed(1)}%` })));
}

function displayCard(status) {
  const outputs = (status.outputs || []).filter((output) => output.connected);
  const connectors = status.connectors || [];

  return h("div", { class: "card" },
    h("h2", { text: "Display" }),
    h("div", { class: "readout" },
      h("span", { class: "label", text: "Screen" }),
      h("span", { class: "value", text: status.screen.width ? `${status.screen.width} × ${status.screen.height}` : "not up yet" })),
    outputs.length
      ? outputs.map((output) => h("div", { class: "readout" },
          h("span", { class: "label" }, output.name, output.primary ? h("span", { class: "dim", text: " · primary" }) : null),
          h("span", { class: "value", text: output.enabled ? `${output.currentMode} at ${output.x},${output.y}` : "connected, off" })))
      : h("p", { class: "dim", text: "The X server is not reporting any connected output." }),
    connectors.length
      ? h("div", { class: "readout" },
          h("span", { class: "label", text: "Sockets" }),
          h("span", { class: "value dim", text: connectors.map((one) => `${one.name}${one.connected ? "" : " (empty)"}`).join(", ") }))
      : null);
}
