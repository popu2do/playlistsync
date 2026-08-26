/** Format utilities for the cockpit (durations, dates, bytes). */

/** "3:45" from milliseconds. */
export function formatMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '0:00';
  const totalSec = Math.floor(ms / 1000);
  const m = Math.floor(totalSec / 60);
  const s = totalSec % 60;
  return `${m}:${s.toString().padStart(2, '0')}`;
}

/** "3:45" from the Go-style duration string (e.g. "3:45" or "PT3M45S"). */
export function formatDurationString(duration: string | undefined): string {
  if (!duration) return '—';
  const m = /^(\d+):(\d{1,2})$/.exec(duration);
  if (m) {
    const secondsPart = m[2] ?? '0';
    return `${Number(m[1] ?? 0)}:${secondsPart.padStart(2, '0')}`;
  }
  const iso = /^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?$/.exec(duration);
  if (iso) {
    const h = Number(iso[1] ?? 0);
    const min = Number(iso[2] ?? 0);
    const sec = Math.floor(Number(iso[3] ?? 0));
    const total = h * 3600 + min * 60 + sec;
    const m2 = Math.floor(total / 60);
    return `${m2}:${(total % 60).toString().padStart(2, '0')}`;
  }
  return duration;
}

/** RFC3339 -> local "MM-DD HH:mm:ss" (or "—" when missing/invalid). */
export function formatTimestamp(rfc3339: string | undefined): string {
  if (!rfc3339) return '—';
  const d = new Date(rfc3339);
  if (Number.isNaN(d.getTime())) return rfc3339;
  const pad = (n: number) => n.toString().padStart(2, '0');
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(
    d.getMinutes(),
  )}:${pad(d.getSeconds())}`;
}

/** Compact byte size: "1.2 KB". */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '0 B';
  if (bytes < 1024) return `${bytes} B`;
  const units: string[] = ['KB', 'MB', 'GB'];
  let v = bytes;
  let i = -1;
  do {
    v /= 1024;
    i++;
  } while (v >= 1024 && i < units.length - 1);
  const unit = units[i] ?? 'B';
  return `${v.toFixed(1)} ${unit}`;
}

/** Percentage with fixed decimals (clamped 0..100). */
export function formatPercent(ratio: number, digits = 1): string {
  const pct = Math.max(0, Math.min(100, ratio * 100));
  return `${pct.toFixed(digits)}%`;
}

/**
 * Countdown from an expiry timestamp to a reference `now` (defaults to
 * Date.now()). Returns "1h 23m" / "42m" / "expired" / null when unknown.
 */
/** Minutes until an RFC3339 expiry (Infinity when unknown/invalid; negative when expired). */
export function minutesUntil(
  expiresAtRfc3339: string | undefined,
  nowMs: number = Date.now(),
): number {
  if (!expiresAtRfc3339) return Infinity;
  const exp = new Date(expiresAtRfc3339).getTime();
  if (Number.isNaN(exp)) return Infinity;
  return (exp - nowMs) / 60000;
}

export function formatCountdown(
  expiresAtRfc3339: string | undefined,
  nowMs: number = Date.now(),
): string | null {
  if (!expiresAtRfc3339) return null;
  const exp = new Date(expiresAtRfc3339).getTime();
  if (Number.isNaN(exp)) return null;
  const diffMs = exp - nowMs;
  if (diffMs <= 0) return 'expired';
  const totalMin = Math.floor(diffMs / 60000);
  const h = Math.floor(totalMin / 60);
  const m = totalMin % 60;
  return h > 0 ? `${h}h ${m}m` : `${m}m`;
}