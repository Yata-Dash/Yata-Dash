// utils/history.ts — groups long-format history points into sparkline series
// Points originate from GET /api/history/series (flattened back into the
// long-format {tracker_id, recorded_at, field, value} shape by main.ts'
// loadHistory, oldest first). These helpers turn them into per-tracker and
// aggregate series for sparklines and the aggregate cards.
import type { HistoryPoint } from '../types';

/** Series of values for one tracker + field, ordered oldest → newest. */
export function trackerSeries(points: HistoryPoint[], trackerId: string, field: string): number[] {
  const out: number[] = [];
  for (const p of points) {
    if (p.tracker_id === trackerId && p.field === field) out.push(p.value);
  }
  return out;
}

/** Bucket width for aggregate series (seconds). */
const BUCKET_SEC = 1800;

export interface AggSeries {
  up: number[];      // sum of uploaded (GiB)
  down: number[];    // sum of downloaded (GiB)
  buffer: number[];  // up − down
  ratio: number[];   // average ratio
  avgSeed: number[]; // average avg_seed_time (sec)
}

/**
 * Build aggregate series across all trackers on a shared time axis.
 * Points are bucketed to 30-min slots; each tracker's last known value is
 * carried forward so trackers that record at different times still sum
 * correctly.
 */
export function buildAggSeries(points: HistoryPoint[]): AggSeries {
  const empty: AggSeries = { up: [], down: [], buffer: [], ratio: [], avgSeed: [] };
  if (!points.length) return empty;

  // Shared bucket axis across all fields we aggregate. 'buffer' is optional —
  // included when present so it doesn't shift the axis for older data that
  // never recorded it (derived below instead).
  const FIELDS = ['uploaded', 'downloaded', 'buffer', 'ratio', 'avg_seed_time'];
  const bucketSet = new Set<number>();
  for (const p of points) {
    if (FIELDS.includes(p.field)) bucketSet.add(Math.floor(p.recorded_at / BUCKET_SEC));
  }
  const buckets = [...bucketSet].sort((a, b) => a - b);
  if (!buckets.length) return empty;

  const sumSeries = (field: string): number[] => combine(points, field, buckets, 'sum');
  const avgSeries = (field: string): number[] => combine(points, field, buckets, 'avg');

  const up   = sumSeries('uploaded');
  const down = sumSeries('downloaded');
  // Prefer recorded buffer points (accounts for tracker-reported buffer that
  // isn't a pure up−down derivation); fall back to deriving it for older data
  // recorded before buffer was tracked directly.
  const hasBuffer = points.some(p => p.field === 'buffer');
  const buffer = hasBuffer ? sumSeries('buffer') : up.map((v, i) => v - (down[i] ?? 0));
  return {
    up, down, buffer,
    ratio:   avgSeries('ratio'),
    avgSeed: avgSeries('avg_seed_time'),
  };
}

/**
 * Window change of a summed field: Σ over trackers of (last − first), each
 * tracker diffed against ITSELF. Null when no tracker has two points to diff.
 *
 * Diffing the ends of the summed series instead (what edgeDelta does to the
 * buildAggSeries output) is wrong whenever the set of trackers changes inside
 * the window: a tracker whose first point falls mid-window is absent from the
 * first bucket and present in the last, so its whole lifetime total lands in
 * the "7d" chip — add a tracker with 40 TiB uploaded and the card claims
 * "+40 TiB · 7d". Per tracker, a newcomer contributes only what it moved
 * after Yata first recorded it.
 */
export function fieldDelta(points: HistoryPoint[], field: string): number | null {
  const ends = new Map<string, { t0: number; v0: number; t1: number; v1: number; n: number }>();
  for (const p of points) {
    if (p.field !== field) continue;
    const e = ends.get(p.tracker_id);
    if (!e) { ends.set(p.tracker_id, { t0: p.recorded_at, v0: p.value, t1: p.recorded_at, v1: p.value, n: 1 }); continue; }
    e.n++;
    // Input is oldest-first, but compare times rather than trust the order.
    if (p.recorded_at < e.t0) { e.t0 = p.recorded_at; e.v0 = p.value; }
    if (p.recorded_at >= e.t1) { e.t1 = p.recorded_at; e.v1 = p.value; }
  }
  let sum = 0, any = false;
  for (const e of ends.values()) {
    if (e.n < 2) continue;
    sum += e.v1 - e.v0;
    any = true;
  }
  return any ? sum : null;
}

/** Window changes for the summed aggregate cards, per tracker (see
 *  fieldDelta). Buffer follows buildAggSeries' rule: recorded buffer points
 *  when there are any, otherwise derived as uploaded − downloaded. */
export function aggDeltas(points: HistoryPoint[]): { up: number | null; down: number | null; buffer: number | null } {
  const up = fieldDelta(points, 'uploaded');
  const down = fieldDelta(points, 'downloaded');
  const buffer = points.some(p => p.field === 'buffer')
    ? fieldDelta(points, 'buffer')
    : (up === null && down === null ? null : (up ?? 0) - (down ?? 0));
  return { up, down, buffer };
}

/** Carry-forward combine of one field across trackers over shared buckets. */
function combine(points: HistoryPoint[], field: string, buckets: number[], mode: 'sum' | 'avg'): number[] {
  // Per-tracker chronological points for this field (input is oldest first).
  const byTracker = new Map<string, HistoryPoint[]>();
  for (const p of points) {
    if (p.field !== field) continue;
    let arr = byTracker.get(p.tracker_id);
    if (!arr) { arr = []; byTracker.set(p.tracker_id, arr); }
    arr.push(p);
  }

  const cursors = new Map<string, number>();           // per-tracker read index
  const lastVal = new Map<string, number>();           // carry-forward value
  const out: number[] = [];

  for (const bucket of buckets) {
    for (const [id, arr] of byTracker) {
      let i = cursors.get(id) ?? 0;
      while (i < arr.length && Math.floor(arr[i].recorded_at / BUCKET_SEC) <= bucket) {
        lastVal.set(id, arr[i].value);
        i++;
      }
      cursors.set(id, i);
    }
    let sum = 0, n = 0;
    for (const v of lastVal.values()) { sum += v; n++; }
    out.push(n === 0 ? 0 : (mode === 'sum' ? sum : sum / n));
  }
  return out;
}
