// The status document, as the daemon sends it.

export interface Program {
  name: string;
  state: string;
  restarts: number;
  startedAt?: string;
  lastError?: string;
}

export interface Status {
  device: { name: string; identifier: string; location?: string; version: string; uptime: string };
  programs: Program[];
  browser: { currentTitle?: string; currentUrl?: string };
  watchdog: {
    enabled: boolean; suspended?: boolean; consecutiveFailures: number;
    lastSuccessAt?: string; remediesApplied: number;
    lastRemedy?: string; lastRemedyAt?: string; lastFailure?: string;
  };
  machine: {
    cpu: { usagePercent: number; model?: string; count: number };
    memory: { used: number; total: number };
    disks?: { path: string; used: number; total: number }[];
    thermal?: { name: string; celsius: number }[];
    loadAverage: number[];
    uptime: number;
  };
  connectors?: { name: string; connected: boolean }[];
  outputs?: { name: string; connected: boolean; primary?: boolean; enabled?: boolean;
              currentMode?: string; x?: number; y?: number }[];
  screen: { width?: number; height?: number };
  ignoredSettings?: string[];
}
