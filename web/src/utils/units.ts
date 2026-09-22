// utils/units.ts — one place for the two halves of "which units is that?"
//
// Display: a tracker reports a size with whatever label its software prints
// ("379.40 GB" on a TBDev site, "3.77 TiB" on UNIT3D). Yata reads both with
// the 1024 factor (parseSize), so the label is a spelling, not a magnitude —
// and a card showing "379.40 GB − 2.26 GB = 377.14 GiB" is right and still
// confusing. The size_units setting picks the spelling: keep each tracker's
// own, or relabel everything to KiB/MiB/GiB/TiB.
//
// Input: values typed by hand — manual stats, targets, alert thresholds —
// have no label to be faithful to, so they are read leniently ("200g",
// "3 months 6 days") and put in one canonical form in front of the user, or
// refused with the shape spelled out. The server repeats these checks
// (internal/api/typedvalues.go); a rejected value never reaches the config.

import type { AppSettings } from '../types';
import { appSettings } from '../state';
import { fmtSeedTime } from './format';
import { parseAgeDays, parseSeedTime, parseSize } from './parse';

// ── Display ───────────────────────────────────────────────────────────────

/** True when sizes should be relabelled to the binary units. */
export function binaryUnits(s: AppSettings = appSettings): boolean {
  return s?.size_units === 'binary';
}

/** A size string as it should read on screen. Under "as the tracker reports"
 *  the text is returned untouched; under "binary" a parseable size is
 *  rescaled and relabelled ("1500 GB" → "1.46 TiB"). Anything that is not a
 *  size — "—", a group name, empty — comes back as it went in, so this is
 *  safe on any value that might be one. */
export function displaySize(raw: string | null | undefined, s: AppSettings = appSettings): string {
  if (!raw) return raw ?? '';
  if (!binaryUnits(s)) return raw;
  const gib = parseSize(raw);
  if (gib === null) return raw;
  // parseSize drops the sign; a negative buffer keeps it here.
  return fmtBinary(/^\s*-/.test(raw) ? -Math.abs(gib) : gib);
}

/** GiB → the binary ladder, two decimals, sign kept (buffer can be negative). */
function fmtBinary(gib: number): string {
  const sign = gib < 0 ? '-' : '';
  const a = Math.abs(gib);
  if (a >= 1024 * 1024) return `${sign}${(a / (1024 * 1024)).toFixed(2)} PiB`;
  if (a >= 1024)        return `${sign}${(a / 1024).toFixed(2)} TiB`;
  if (a >= 1)           return `${sign}${a.toFixed(2)} GiB`;
  if (a >= 1 / 1024)    return `${sign}${(a * 1024).toFixed(2)} MiB`;
  if (a >= 1 / 1024 ** 2) return `${sign}${(a * 1024 * 1024).toFixed(2)} KiB`;
  return `${sign}${Math.round(a * 1024 ** 3)} B`;
}

// ── Input ─────────────────────────────────────────────────────────────────

/** The shapes a typed value can have. 'text' is anything (a group name). */
export type ValueShape = 'size' | 'duration' | 'number' | 'age' | 'text';

export type Normalized = { ok: true; value: string } | { ok: false; error: string };

/** The expected shape, in the words the server uses, so the inline message
 *  and a 400 read the same. */
export const SHAPE_HINT: Record<ValueShape, string> = {
  size:     'Enter a size like 1.5 TiB or 200 GB',
  duration: 'Enter a duration like 3M 6D (Y M W D h m s)',
  number:   'Enter a number',
  age:      'Enter days, or an age like 1Y 6M',
  text:     '',
};

/** Manual stats by shape (mirrors typedvalues.go). Anything not listed is
 *  free text — "group", and any stat the list has not caught up with. */
const MANUAL_SHAPES: Record<string, ValueShape> = {
  uploaded: 'size', downloaded: 'size', buffer: 'size', seed_size: 'size',
  real_uploaded: 'size', real_downloaded: 'size',
  avg_seed_time: 'duration', total_seedtime: 'duration',
  ratio: 'number', real_ratio: 'number', required_ratio: 'number', bonus_points: 'number',
  seeding: 'number', leeching: 'number', hit_and_runs: 'number', snatched: 'number',
  grabbed: 'number', upload_snatches: 'number', fl_tokens: 'number', invites: 'number',
  warnings: 'number', uploads_approved: 'number', adoptions: 'number',
  requests_filled: 'number', forum_posts: 'number',
};

/** Targets by shape (mirrors typedvalues.go). */
const TARGET_SHAPES: Record<string, ValueShape> = {
  uploaded: 'size', downloaded: 'size', seed_size: 'size', total_transfer: 'size',
  buffer: 'size', real_uploaded: 'size', real_downloaded: 'size',
  ratio: 'number', total_uploads: 'number', adoptions: 'number', bonus_points: 'number',
  snatched: 'number', monthly_uploads: 'number',
  avg_seed: 'duration', days: 'age',
};

export const manualShape = (key: string): ValueShape => MANUAL_SHAPES[key] ?? 'text';
export const targetShape = (key: string): ValueShape => TARGET_SHAPES[key] ?? 'text';

// A typed size: optional sign, a number (commas allowed), and a unit that may
// be a single letter — "200g", "1.5 tib", "3,000 GB". The unit is REQUIRED:
// a bare "200" is as likely to mean 200 GiB as 200 TiB, and guessing wrong is
// worse than asking.
const SIZE_INPUT_RE = /^(-?)(\d[\d,]*\.?\d*|\.\d+)\s*(?:([kmgtp])(i)?b?|(b))$/i;

/** "200g" → "200.00 GB", "1.5 tib" → "1.50 TiB". A bare letter keeps the
 *  decimal label as typed; an "i" asks for the binary one — the same number
 *  either way. */
export function normalizeSizeInput(raw: string): Normalized {
  const m = raw.trim().match(SIZE_INPUT_RE);
  if (!m) return { ok: false, error: SHAPE_HINT.size };
  const n = parseFloat(m[2].replace(/,/g, ''));
  if (isNaN(n)) return { ok: false, error: SHAPE_HINT.size };
  const unit = m[3] ? m[3].toUpperCase() + (m[4] ? 'iB' : 'B') : 'B';
  return { ok: true, value: `${m[1]}${n.toFixed(2)} ${unit}` };
}

// Long-form duration units → the letters parseSeedTime reads. The letters are
// case-sensitive (M months, m minutes); the words are not ambiguous.
const DURATION_WORDS: [RegExp, string][] = [
  [/(\d+(?:\.\d+)?)\s*(?:years?|yrs?)\b/gi, '$1Y'],
  [/(\d+(?:\.\d+)?)\s*(?:months?|mos?)\b/gi, '$1M'],
  [/(\d+(?:\.\d+)?)\s*(?:weeks?|wks?)\b/gi, '$1W'],
  [/(\d+(?:\.\d+)?)\s*(?:days?)\b/gi, '$1D'],
  [/(\d+(?:\.\d+)?)\s*(?:hours?|hrs?)\b/gi, '$1h'],
  [/(\d+(?:\.\d+)?)\s*(?:minutes?|mins?)\b/gi, '$1m'],
  [/(\d+(?:\.\d+)?)\s*(?:seconds?|secs?)\b/gi, '$1s'],
];
// What a duration must reduce to once the words are letters: number-letter
// pairs and nothing else. parseSeedTime alone reads "3 monkeys" as 3 months.
const DURATION_TOKENS_RE = /^(?:\d+(?:\.\d+)?\s*[YyMWwDdhms]\s*)+$/;

/** "3 months 6 days" / "3M 6D" / "90d" → the canonical "3M 6D"; a plain
 *  number is seconds, as the server stores durations. */
export function normalizeDurationInput(raw: string): Normalized {
  let s = raw.trim();
  if (!s) return { ok: false, error: SHAPE_HINT.duration };
  if (/^\d+(\.\d+)?$/.test(s)) {
    const secs = parseFloat(s);
    return secs > 0 ? { ok: true, value: fmtSeedTime(secs) } : { ok: false, error: SHAPE_HINT.duration };
  }
  for (const [re, to] of DURATION_WORDS) s = s.replace(re, to);
  if (!DURATION_TOKENS_RE.test(s)) return { ok: false, error: SHAPE_HINT.duration };
  const secs = parseSeedTime(s);
  if (secs === null || secs <= 0) return { ok: false, error: SHAPE_HINT.duration };
  return { ok: true, value: fmtSeedTime(secs) };
}

const NUMBER_INPUT_RE = /^-?(\d[\d,]*\.?\d*|\.\d+)$/;

/** A plain count or ratio, kept as typed. A ratio can be infinite — trackers
 *  print "∞" or "Inf" for an account that has never downloaded. */
export function normalizeNumberInput(raw: string): Normalized {
  const s = raw.trim();
  if (['∞', 'inf', 'infinity'].includes(s.toLowerCase())) return { ok: true, value: s };
  return NUMBER_INPUT_RE.test(s) ? { ok: true, value: s } : { ok: false, error: SHAPE_HINT.number };
}

/** An account age: plain days, or "1Y 6M 2W 3D", shown as the Y/M/W/D form
 *  and stored as days by the target collector. */
export function normalizeAgeInput(raw: string): Normalized {
  const s = raw.trim();
  if (!s) return { ok: false, error: SHAPE_HINT.age };
  if (/^\d+$/.test(s)) return { ok: true, value: s };
  let t = s;
  for (const [re, to] of DURATION_WORDS) t = t.replace(re, to);
  // Ages have no minutes, so a lowercase m can only mean months here.
  if (!/^(?:\d+\s*[YyMmWwDd]\s*)+$/.test(t)) return { ok: false, error: SHAPE_HINT.age };
  const days = parseAgeDays(t);
  return days != null && days > 0 ? { ok: true, value: t.toUpperCase() } : { ok: false, error: SHAPE_HINT.age };
}

/** Read one typed value against its shape. Text passes as trimmed. */
export function normalizeInput(shape: ValueShape, raw: string): Normalized {
  switch (shape) {
    case 'size':     return normalizeSizeInput(raw);
    case 'duration': return normalizeDurationInput(raw);
    case 'number':   return normalizeNumberInput(raw);
    case 'age':      return normalizeAgeInput(raw);
    default:         return { ok: true, value: raw.trim() };
  }
}
