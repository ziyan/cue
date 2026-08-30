// Everything the interface asks the daemon for.
//
// One place, so that a call that starts failing is found by its name rather
// than by grepping for a path, and so that signing out is noticed once.

export class Refused extends Error {}

let onSignedOut: (() => void) | null = null;

export function whenSignedOut(handler: () => void) {
  onSignedOut = handler;
}

// The version of the configuration this interface last read.
//
// Held here rather than passed around because every part of the interface
// edits one document: a page reads it, somebody types, and the save has to say
// which version it was an edit of. Without that the last save wins and the
// other is gone with nobody told -- which two tabs on the settings page did to
// each other before this existed.
let configurationVersion = "";

export class Conflict extends Error {
  readonly configuration: Configuration;
  constructor(said: string, configuration: Configuration) {
    super(said);
    this.name = "Conflict";
    this.configuration = configuration;
  }
}

async function request<T>(method: string, path: string, body?: unknown,
                          headers?: Record<string, string>): Promise<T> {
  const response = await fetch(path, {
    method,
    headers: {
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      ...(headers ?? {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  if (response.status === 401) {
    onSignedOut?.();
    throw new Refused("sign in first");
  }
  // Somebody else saved while this was being edited. Carries what is actually
  // on the device, so a page can show what changed rather than saying "try
  // again" and hoping.
  if (response.status === 409) {
    const told = (await response.json()) as { error?: string; configuration?: Configuration };
    configurationVersion = response.headers.get("ETag") ?? "";
    throw new Conflict(
      told.error ?? "somebody else changed this while you were editing it",
      told.configuration as Configuration);
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
  // Noted wherever it appears, rather than by whoever happens to be asking:
  // one place that reads it means a save always sends the version of the
  // document it was actually an edit of, including a save straight after
  // another save with no read in between.
  const version = response.headers.get("ETag");
  if (version && path === "/api/v1/configuration") configurationVersion = version;

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
  service?: {
    address: string;
    account?: string;
    deviceId?: string;
    // What the service calls this device, which is not always what it calls
    // itself: an account cannot hold two devices of one name, so a second
    // screen called "carbon" is recorded there as "carbon 2".
    name?: string;
  };
  [section: string]: unknown;
}

export interface WirelessNetwork {
  ssid: string;
  signalStrength: number;
  security?: string;
}

export interface Interface {
  name: string;
  kind: string;
  carrier?: boolean;
  up?: boolean;
  addresses?: string[];
  gateway?: string;
  nameservers?: string[];
  receivedBytes: number;
  transmittedBytes: number;
  wireless?: { ssid?: string; state: string; signalStrength?: number };
}

export interface NetworkState {
  interfaces?: Interface[];
  errors?: Record<string, string>;
  problem?: string;
}

export interface UpgradeState {
  running: string;
  latest?: string;
  notes?: string;
  publishedAt?: string;
  url?: string;
  newer: boolean;
  checkedAt?: string;
  trouble?: string;
  canApply: boolean;
  whyNot?: string;
  image?: string;
  progress: {
    running: boolean;
    version?: string;
    stage?: string;
    startedAt?: string;
    trouble?: string;
  };
}

// What the daemon knows about this device's attachment to the hosted service:
// whether it is linked, and whether a code is up and waiting to be scanned.
export interface LinkState {
  linked: boolean;
  account?: string;
  pending: boolean;
  url?: string;
  expiresAt?: string;
  // Somebody has authorised it and the device is collecting the credential and
  // proving it works. A different thing to be waiting for: the code has done
  // its job and the phone can be put away.
  checking?: boolean;
  error?: string;
}

// Whether the device is getting through to the service it is linked to.
// Separate from being linked, because they fail separately: a device can hold
// a perfectly good credential and be unable to reach anything.
export interface ReportingState {
  attached: boolean;
  lastReportedAt?: string;
  trouble?: string;
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
    request<Configuration>("PUT", "/api/v1/configuration", configuration,
      configurationVersion ? { "If-Match": configurationVersion } : undefined),
  network: () => request<NetworkState>("GET", "/api/v1/network"),
  scanWireless: (name: string) =>
    request<{ networks: WirelessNetwork[] }>("POST", `/api/v1/network/scan/${encodeURIComponent(name)}`),

  show: (item: string) => request<void>("POST", `/api/v1/show/${encodeURIComponent(item)}`),
  restart: (program: string) => request<void>("POST", `/api/v1/restart/${encodeURIComponent(program)}`),
  navigate: (url: string) => request<void>("POST", "/api/v1/navigate", { url }),
  xorgLog: () => request<LogEntry[]>("GET", "/api/v1/logs/xorg"),

  link: () => request<LinkState>("GET", "/api/v1/link"),
  startLink: () => request<LinkState>("POST", "/api/v1/link"),
  abandonLink: () => request<LinkState>("DELETE", "/api/v1/link"),
  forgetLink: () => request<LinkState>("POST", "/api/v1/link/forget"),

  upgrade: () => request<UpgradeState>("GET", "/api/v1/upgrade"),
  applyUpgrade: () => request<void>("POST", "/api/v1/upgrade"),
};
