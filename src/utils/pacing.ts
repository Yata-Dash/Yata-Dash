// utils/pacing.ts — goal pacing: the rate NEEDED (remaining / days left) vs
// the rate the tracker HAS (stats.rates), an on-track/behind verdict, the
// ratio needed-upload figure, and the smart default deadline. Pure functions
// only, so future tests can target them directly — mirrors
// internal/api/pacing.go; parity of MEANING, not pixels. No TS test harness
// exists (see the plan) — correctness rides on the Go twin + live
// verification.
import type { StatField } from '../types';
import { parseRatio } from './format';
import { memberDays, parseAgeDays, parseSeedTime, parseSize } from './parse';

export type PacingState = 'done' | 'overdue' | 'on_track' | 'behind' | 'no_rate' | 'ratio_info';

export interface Pacing {
  state: PacingState;
  daysLeft: number;  // ceil(deadline - today); can be <= 0 when overdue
  required: number;  // remaining / days-left, in the row's own unit; 0 for done/ratio_info
  rate: number;       // the existing per-day growth rate; 0 when hasRate is false
  hasRate: boolean;
  /** ratio_info only — extra upload (GiB) needed to hit the target ratio;
   *  0 when already there (never negative). */
  neededUploadGiB: number;
}

/** Target key -> the stats.rates key that projects it (mirrors goalRateKey
 *  in internal/api/pacing.go). Keys the rates map never populates (ratio,
 *  avg_seed, days, most custom counts) pass through unchanged — simply
 *  absent from rates, degrading to "no_rate" like any other key. */
function goalRateKey(key: string): string {
  return key === 'total_uploads' ? 'uploads_approved' : key;
}

function fieldStr(fields: Record<string, StatField> | undefined, key: string): string {
  const v = fields?.[key]?.value;
  return v == null ? '' : String(v);
}

function cleanFloat(raw: string): number | null {
  const s = raw.replace(/,/g, '').trim();
  if (!s) return null;
  const n = parseFloat(s);
  return isNaN(n) ? null : n;
}

function sizeRemaining(curRaw: string, tgtRaw: string): number | null {
  const tgt = parseSize(tgtRaw);
  if (tgt == null || !(tgt > 0)) return null;
  const cur = parseSize(curRaw);
  if (cur == null) return null;
  return tgt - cur;
}

function numRemaining(curRaw: string, tgtRaw: string): number | null {
  const tgt = cleanFloat(tgtRaw);
  if (tgt == null || !(tgt > 0)) return null;
  const cur = cleanFloat(curRaw);
  if (cur == null) return null;
  return tgt - cur;
}

/** seedTimeRemaining is numRemaining's seconds counterpart, converted to
 *  days so avg_seed's "required" figure stays a plain per-day count like
 *  every other row (it's never rate-projected — see goalRateKey — but a
 *  no_rate row still shows a needed-per-day figure). */
function seedTimeRemaining(curRaw: string, tgtRaw: string): number | null {
  const tgt = parseSeedTime(tgtRaw);
  if (tgt == null || !(tgt > 0)) return null;
  const cur = parseSeedTime(curRaw);
  if (cur == null) return null;
  return (tgt - cur) / 86400;
}

/** customRemaining handles a tracker-specific target key: compare as a size
 *  when BOTH sides parse as one, otherwise fall back to plain numeric
 *  comparison for both — mirrors internal/api/pacing.go's customRemaining. */
function customRemaining(curRaw: string, tgtRaw: string): number | null {
  let curV = parseSize(curRaw), tgtV = parseSize(tgtRaw);
  if (curV == null || tgtV == null) {
    curV = cleanFloat(curRaw);
    tgtV = cleanFloat(tgtRaw);
  }
  if (tgtV == null || !(tgtV > 0) || curV == null) return null;
  return tgtV - curV;
}

function goalRemaining(key: string, targetRaw: string, fields: Record<string, StatField> | undefined): number | null {
  switch (key) {
    case 'uploaded': case 'downloaded': case 'seed_size':
      return sizeRemaining(fieldStr(fields, key), targetRaw);
    case 'total_uploads':
      return numRemaining(fieldStr(fields, 'uploads_approved'), targetRaw);
    case 'avg_seed':
      return seedTimeRemaining(fieldStr(fields, 'avg_seed_time'), targetRaw);
    case 'bonus_points': case 'adoptions': case 'snatched':
      return numRemaining(fieldStr(fields, key), targetRaw);
    default:
      return customRemaining(fieldStr(fields, key), targetRaw);
  }
}

const RATIO_INFO_ZERO: Pacing = { state: 'ratio_info', daysLeft: 0, required: 0, rate: 0, hasRate: false, neededUploadGiB: 0 };

/** ratioPacing computes a ratio row's info-only pacing: the extra upload
 *  (GiB) needed to reach the target ratio, populated only when positive.
 *  There's no honest ratio "rate" to pace against, so no verdict here. */
function ratioPacing(targetRaw: string, fields: Record<string, StatField> | undefined): Pacing {
  const tgt = parseRatio(targetRaw);
  if (isNaN(tgt) || !(tgt > 0) || !isFinite(tgt)) return { ...RATIO_INFO_ZERO };
  const downloaded = parseSize(fieldStr(fields, 'downloaded'));
  const uploaded = parseSize(fieldStr(fields, 'uploaded'));
  if (downloaded == null || uploaded == null) return { ...RATIO_INFO_ZERO };
  const needed = downloaded * tgt - uploaded;
  return { ...RATIO_INFO_ZERO, neededUploadGiB: needed > 0 ? needed : 0 };
}

/** daysUntil is ceil(deadline - today) in whole days, both truncated to UTC
 *  midnight so it agrees with the backend regardless of the viewer's time
 *  zone. NaN when the deadline string doesn't parse. */
function daysUntil(deadlineRaw: string, now: Date): number {
  const deadline = new Date(`${deadlineRaw}T00:00:00Z`);
  if (isNaN(deadline.getTime())) return NaN;
  const todayUTC = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
  return Math.ceil((deadline.getTime() - todayUTC) / 86400000);
}

/**
 * computeGoalPacing computes one dated row's pacing, or null when the row
 * isn't valid: no/unparseable deadline, no target value, or key is "days"
 * (account age never takes a deadline — the editors never offer one, and
 * the backend drops it on save if it ever arrives).
 */
export function computeGoalPacing(
  key: string,
  targetRaw: string,
  deadlineRaw: string,
  fields: Record<string, StatField> | undefined,
  rates: Record<string, number> | undefined,
  now: Date = new Date(),
): Pacing | null {
  if (key === 'days' || !targetRaw || !deadlineRaw) return null;
  const daysLeft = daysUntil(deadlineRaw, now);
  if (isNaN(daysLeft)) return null;

  // Ratio rows are a wholly separate, verdict-less branch — they never reach
  // the remaining/days-left math below.
  if (key === 'ratio') return ratioPacing(targetRaw, fields);

  const remaining = goalRemaining(key, targetRaw, fields);
  if (remaining == null) return null;

  if (remaining <= 0) return { state: 'done', daysLeft, required: 0, rate: 0, hasRate: false, neededUploadGiB: 0 };
  if (daysLeft <= 0) return { state: 'overdue', daysLeft, required: 0, rate: 0, hasRate: false, neededUploadGiB: 0 };

  const required = remaining / daysLeft;
  const rate = rates?.[goalRateKey(key)];
  const hasRate = rate != null && !isNaN(rate);
  const state: PacingState = hasRate && (rate as number) >= required ? 'on_track' : hasRate ? 'behind' : 'no_rate';
  return { state, daysLeft, required, rate: hasRate ? (rate as number) : 0, hasRate, neededUploadGiB: 0 };
}

/**
 * defaultGoalDeadline is the smart default when the user first sets a date
 * on a row: today + max(remaining days on an unmet account-age target, 30)
 * — the common goal is "beat my age requirement before it completes on its
 * own". Falls back to today + 30 when there's no age target, it's already
 * met, or it doesn't parse. Never returns a date under 30 days out.
 */
export function defaultGoalDeadline(
  ageTargetRaw: string | undefined,
  joinDate: string | undefined,
  now: Date = new Date(),
): string {
  let extraDays = 0;
  if (ageTargetRaw && joinDate) {
    const tgtDays = parseAgeDays(ageTargetRaw);
    const curDays = memberDays(joinDate);
    if (tgtDays != null && curDays != null) {
      const remain = tgtDays - curDays;
      if (remain > 0) extraDays = remain;
    }
  }
  const days = Math.max(extraDays, 30);
  const d = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}
