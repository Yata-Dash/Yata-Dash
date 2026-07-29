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

// ── Bonus points ──────────────────────────────────────────────────────────
//
// Trackers report these fractionally (UNIT3D accrues per minute, so "98432.50"
// is normal) and they get very large: a high hourly rate reaches nine digits,
// and trackers running point raffles pay out ten. Neither suits a table cell
// or a ~120px card stat box, so the dashboard surfaces show a floored and,
// past ten million, abbreviated figure. History, Tracker Detail and the
// expanded row keep the exact number.

/** Parse a reported bonus-points value. Tolerates thousands separators and
 *  numeric input; null when there's nothing usable to show. */
function parsePoints(raw: string | number | null | undefined): number | null {
  if (raw === null || raw === undefined || raw === '') return null;
  const n = typeof raw === 'number' ? raw : parseFloat(String(raw).replace(/,/g, ''));
  return Number.isFinite(n) ? n : null;
}

/**
 * Bonus points for the table and grid card.
 *
 * ALWAYS ROUNDS DOWN, never to nearest — no tracker lets you spend part of a
 * point, and a 50,000-point requirement isn't met by 49,999.63. Rounding to
 * nearest would display "50,000" and claim it was. The abbreviated range
 * truncates for the same reason, so 219,664,390 reads "219M" rather than
 * being rounded up to "220M": the figure shown is never more than you hold.
 *
 * Under TEN million the exact count is kept. Most balances live between ten
 * thousand and ten million, and that's the range where you're saving toward a
 * specific purchase and want the real number — it also genuinely fits: the
 * widest such value, "9,999,999", measures 70px beside an 11px source dot in
 * the narrowest card's 105px stat box. Abbreviating there would be hiding
 * digits there's room for.
 *
 * Returns '' when there's nothing to show, so callers can fall back to '—'.
 */
export function fmtBonusPoints(raw: string | number | null | undefined): string {
  const n = parsePoints(raw);
  if (n === null) return '';
  const sign = n < 0 ? '-' : '';
  const v = Math.abs(n);
  if (v < 1e7) return sign + Math.floor(v).toLocaleString();
  const units: [string, number][] = [['T', 1e12], ['B', 1e9], ['M', 1e6]];
  for (const [unit, div] of units) {
    if (v < div) continue;
    const scaled = v / div;
    // Three significant figures — 10.5M, 219M, 1.23B — truncated, not rounded.
    const dp = scaled >= 100 ? 0 : scaled >= 10 ? 1 : 2;
    const factor = 10 ** dp;
    return `${sign}${(Math.floor(scaled * factor) / factor).toFixed(dp)}${unit}`;
  }
  return sign + Math.floor(v).toLocaleString();
}

/** The unabbreviated figure, for the hover text beside fmtBonusPoints.
 *  Keeps the fraction the tracker reported so nothing is silently lost. */
export function fmtBonusPointsExact(raw: string | number | null | undefined): string {
  const n = parsePoints(raw);
  if (n === null) return '';
  return `${n.toLocaleString(undefined, { maximumFractionDigits: 2 })} points`;
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
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * A value safe to drop into an inline event handler's argument list, e.g.
 * onclick="openEditModal('${jsId(id)}')". Anything outside [A-Za-z0-9_-] makes
 * it return '' rather than being escaped.
 *
 * Validation, not escaping — because escaping CANNOT work here. An attribute's
 * character references are decoded before the JavaScript is parsed, so
 * `&#39;); alert(1); //` arrives at the JS parser as `'); alert(1); //` and
 * runs. esc() escaping quotes protects quoted ATTRIBUTES; it does nothing for
 * inline handlers, and treating it as though it does is the trap here.
 *
 * Every value these handlers carry today is a server-generated ID, a canonical
 * field key or a fixed literal — all safely inside the character class. This
 * makes that an enforced invariant rather than a convention: the day someone
 * passes a tracker NAME to one of them, the button goes inert instead of
 * becoming an injection point.
 */
export function jsId(v: unknown): string {
  const s = String(v ?? '');
  return /^[A-Za-z0-9_-]+$/.test(s) ? s : '';
}

/**
 * A URL safe to put in an href, or '' if it isn't one.
 *
 * esc() makes a string safe as HTML *text* and safe inside a quoted attribute,
 * but it does nothing about the SCHEME — `javascript:alert(1)` contains no
 * escapable character, so an escaped value in an href is still a live script
 * URL waiting for a click. That matters because not every URL Yata renders is
 * typed by the user: tracker APIs supply them (the active_events list carries a
 * link per event), as do Prowlarr/Jackett imports and the community pathways
 * dataset. A hostile or compromised tracker handing back
 * `{"url": "javascript:fetch('/api/tokens')…"}` would otherwise get a
 * same-origin script running against a logged-in dashboard.
 *
 * Only http and https pass. Relative URLs are rejected too: everything this
 * guards is an off-site link, so a relative one is a sign something is wrong
 * rather than a case worth supporting.
 */
export function safeUrl(raw: unknown): string {
  const s = String(raw ?? '').trim();
  if (!s) return '';
  try {
    // Parsed, not pattern-matched: the URL parser already knows about the
    // tricks — leading control characters, embedded newlines, "jAvAsCrIpT:",
    // "java\tscript:" — that a regex on the raw string misses.
    const u = new URL(s);
    return u.protocol === 'http:' || u.protocol === 'https:' ? u.href : '';
  } catch {
    return ''; // not absolute, or not parseable at all
  }
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
    user_id_not_found: 'Not logged in — session cookie likely expired',
    user_not_found:   'Profile page not found',
    forbidden:        'Access forbidden — cookie or permissions',
    read_error:       'Could not read tracker response',
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

// ── Absolute timestamps ──────────────────────────────────────────────────────
//
// Dates are written year-first everywhere Yata shows an absolute one. It reads
// the same in every country — 07/08 is the 7th of August to most of the world
// and the 8th of July to the US, and a dashboard aggregating trackers from
// everywhere can't afford that ambiguity — and it sorts as it reads.
//
// Built from local date parts rather than toISOString(), which is UTC: an
// evening in a positive-offset zone lands on the following UTC day, so the
// date half would silently disagree with the time half beside it.

const pad2 = (n: number): string => (n < 10 ? '0' + n : String(n));

/** Local calendar date — "2026-07-18". */
export function fmtDay(d: Date): string {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
}

/** Local date and time for hover text — "2026-07-18 15:55". Seconds are left
 *  off: nothing here is timed that finely, and they only add noise. */
export function fmtStamp(unixSec: number): string {
  const d = new Date(unixSec * 1000);
  return `${fmtDay(d)} ${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
}

/** Whether a tracker_events kind is a connection event — either channel, or
 *  the pre-split kind still sitting in older databases. */
export function isConnectionKind(kind: string): boolean {
  return kind === 'connection' || kind.startsWith('connection_');
}

/**
 * Decode a connection event for display.
 *
 * `kind` is "connection_api" | "connection_scrape" — or plain "connection" for
 * events recorded before Yata tracked the two channels separately, which carry
 * no channel and so get the unqualified wording. `detail` is "up" or
 * "down:<errorKind>".
 *
 * Returns both lengths because the surfaces differ: a chart flag has room for
 * "API down" and nothing more, while a timeline row can say what actually
 * broke.
 */
export function connectionEventText(kind: string, detail: string): { up: boolean; label: string; text: string } {
  const chan = kind === 'connection_api' ? 'API' : kind === 'connection_scrape' ? 'Scrape' : '';
  if (detail === 'up') {
    return {
      up: true,
      label: chan ? `${chan} up` : 'Up',
      text: chan ? `${chan} back online` : 'Back online',
    };
  }
  // Colon, not a dash: some errLabel strings carry their own em-dash clause
  // ("Session cookie expired — log in again and re-copy it") and two in one
  // line reads as a typo.
  const reason = errLabel(detail.replace(/^down:/, ''));
  return {
    up: false,
    label: chan ? `${chan} down` : 'Down',
    text: chan ? `${chan} unreachable: ${reason}` : `Unreachable: ${reason}`,
  };
}

/** Prettify a canonical field key for generic display: "fl_tokens" → "Fl Tokens" */
export function fieldLabel(key: string): string {
  return key.split('_').map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');
}

/**
 * How fresh a single stat is, and where it came from — e.g. "as of the last
 * API update (2026-07-28 14:32)". Returns '' when the field carries no
 * provenance, so callers say nothing rather than guessing.
 *
 * Every stat records the source it was merged from, so this is what actually
 * happened rather than an assumption about the tracker. That matters for the
 * trackers whose answer changes over time: a UNIT3D site whose API reports
 * unread mail directly, one where it only comes from the profile page, and
 * one where the API provides it until it fails and the scrape takes over —
 * all three get the right wording without anything being declared anywhere.
 */
export function fieldFreshness(field: StatField | undefined): string {
  if (!field?.source) return '';
  const when = field.updated_at ? ` (${fmtStamp(field.updated_at)})` : '';
  switch (field.source) {
    case 'scrape': return `as of the last profile scrape${when}`;
    case 'manual': return 'entered manually';
    case 'qui':    return `as of the last qui sync${when}`;
    default:       return `as of the last API update${when}`;
  }
}

/**
 * The unread mail / notification flags shown beside a tracker's name, in one
 * place rather than three. Each icon follows its own Display toggle and only
 * renders when the flag is actually true.
 *
 * Built here because the previous copies in the grid, table and detail views
 * were identical by convention alone — which is how all three ended up
 * asserting "as of the last scrape" for trackers that are never scraped.
 * Callers pass the fields so this stays free of app-state imports.
 */
export function unreadFlagsHtml(
  trackerName: string,
  mail: StatField | undefined,
  notifications: StatField | undefined,
  settings: AppSettings,
): string {
  const flag = (field: StatField | undefined, on: boolean, icon: string, what: string, suffix = '') => {
    if (!on || String(field?.value ?? '') !== 'true') return '';
    const fresh = fieldFreshness(field);
    // Comma, not parentheses: fieldFreshness already parenthesises its
    // timestamp, and nesting the two reads as a typo.
    const tip = `${what} on ${trackerName}${fresh ? `, ${fresh}` : ''}${suffix}`;
    return `<span class="unread-flag" title="${esc(tip)}"><i class="fas ${icon}"></i></span>`;
  };
  return flag(mail, settings.show_unread_mail !== false, 'fa-envelope', 'Unread mail', ' — check your inbox')
    + flag(notifications, settings.show_unread_notifications !== false, 'fa-bell', 'Unread notifications');
}

/**
 * Per-stat source indicator dot. Returns '' unless settings.show_stat_sources
 * is on and the field carries provenance. Tooltip includes the update time.
 */
export function srcDot(field: StatField | undefined, settings: AppSettings): string {
  if (!settings.show_stat_sources || !field?.source) return '';
  const src = field.source === 'scrape' ? 'scrape'
    : field.source === 'manual' ? 'manual'
    : field.source === 'qui' ? 'qui' : 'api';
  const when = field.updated_at
    ? ` · updated ${new Date(field.updated_at * 1000).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}`
    : '';
  const label = src === 'api' ? 'From API'
    : src === 'scrape' ? 'From profile scrape'
    : src === 'qui' ? 'From qui'
    : 'Entered manually';
  return `<span class="stat-src stat-src--${src}" title="${label}${when}"></span>`;
}

/** Format a tracker's display name according to the tracker_name_mode setting. */
export function fmtTrackerName(name: string, abbr: string, mode: string): string {
  if (mode === 'abbr') return abbr ? `[${abbr}]` : name;
  if (mode === 'both' && abbr) return `${name} [${abbr}]`;
  return name; // "name" or "" or no abbr
}
