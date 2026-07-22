// utils/series.ts — pure helpers for the History view (HISTORY_VIEW_PLAN.md).
// Range/metric metadata, unit-aware value formatting, axis tick math, and
// point lookups. No DOM, no fetch — everything here is unit-testable.
import { fmtEtaDays, fmtSeedTime } from './format';
import { findGroupDef, groupRequirementsToTargets } from './group';
import { parseSize, parseSeedTime } from './parse';
import type { HistorySeries, Tracker, TrackerGroupMap } from '../types';

// ── Ranges ──────────────────────────────────────────────────────────────────

/** Range keys accepted by GET /api/history/series, in display order. */
export const HISTORY_RANGES = [
  { key: '48h',  label: '48h' },
  { key: '7d',   label: '7d' },
  { key: '14d',  label: '14d' },
  { key: '30d',  label: '30d' },
  { key: '90d',  label: '90d' },
  { key: '365d', label: '1y' },
  { key: 'all',  label: 'All' },
] as const;

export type HistoryRangeKey = typeof HISTORY_RANGES[number]['key'];

// ── Metrics ─────────────────────────────────────────────────────────────────

/** The recorded numeric fields, in picker order (stats.RecordHistory). */
export const HISTORY_METRICS: { key: string; label: string; unit: SeriesUnit }[] = [
  { key: 'uploaded',         label: 'Uploaded',      unit: 'GiB' },
  { key: 'downloaded',       label: 'Downloaded',    unit: 'GiB' },
  { key: 'buffer',           label: 'Buffer',        unit: 'GiB' },
  { key: 'seed_size',        label: 'Seed Size',     unit: 'GiB' },
  { key: 'ratio',            label: 'Ratio',         unit: 'ratio' },
  { key: 'bonus_points',     label: 'Bonus Points',  unit: 'count' },
  { key: 'seeding',          label: 'Seeding',       unit: 'count' },
  { key: 'leeching',         label: 'Leeching',      unit: 'count' },
  { key: 'uploads_approved', label: 'Uploads',       unit: 'count' },
  { key: 'hit_and_runs',     label: 'Hit & Runs',    unit: 'count' },
  { key: 'avg_seed_time',    label: 'Avg Seed Time', unit: 'seconds' },
];

export type SeriesUnit = 'GiB' | 'count' | 'ratio' | 'seconds';

export function metricLabel(key: string): string {
  return HISTORY_METRICS.find(m => m.key === key)?.label ?? key;
}

export function metricUnit(key: string): SeriesUnit {
  return HISTORY_METRICS.find(m => m.key === key)?.unit ?? 'count';
}

// ── Target reference lines ──────────────────────────────────────────────────

/** Which targets-map keys speak about a given history metric. */
export const TARGET_KEYS_FOR_METRIC: Record<string, string[]> = {
  uploads_approved: ['total_uploads'],
  avg_seed_time: ['avg_seed'],
};

/** One tracker's target thresholds for a metric, as chart reference lines —
 *  shared by the History view and the Tracker Detail mini-charts. Resolved
 *  from the SAME sources the dashboard TARGETS bars use, so every surface
 *  agrees:
 *   1. the stored targets map (base requirements — manual targets, OR a chosen
 *      group's requirements, which the edit modal materialises into this map),
 *   2. the target group's `min_counts` (live from the def — e.g. HUNO's
 *      seed-time brackets, never stored in the targets map),
 *   3. the target group's `any_of` alternatives (live from the def).
 *  Group targets are the important case, so base requirements are read from
 *  `tracker.targets` directly and NEVER re-derived (an earlier version wrongly
 *  overwrote them, dropping group targets whose def lookup didn't line up). */
export function targetRefLinesFor(
  t: Tracker,
  metric: string,
  groupDefs: TrackerGroupMap | undefined,
): { value: number; label: string }[] {
  const unit = metricUnit(metric);
  const keys = TARGET_KEYS_FOR_METRIC[metric] ?? [metric];

  const parseTarget = (raw: string): number | null => {
    if (unit === 'GiB') return parseSize(raw);
    if (unit === 'seconds') return parseSeedTime(raw);
    const n = parseFloat(raw);
    return isNaN(n) ? null : n;
  };
  const values: number[] = [];
  const addFromMap = (map: Record<string, string>) => {
    for (const key of keys) {
      const raw = map[key];
      if (!raw) continue;
      const v = parseTarget(raw);
      if (v != null && v > 0) values.push(v);
    }
  };

  // 1. Base requirements (manual or materialised-group) — the dashboard's
  //    primary source. Never overwrite it.
  addFromMap(t.targets ?? {});

  // 2 & 3. Live extras from the target group's def.
  const g = t.target_group && t.def_key && groupDefs
    ? findGroupDef(groupDefs, t.def_key, t.target_group) : undefined;
  if (g) {
    for (const mc of g.requirements.min_counts ?? []) {
      if (mc.count > 0 && keys.includes(mc.field)) values.push(mc.count);
    }
    for (const alt of g.requirements.any_of ?? []) addFromMap(groupRequirementsToTargets(alt));
  }

  // De-dupe overlapping thresholds; draw lowest→highest. Durations use the
  // day/month/year duration setting so the target label matches the axis ticks.
  const fmt = (v: number) => unit === 'seconds' ? fmtEtaDays(v / 86400) : fmtUnitValue(unit, v);
  return [...new Set(values)].sort((a, b) => a - b)
    .map(v => ({ value: v, label: `Target ${fmt(v)}` }));
}

// ── Value formatting ────────────────────────────────────────────────────────

/** Full-precision readout formatting ("8.61 TiB", "1,713", "3.27", "42D 7h"). */
export function fmtUnitValue(unit: SeriesUnit, v: number): string {
  switch (unit) {
    case 'GiB':     return fmtGiB(v, 2);
    case 'ratio':   return v.toFixed(2);
    case 'seconds': return fmtSeedTime(v);
    default:        return Math.round(v).toLocaleString();
  }
}

/** Compact axis-tick formatting ("8.6T", "1.7k", "3.3", "40D"). */
export function fmtAxisValue(unit: SeriesUnit, v: number): string {
  switch (unit) {
    case 'GiB': {
      const a = Math.abs(v);
      if (v === 0)          return '0';
      if (a >= 1024 * 1024) return trim1(v / (1024 * 1024)) + 'P';
      if (a >= 1024)        return trim1(v / 1024) + 'T';
      if (a >= 1)           return trim1(v) + 'G';
      return trim1(v * 1024) + 'M';
    }
    case 'ratio':   return trim1(v);
    case 'seconds': {
      const a = Math.abs(v);
      if (a >= 31536000) return trim1(v / 31536000) + 'y';
      if (a >= 86400)    return trim1(v / 86400) + 'd';
      if (a >= 3600)     return trim1(v / 3600) + 'h';
      return trim1(v / 60) + 'm';
    }
    default: {
      const a = Math.abs(v);
      if (a >= 1e6) return trim1(v / 1e6) + 'M';
      if (a >= 1e3) return trim1(v / 1e3) + 'k';
      return trim1(v);
    }
  }
}

/** GiB float → human size string (sign-preserving; buffer can be negative). */
export function fmtGiB(gib: number, dp = 2): string {
  const sign = gib < 0 ? '-' : '';
  const a = Math.abs(gib);
  if (a >= 1024 * 1024) return `${sign}${(a / (1024 * 1024)).toFixed(dp)} PiB`;
  if (a >= 1024)        return `${sign}${(a / 1024).toFixed(dp)} TiB`;
  return `${sign}${a.toFixed(dp)} GiB`;
}

function trim1(v: number): string {
  const s = v.toFixed(1);
  return s.endsWith('.0') ? s.slice(0, -2) : s;
}

// ── Axis tick math ──────────────────────────────────────────────────────────

/** "Nice" value ticks covering [min,max] — 1/2/5×10ⁿ steps, ~want ticks.
 *  `minStep` floors the step (pass 1 for integer metrics so a narrow domain
 *  never yields impossible ticks like 14.5 uploads). */
export function niceTicks(min: number, max: number, want = 5, minStep = 0): number[] {
  if (!isFinite(min) || !isFinite(max)) return [];
  if (min === max) { min -= 1; max += 1; }
  const span = max - min;
  const rawStep = span / Math.max(1, want);
  const mag = Math.pow(10, Math.floor(Math.log10(rawStep)));
  let step = mag;
  for (const m of [1, 2, 5, 10]) {
    if (rawStep <= m * mag) { step = m * mag; break; }
  }
  if (minStep > 0 && step < minStep) step = minStep;
  const first = Math.ceil(min / step) * step;
  const out: number[] = [];
  // Epsilon guards float drift so the top tick isn't dropped.
  for (let v = first; v <= max + step * 1e-6; v += step) out.push(Math.abs(v) < step * 1e-9 ? 0 : v);
  return out;
}

/** Time ticks across [from,to] (unix sec) at day/week/month-ish spacing. */
export function timeTicks(from: number, to: number, want = 6): number[] {
  const span = Math.max(1, to - from);
  const steps = [
    3600, 2 * 3600, 6 * 3600, 12 * 3600,          // hours
    86400, 2 * 86400, 7 * 86400, 14 * 86400,      // days/weeks
    30 * 86400, 61 * 86400, 91 * 86400, 182 * 86400, 365 * 86400,
  ];
  let step = steps[steps.length - 1];
  for (const s of steps) {
    if (span / s <= want) { step = s; break; }
  }
  const first = Math.ceil(from / step) * step;
  const out: number[] = [];
  for (let t = first; t <= to; t += step) out.push(t);
  return out;
}

/** Tick label appropriate to the total span: "14:00" · "Jun 3" · "Jun '25". */
export function fmtTimeTick(t: number, spanSec: number): string {
  const d = new Date(t * 1000);
  if (spanSec <= 2 * 86400) {
    return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  }
  if (spanSec <= 200 * 86400) {
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }
  // "Jun '25" — the apostrophe keeps a 2-digit year from reading as a day.
  return `${d.toLocaleDateString(undefined, { month: 'short' })} '${String(d.getFullYear()).slice(-2)}`;
}

/** Readout timestamp: full date, plus time when the range is intraday-fine. */
export function fmtPointTime(t: number, spanSec: number): string {
  const d = new Date(t * 1000);
  const date = d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
  if (spanSec <= 15 * 86400) {
    return `${date} ${d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}`;
  }
  return date;
}

// ── Point lookups ───────────────────────────────────────────────────────────

/** Index of the point nearest to time t (binary search; points sorted). */
export function nearestIndex(points: [number, number][], t: number): number {
  if (!points.length) return -1;
  let lo = 0, hi = points.length - 1;
  while (hi - lo > 1) {
    const mid = (lo + hi) >> 1;
    if (points[mid][0] < t) lo = mid; else hi = mid;
  }
  return Math.abs(points[lo][0] - t) <= Math.abs(points[hi][0] - t) ? lo : hi;
}

/** Value at (nearest to) time t, or null when the series has no points. */
export function valueAt(points: [number, number][], t: number): number | null {
  const i = nearestIndex(points, t);
  return i < 0 ? null : points[i][1];
}

/** Two-point delta for the pin readout: change, span, per-day rate. */
export function deltaStats(points: [number, number][], t1: number, t2: number):
  { from: number; to: number; dv: number; days: number; perDay: number } | null {
  const i1 = nearestIndex(points, Math.min(t1, t2));
  const i2 = nearestIndex(points, Math.max(t1, t2));
  if (i1 < 0 || i2 < 0 || i1 === i2) return null;
  const [ta, va] = points[i1];
  const [tb, vb] = points[i2];
  const days = (tb - ta) / 86400;
  if (days <= 0) return null;
  return { from: va, to: vb, dv: vb - va, days, perDay: (vb - va) / days };
}

// ── Series transforms (History phase 4) ─────────────────────────────────────

/** True when summing this metric across trackers is meaningful (portfolio
 *  line). Ratio and average seed time are not sums. */
export function isSummableUnit(unit: SeriesUnit): boolean {
  return unit === 'GiB' || unit === 'count';
}

/** Cumulative points → per-day rate points: each point becomes the per-day
 *  delta from its predecessor, stamped at the newer time. First point drops
 *  (no predecessor). */
export function toRateSeries(points: [number, number][]): [number, number][] {
  const out: [number, number][] = [];
  for (let i = 1; i < points.length; i++) {
    const dtDays = (points[i][0] - points[i - 1][0]) / 86400;
    if (dtDays <= 0) continue;
    out.push([points[i][0], (points[i][1] - points[i - 1][1]) / dtDays]);
  }
  return out;
}

/** Signed per-day rate from a series' recent points — the client-side
 *  complement to the backend's GrowthRates (which omits flat/declining fields
 *  because the dashboard only projects ETAs for growth). The History
 *  projection wants the true recent slope whatever its sign, so it can draw
 *  flat and downward tails too. Looks at the last `windowDays` of data
 *  (mirroring the backend's stable-rate window); needs ≥2 points spanning
 *  ≥3 h. Returns null when the series can't support a rate. */
export function recentRatePerDay(points: [number, number][], windowDays = 14): number | null {
  if (points.length < 2) return null;
  const last = points[points.length - 1];
  const cutoff = last[0] - windowDays * 86400;
  let first = points[0];
  for (const p of points) {
    if (p[0] >= cutoff) { first = p; break; }
  }
  // Degenerate window (e.g. only the last point is inside) → widen to all.
  if (first[0] >= last[0]) first = points[0];
  const spanSec = last[0] - first[0];
  if (spanSec < 3 * 3600) return null;
  return (last[1] - first[1]) / (spanSec / 86400);
}

/** Sum several series into one "portfolio" line. At each distinct timestamp
 *  every series contributes its last-known value (carry-forward); a series
 *  joins the sum from its first point on — so a young tracker appearing
 *  mid-window shows as the real step-up in recorded portfolio it is. */
export function portfolioSeries(lists: [number, number][][]): [number, number][] {
  const times = [...new Set(lists.flatMap(ps => ps.map(p => p[0])))].sort((a, b) => a - b);
  const idx = lists.map(() => 0);
  const last = lists.map(() => null as number | null);
  const out: [number, number][] = [];
  for (const t of times) {
    let sum = 0, any = false;
    lists.forEach((ps, i) => {
      while (idx[i] < ps.length && ps[idx[i]][0] <= t) { last[i] = ps[idx[i]][1]; idx[i]++; }
      if (last[i] != null) { sum += last[i] as number; any = true; }
    });
    if (any) out.push([t, sum]);
  }
  return out;
}

/** Moving-average smoothing for noisy metrics (ratio/seeding). Keeps each
 *  point's timestamp, averages values over a window sized to the series length
 *  (odd, ≥3). Short series pass through unchanged. Used for the drawn line
 *  only — the readout stays on raw values. */
export function smoothSeries(points: [number, number][], frac = 0.06): [number, number][] {
  const n = points.length;
  if (n < 5) return points;
  let w = Math.max(3, Math.round(n * frac));
  if (w % 2 === 0) w++;
  const half = (w - 1) / 2;
  const out: [number, number][] = [];
  for (let i = 0; i < n; i++) {
    let sum = 0, cnt = 0;
    for (let j = i - half; j <= i + half; j++) {
      if (j >= 0 && j < n) { sum += points[j][1]; cnt++; }
    }
    out.push([points[i][0], sum / cnt]);
  }
  return out;
}

/** Milestone thresholds per unit — "nice round" achievement levels. */
const MILESTONES: Record<SeriesUnit, number[]> = {
  GiB: [512, 1024, 2048, 5120, 10240, 25600, 51200, 102400, 256000, 512000, 1048576], // 0.5 TiB … 1 PiB
  ratio: [1, 2, 3, 5, 10, 20, 50],
  count: [100, 500, 1000, 5000, 10000, 50000, 100000, 500000, 1000000, 5000000, 10000000],
  seconds: [], // duration milestones aren't meaningful
};

/** Compact milestone label (e.g. "10 TiB", "1M", "2"). */
function milestoneLabel(unit: SeriesUnit, v: number): string {
  if (unit === 'GiB') return fmtGiB(v, 0);
  if (unit === 'ratio') return String(v);
  return fmtAxisValue('count', v);
}

/** New-high milestones within the window: the first time the series reaches a
 *  round threshold it hadn't reached before (points oldest→newest). Uses a
 *  running maximum seeded by the first point, so:
 *   - a level already achieved when the window opens isn't re-marked, and
 *   - a transient dip that recovers (data glitch / removed-then-readded
 *     torrents) doesn't fire a false milestone when it re-crosses on the way
 *     back up — only genuine new peaks count. */
export function milestonesFor(points: [number, number][], unit: SeriesUnit): { at: number; value: number; label: string }[] {
  const out: { at: number; value: number; label: string }[] = [];
  const thresholds = MILESTONES[unit] ?? [];
  if (points.length < 2 || !thresholds.length) return out;
  let runningMax = points[0][1];
  const start = points[0][1];
  for (let i = 1; i < points.length; i++) {
    const v = points[i][1];
    if (v <= runningMax) continue;
    for (const th of thresholds) {
      if (th > start && th > runningMax && th <= v) {
        out.push({ at: points[i][0], value: th, label: milestoneLabel(unit, th) });
      }
    }
    runningMax = v;
  }
  return out;
}

/** Overall [min,max] across series (for the shared y domain). */
export function seriesExtent(series: HistorySeries[]): [number, number] {
  let min = Infinity, max = -Infinity;
  for (const s of series) {
    for (const [, v] of s.points) {
      if (v < min) min = v;
      if (v > max) max = v;
    }
  }
  return [min, max];
}
