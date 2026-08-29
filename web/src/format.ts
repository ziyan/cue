// The same formatting the interface has always used, moved across unchanged:
// an operator reading a screen from their desk cares about "1.8 GB of 15 GB"
// and "3s ago", not about bytes and timestamps.

export function bytes(value: number): string {
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
export function duration(nanoseconds: number): string {
  const seconds = Math.floor((nanoseconds || 0) / 1e9);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

export function ago(timestamp?: string): string {
  if (!timestamp || timestamp.startsWith("0001-01-01")) return "never";
  const elapsed = Date.now() - new Date(timestamp).getTime();
  if (elapsed < 0) return "just now";
  return `${duration(elapsed * 1e6)} ago`;
}

export function percentage(part: number, whole: number): number {
  if (!whole) return 0;
  return Math.min(100, Math.max(0, (part / whole) * 100));
}
