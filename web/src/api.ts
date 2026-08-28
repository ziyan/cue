// Everything the interface asks the daemon for.
//
// One place, so that a call that starts failing is found by its name rather
// than by grepping for a path, and so that signing out is noticed once.

export class Refused extends Error {}

let onSignedOut: (() => void) | null = null;

export function whenSignedOut(handler: () => void) {
  onSignedOut = handler;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  if (response.status === 401) {
    onSignedOut?.();
    throw new Refused("sign in first");
  }
  if (!response.ok) {
    let said = `${response.status} ${response.statusText}`;
    try {
      const problem = (await response.json()) as { error?: string };
      if (problem.error) said = problem.error;
    } catch {
      // Not JSON. The status is all there is to say.
    }
    throw new Error(said);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

// The configuration document, as much of it as this interface names so far.
// The index signature is there because the daemon owns the shape and the
// interface saves back whatever it was given: a field this build has never
// heard of must survive a round trip rather than be dropped.
export interface Configuration {
  device: {
    name: string;
    location: string;
    timezone: string;
    language: string;
    identifier?: string;
  };
  [section: string]: unknown;
}

export interface LogEntry {
  at?: string;
  monotonic?: number;
  severity?: string;
  text: string;
}

export interface SetupState {
  needsSetup: boolean;
  signedIn: boolean;
  device: { name?: string; identifier?: string; language?: string };
  version?: string;
}

export const api = {
  setupState: () => request<SetupState>("GET", "/api/v1/setup"),
  setup: (body: Record<string, string>) => request<void>("POST", "/api/v1/setup", body),
  signIn: (password: string) => request<void>("POST", "/api/v1/session", { password }),
  signOut: () => request<void>("DELETE", "/api/v1/session"),

  status: () => request<Record<string, unknown>>("GET", "/api/v1/status"),
  configuration: () => request<Configuration>("GET", "/api/v1/configuration"),
  saveConfiguration: (configuration: Configuration) =>
    request<Configuration>("PUT", "/api/v1/configuration", configuration),
  restart: (program: string) => request<void>("POST", `/api/v1/restart/${encodeURIComponent(program)}`),
  navigate: (url: string) => request<void>("POST", "/api/v1/navigate", { url }),
  xorgLog: () => request<LogEntry[]>("GET", "/api/v1/logs/xorg"),

  upgrade: () => request<Record<string, unknown>>("GET", "/api/v1/upgrade"),
  applyUpgrade: () => request<Record<string, unknown>>("POST", "/api/v1/upgrade"),
};
