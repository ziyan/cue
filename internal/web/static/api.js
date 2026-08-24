// Every call to the daemon goes through here, so that a session that has
// expired is handled in one place rather than in nine.

let onUnauthorized = () => {};

export function whenSignedOut(handler) {
  onUnauthorized = handler;
}

async function request(method, path, body) {
  const options = { method, headers: {}, credentials: "same-origin" };
  if (body !== undefined) {
    options.headers["Content-Type"] = "application/json";
    options.body = JSON.stringify(body);
  }

  const response = await fetch(path, options);

  if (response.status === 401 || response.status === 403) {
    onUnauthorized();
    throw new Error("signed out");
  }

  const type = response.headers.get("Content-Type") || "";
  const payload = type.includes("application/json") ? await response.json() : await response.text();

  if (!response.ok) {
    const message = payload && payload.error ? payload.error : `${response.status} ${response.statusText}`;
    throw new Error(message);
  }
  return payload;
}

export const api = {
  setupState: () => request("GET", "/api/v1/setup"),
  setup: (body) => request("POST", "/api/v1/setup", body),
  signIn: (password) => request("POST", "/api/v1/session", { password }),
  signOut: () => request("DELETE", "/api/v1/session"),

  status: () => request("GET", "/api/v1/status"),
  configuration: () => request("GET", "/api/v1/configuration"),
  saveConfiguration: (configuration) => request("PUT", "/api/v1/configuration", configuration),

  show: (item) => request("POST", `/api/v1/show/${encodeURIComponent(item)}`),
  navigate: (url) => request("POST", "/api/v1/navigate", { url }),
  restart: (program) => request("POST", `/api/v1/restart/${encodeURIComponent(program)}`),
  xorgLog: () => request("GET", "/api/v1/logs/xorg"),

  timezones: () => request("GET", "/api/v1/timezones"),
  network: () => request("GET", "/api/v1/network"),
  scanWireless: (name) => request("POST", `/api/v1/network/scan/${encodeURIComponent(name)}`),

  enrolInFleet: (url, token) => request("POST", "/api/v1/fleet", { url, token }),
  leaveFleet: () => request("DELETE", "/api/v1/fleet"),
};
