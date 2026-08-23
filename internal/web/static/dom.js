// Element construction, so that nothing in this interface builds HTML by
// concatenating strings. A device name, a page title or an error message from
// a dashboard can contain anything at all, and innerHTML would run it.

export function h(tag, attributes, ...children) {
  const element = document.createElement(tag);

  for (const [name, value] of Object.entries(attributes || {})) {
    if (value === null || value === undefined || value === false) continue;
    if (name === "class") element.className = value;
    else if (name === "text") element.textContent = value;
    else if (name.startsWith("on") && typeof value === "function") {
      element.addEventListener(name.slice(2).toLowerCase(), value);
    } else if (name === "value") element.value = value;
    else if (name === "checked") element.checked = !!value;
    else element.setAttribute(name, value === true ? "" : value);
  }

  for (const child of children.flat(Infinity)) {
    if (child === null || child === undefined || child === false) continue;
    element.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return element;
}

export function clear(element) {
  while (element.firstChild) element.removeChild(element.firstChild);
}

// --- formatting -------------------------------------------------------------

export function bytes(value) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let index = 0;
  let amount = value;
  while (amount >= 1024 && index < units.length - 1) {
    amount /= 1024;
    index += 1;
  }
  return `${amount < 10 && index > 0 ? amount.toFixed(1) : Math.round(amount)} ${units[index]}`;
}

// duration takes nanoseconds, which is how Go encodes a time.Duration.
export function duration(nanoseconds) {
  const seconds = Math.floor((nanoseconds || 0) / 1e9);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

export function ago(timestamp) {
  if (!timestamp || timestamp.startsWith("0001-01-01")) return "never";
  const elapsed = Date.now() - new Date(timestamp).getTime();
  if (elapsed < 0) return "just now";
  return `${duration(elapsed * 1e6)} ago`;
}

export function percentage(part, whole) {
  if (!whole) return 0;
  return Math.min(100, Math.max(0, (part / whole) * 100));
}
