// utils/parse.ts — parsing helpers for sizes, times, dates

/** Parse a size string (e.g. "3.14 TiB") → GiB as number, or null */
export function parseSize(str: unknown): number | null {
  if (!str) return null;
  const m = String(str).match(/([\d.]+)\s*(B|[KMGTP]i?B)/i);
  if (!m) return null;
  // Decimal-labelled units (kb/mb/gb/tb) map to the binary factors — tracker
  // software computes 1024-based sizes but often mislabels them.
  const factors: Record<string, number> = {
    b: 1 / (1024 ** 3), kib: 1 / (1024 ** 2), mib: 1 / 1024,
    gib: 1, tib: 1024, pib: 1048576,
    kb: 1 / (1024 ** 2), mb: 1 / 1024, gb: 1, tb: 1024, pb: 1048576,
  };
  return parseFloat(m[1]) * (factors[m[2].toLowerCase()] ?? 1);
}

/**
 * Parse seed time string → total seconds.
 * Supports: Y M W D h m s
 * Case rules:
 *   Y/y = years, M = months (UPPERCASE only), W/w = weeks, D/d = days,
 *   h = hours, m = minutes (lowercase only), s = seconds
 * Also accepts a plain integer/float as raw seconds.
 */
export function parseSeedTime(str: unknown): number | null {
  if (str == null || str === '') return null;
  const s = String(str).trim();
  if (!s) return null;
  // Plain number → raw seconds
  if (/^\d+(\.\d+)?$/.test(s)) return Math.round(parseFloat(s));

  let total = 0, found = false;
  const grp = (pattern: RegExp): number => {
    const m = s.match(pattern);
    if (!m) return 0;
    found = true;
    return parseFloat(m[1]);
  };
  total += grp(/(\d+(?:\.\d+)?)\s*[Yy](?![a-zA-Z])/) * 365 * 86400;
  total += grp(/(\d+(?:\.\d+)?)\s*M(?![a-zA-Z])/)    * 30  * 86400; // uppercase M
  total += grp(/(\d+(?:\.\d+)?)\s*[Ww](?![a-zA-Z])/) * 7   * 86400;
  total += grp(/(\d+(?:\.\d+)?)\s*[Dd](?![a-zA-Z])/) * 86400;
  total += grp(/(\d+(?:\.\d+)?)\s*h(?![a-zA-Z])/)    * 3600;
  total += grp(/(\d+(?:\.\d+)?)\s*m(?![a-zA-Z])/)    * 60;  // lowercase m
  total += grp(/(\d+(?:\.\d+)?)\s*s(?![a-zA-Z])/);
  return found ? Math.round(total) : null;
}

/** Calculate account age in whole days from a YYYY-MM-DD string */
export function memberDays(d: string): number | null {
  if (!d) return null;
  const j = new Date(d);
  if (isNaN(j.getTime())) return null;
  return Math.floor((Date.now() - j.getTime()) / 86_400_000);
}

/** Format account age in days to "1Y 2M 3W 4D" */
export function memberDur(d: string): string {
  const days = memberDays(d);
  if (days === null || days < 0) return '—';
  if (days === 0) return '0D';
  const Y = Math.floor(days / 365), r1 = days % 365;
  const M = Math.floor(r1 / 30),   r2 = r1 % 30;
  const W = Math.floor(r2 / 7),    D = r2 % 7;
  return ([[Y,'Y'],[M,'M'],[W,'W'],[D,'D']] as [number,string][])
    .filter(([v]) => v)
    .map(([v, u]) => `${v}${u}`)
    .join(' ') || '0D';
}

/**
 * Parse account-age target: plain number (days) or "Y M W D" format.
 * Returns total days.
 */
export function parseAgeDays(str: string): number | null {
  if (!str) return null;
  const s = String(str).trim();
  if (/^\d+$/.test(s)) return parseInt(s);
  let total = 0;
  const yr = s.match(/(\d+)\s*Y/i); if (yr) total += parseInt(yr[1]) * 365;
  const mo = s.match(/(\d+)\s*M/);  if (mo) total += parseInt(mo[1]) * 30;
  const wk = s.match(/(\d+)\s*W/i); if (wk) total += parseInt(wk[1]) * 7;
  const dd = s.match(/(\d+)\s*D/i); if (dd) total += parseInt(dd[1]);
  return total > 0 ? total : null;
}

/** Build a favicon URL from a tracker URL */
export function getFaviconUrl(trackerUrl: string): string {
  try {
    const u = new URL(trackerUrl);
    return `${u.protocol}//${u.host}/favicon.ico`;
  } catch { return ''; }
}
