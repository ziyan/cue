// The daemon writes durations as text -- "30s", "1m30s" -- because that is
// what reads sensibly in the configuration file. Forms want a number.

export function secondsOf(value?: string): string {
  if (!value) return "";
  const match = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?)s)?$/.exec(String(value));
  if (!match) return "";
  const total = (parseInt(match[1] ?? "0", 10) * 3600)
    + (parseInt(match[2] ?? "0", 10) * 60)
    + Math.round(parseFloat(match[3] ?? "0"));
  return total === 0 ? "" : String(total);
}

export function asSeconds(value: string, least = 1): string {
  return `${Math.max(least, parseInt(value, 10) || least)}s`;
}
