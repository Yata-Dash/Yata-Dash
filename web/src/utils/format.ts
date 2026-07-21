// utils/format.ts — display formatting helpers
// All unit formatting lives here. To change display format, edit only this file.
import type { AppSettings, StatField } from '../types';
import { appSettings } from '../state';

/** Format a day count per the user's duration_format setting:
 *  "ym" (default): 1Y 11M / 9M 2W / 45D — "days": 694 days.
 *  `format` overrides the saved setting (used by the live theme preview). */
export function fmtEtaDays(d: number, format?: string): string {
  const days = Math.max(0, Math.round(d));
  if ((format || appSettings.duration_format || 'ym') === 'days') {
    return `${days} day${days === 1 ? '' : 's'}`;
  }
  if (days >= 365) {
    const y = Math.floor(days / 365);
    const m = Math.round((days - y * 365) / 30.44);
    if (m >= 12) return `${y + 1}Y`;
    return m > 0 ? `${y}Y ${m}M` : `${y}Y`;
  }
  if (days >= 30) {
    let m = Math.floor(days / 30.44);
    let w = Math.round((days - m * 30.44) / 7);
    if (w >= 4) { m += 1; w = 0; } // 4 weeks ≈ a month — carry up (91d → "3M", not "2M 4W")
    return w > 0 ? `${m}M ${w}W` : `${m}M`;
  }
  return `${days}D`;
}

/** Format GiB value to a human-readable size string */
export function fmtGib(gib: number): string {
  if (!gib || isNaN(gib)) return '—';
  if (gib >= 1024) return (gib / 1024).toFixed(2) + ' TiB';
  return gib.toFixed(2) + ' GiB';
}

// ── Per-day trend rollovers (hover tooltips on stat values) ────────────────

/** Stat fields whose growth rate is a size (GiB/day); the rest are raw counts. */
const RATE_SIZE_FIELDS = new Set(['uploaded', 'downloaded', 'buffer', 'seed_size']);

/** Signed GiB/day → "245.3 GiB" / "-1.20 TiB" (buffer can shrink). */
function fmtGiBRate(gibPerDay: number): string {
  const sign = gibPerDay < 0 ? '-' : '';
  const g = Math.abs(gibPerDay);
  if (g >= 1024)  return `${sign}${(g / 1024).toFixed(2)} TiB`;
  if (g >= 1)     return `${sign}${g.toFixed(1)} GiB`;
  if (g >= 1/1024) return `${sign}${(g * 1024).toFixed(1)} MiB`;
  return `${sign}${(g * 1024 * 1024).toFixed(0)} KiB`;
}

/** Goal-pacing keys whose numbers are sizes (GiB/day) — the target keys
 *  goal pacing can project a growth rate for. Everything else is a plain
 *  count/day (or, for avg_seed, a day-count with no rate — see pacing.ts). */
const GOAL_SIZE_KEYS = new Set(['uploaded', 'downloaded', 'seed_size']);

/** Format a goal-pacing "needed"/"doing" per-day number in its row's own
 *  unit: GiB/day for size-backed keys (reusing fmtGiBRate), otherwise a
 *  plain count/day — mirrors internal/api/pacing.go's formatGoalRequired. */
export function fmtGoalRate(key: string, perDay: number): string {
  if (GOAL_SIZE_KEYS.has(key)) return `${fmtGiBRate(perDay)}/day`;
  const abs = Math.abs(perDay);
  const amount = abs >= 10 ? Math.round(perDay).toLocaleString() : perDay.toFixed(1);
  return `${amount}/day`;
}

/** Unit for the aggregate-card 7-day delta chips. Duplicates utils/series.ts's
 *  SeriesUnit (minus 'count', unused here) rather than importing it — series.ts
 *  already imports fmtEtaDays/fmtSeedTime from this file, so importing back
 *  would be circular. */
export type DeltaUnit = 'GiB' | 'ratio' | 'seconds';

/** Sign-preserving delta formatting for the aggregate cards' 7-day change
 *  chips (views/aggCards.ts) — mirrors views/detail.ts's per-stat delta chip
 *  (fmtSignedDelta there, not exported) so the top cards and Detail read as
 *  one system. Always shows a sign, unlike the plain formatters above. */
export function fmtSignedDelta(unit: DeltaUnit, dv: number): string {
  const sign = dv > 0 ? '+' : '-';
  const abs = Math.abs(dv);
  switch (unit) {
    case 'GiB':     return sign + fmtGiBRate(abs);
    case 'ratio':   return sign + abs.toFixed(2);
    case 'seconds': return sign + fmtEtaDays(abs / 86400);
  }
}

/** Format a goal deadline (YYYY-MM-DD) as "Dec 31" — parsed as a plain UTC
 *  date so it never shifts a day depending on the viewer's time zone. */
export function fmtDueDate(dateStr: string): string {
  const d = new Date(`${dateStr}T00:00:00Z`);
  if (isNaN(d.getTime())) return dateStr;
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' });
}

/**
 * Hover tooltip showing a stat's per-day trend, e.g. "≈ 245.3 GiB per day" or
 * "≈ 3,423 per day". Empty when the setting is off, the field has no measured
 * rate, or the rate rounds to nothing.
 */
export function rateTip(
  rates: Record<string, number> | undefined,
  field: string,
  settings: AppSettings,
): string {
  if (settings.show_rate_hovers === false) return '';
  const r = rates?.[field];
  if (!r || isNaN(r)) return '';
  let amount: string;
  if (RATE_SIZE_FIELDS.has(field)) {
    amount = fmtGiBRate(r);
  } else if (Math.abs(r) >= 10) {
    amount = Math.round(r).toLocaleString();
  } else {
    amount = r.toFixed(1);
    if (parseFloat(amount) === 0) return ''; // too small to be meaningful
  }
  return `≈ ${amount} per day`;
}

/** Parse a ratio field that may be numeric OR an "infinite" sentinel — some
 *  trackers report ratio as "∞" / "Inf" / "Infinity" when downloaded is 0.
 *  Returns Infinity for those (so it renders as ∞ and colours green, not a
 *  red 0.00), the number when numeric, or NaN when missing/unparseable. */
export function parseRatio(raw: unknown): number {
  const s = String(raw ?? '').trim().replace(/,/g, '');
  if (s === '') return NaN;
  if (/^(∞|inf|infinity)$/i.test(s)) return Infinity;
  return parseFloat(s);
}

/** Format a ratio to 2 decimal places. Infinite (downloaded = 0) → "∞". */
export function fmtRatio(r: number): string {
  const n = parseFloat(String(r));
  if (isNaN(n)) return '—';
  if (!isFinite(n)) return '∞';
  return n.toFixed(2);
}

/** Choose a CSS color variable name based on ratio value */
export function ratioColor(r: number): string {
  if (r >= 10) return 'green';
  if (r >= 1)  return 'amber';
  return 'red';
}

/**
 * min_ratio-aware ratio colour. When the tracker's account-wide required
 * ratio is known (> 0): red ONLY below it; a generically-red ratio at/above
 * the tracker minimum is bumped to amber. Green/amber thresholds unchanged.
 */
export function ratioColorFor(r: number, minRatio?: number): string {
  const base = ratioColor(r);
  if (!minRatio || minRatio <= 0) return base;
  if (r < minRatio) return 'red';
  return base === 'red' ? 'amber' : base;
}

/** Format total seconds into "1Y 2M 3W 4D 5h 06m 07s" */
export function fmtSeedTime(totalSec: number | null | undefined): string {
  if (totalSec == null || isNaN(Number(totalSec))) return '—';
  let t = Math.abs(Math.round(Number(totalSec)));
  if (t === 0) return '0s';
  const steps: [number, string][] = [
    [31536000, 'Y'], [2592000, 'M'], [604800, 'W'],
    [86400, 'D'], [3600, 'h'], [60, 'm'], [1, 's'],
  ];
  const parts: string[] = [];
  for (const [sec, u] of steps) {
    const v = Math.floor(t / sec);
    t -= v * sec;
    if (v) parts.push(`${v}${u}`);
  }
  return parts.join(' ') || '0s';
}

/** Seed time for compact card boxes: the Y/M/W/D units on the main line with
 *  h/m/s wrapped onto a smaller second line, so long durations (a heavy
 *  uploader's "333Y 9M 3W 4D 17h 30m 25s") stop overflowing horizontally
 *  while keeping every unit visible. Returns HTML. Sub-day values (only h/m/s)
 *  stay on one normal line. */
export function fmtSeedTimeStacked(totalSec: number | null | undefined): string {
  if (totalSec == null || isNaN(Number(totalSec))) return '—';
  let t = Math.abs(Math.round(Number(totalSec)));
  if (t === 0) return '0s';
  const steps: [number, string][] = [
    [31536000, 'Y'], [2592000, 'M'], [604800, 'W'], [86400, 'D'],
    [3600, 'h'], [60, 'm'], [1, 's'],
  ];
  const major: string[] = [];
  const minor: string[] = [];
  for (const [sec, u] of steps) {
    const v = Math.floor(t / sec);
    t -= v * sec;
    if (!v) continue;
    // Y/M/W/D headline (case-sensitive: "M" month, not "m" minute); h/m/s wrap.
    (u === 'Y' || u === 'M' || u === 'W' || u === 'D' ? major : minor).push(`${v}${u}`);
  }
  if (!major.length) return esc(minor.join(' ')) || '0s'; // sub-day → single line
  const minorHtml = minor.length ? `<span class="stat-time-minor">${esc(minor.join(' '))}</span>` : '';
  return `<span class="stat-time-major">${esc(major.join(' '))}</span>${minorHtml}`;
}

/** Format account age in days to "1Y 2M 3W 4D" */
export function fmtAgeDays(days: number): string {
  if (!days) return '—';
  const Y = Math.floor(days / 365), r1 = days % 365;
  const M = Math.floor(r1 / 30),   r2 = r1 % 30;
  const W = Math.floor(r2 / 7),    D = r2 % 7;
  return ([[Y,'Y'],[M,'M'],[W,'W'],[D,'D']] as [number,string][])
    .filter(([v]) => v)
    .map(([v, u]) => `${v}${u}`)
    .join(' ') || `${days}D`;
}

/** Format bytes/sec into human speed (KiB/s → MiB/s → GiB/s) */
export function fmtSpeed(bps: number): string {
  if (!bps || isNaN(bps) || bps <= 0) return '0 KiB/s';
  const kib = bps / 1024;
  if (kib >= 1024 * 1024) return (kib / 1024 / 1024).toFixed(2) + ' GiB/s';
  if (kib >= 1024)         return (kib / 1024).toFixed(2) + ' MiB/s';
  if (kib >= 1)            return kib.toFixed(1) + ' KiB/s';
  return bps.toFixed(0) + ' B/s';
}

/** Format raw bytes into B/KiB/MiB/GiB/TiB */
export function fmtBytes(b: number): string {
  if (!b || isNaN(b) || b <= 0) return '—';
  if (b >= 1024 ** 4) return (b / 1024 ** 4).toFixed(2) + ' TiB';
  if (b >= 1024 ** 3) return (b / 1024 ** 3).toFixed(2) + ' GiB';
  if (b >= 1024 ** 2) return (b / 1024 ** 2).toFixed(1) + ' MiB';
  if (b >= 1024)      return (b / 1024).toFixed(1) + ' KiB';
  return b + ' B';
}

/** Escape HTML special characters */
export function esc(s: unknown): string {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

/** Minimal numeric format — returns null for non-numeric values */
export function safeNum(val: unknown, decimals = 2): string | null {
  if (val == null || val === '') return null;
  const n = parseFloat(String(val).replace(/,/g, ''));
  if (isNaN(n)) return null;
  return decimals === 0 ? String(Math.round(n)) : n.toFixed(decimals);
}

/** Map error codes / error_kind values to user-friendly labels */
export function errLabel(err: string): string {
  const map: Record<string, string> = {
    no_key:           'API key not configured',
    disabled:         'Tracker is disabled',
    timeout:          'Connection timed out',
    connection_error: 'Could not connect',
    parse_error:      'Could not parse tracker response',
    api_error:        'Tracker API error',
    auth_error:       'Authentication failed',
    session_expired:  'Session cookie expired — log in again and re-copy it',
    empty_scrape:     'Profile page had no recognisable stats',
    store_error:      'Local storage error',
    http_401:         'Invalid API key (401)',
    http_403:         'Access forbidden (403)',
    http_404:         'Endpoint not found (404)',
    http_429:         'Rate limited (429)',
    http_500:         'Server error (500)',
  };
  if (map[err]) return map[err];
  const m = err.match(/^http_(\d+)$/);
  if (m) return `HTTP error (${m[1]})`;
  return `Error: ${err}`;
}

/** Prettify a canonical field key for generic display: "fl_tokens" → "Fl Tokens" */
export function fieldLabel(key: string): string {
  return key.split('_').map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');
}

/**
 * Per-stat source indicator dot. Returns '' unless settings.show_stat_sources
 * is on and the field carries provenance. Tooltip includes the update time.
 */
export function srcDot(field: StatField | undefined, settings: AppSettings): string {
  if (!settings.show_stat_sources || !field?.source) return '';
  const src = field.source === 'scrape' ? 'scrape'
    : field.source === 'manual' ? 'manual' : 'api';
  const when = field.updated_at
    ? ` · updated ${new Date(field.updated_at * 1000).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}`
    : '';
  const label = src === 'api' ? 'From API'
    : src === 'scrape' ? 'From profile scrape'
    : 'Entered manually';
  return `<span class="stat-src stat-src--${src}" title="${label}${when}"></span>`;
}

/** Format a tracker's display name according to the tracker_name_mode setting. */
export function fmtTrackerName(name: string, abbr: string, mode: string): string {
  if (mode === 'abbr') return abbr ? `[${abbr}]` : name;
  if (mode === 'both' && abbr) return `${name} [${abbr}]`;
  return name; // "name" or "" or no abbr
}
