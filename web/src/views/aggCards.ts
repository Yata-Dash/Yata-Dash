// views/aggCards.ts — aggregate stat cards at the top of both views
import type { AppSettings, HistoryPoint, ScrapeStatusMap, StatsMap, Tracker } from '../types';
import { numOf, strOf, scrapeStatus } from '../state';
import { fmtGib, fmtRatio, fmtSeedTime, fmtSignedDelta } from '../utils/format';
import type { DeltaUnit } from '../utils/format';
import { parseSize, parseSeedTime } from '../utils/parse';
import { renderSparkline } from '../components/sparkline';
import { buildAggSeries } from '../utils/history';

export function renderAggCards(
  trackers: Tracker[],
  statsCache: StatsMap,
  historyData: HistoryPoint[],
  settings: AppSettings,
): void {
  // Disabled trackers are hidden from the dashboard — their (stale, no longer
  // refreshed) stats shouldn't inflate totals or count as health issues.
  trackers = trackers.filter(t => t.enabled !== false);

  let totalUpGiB = 0, totalDownGiB = 0;
  let weightedSeedSec = 0, totalSeeding = 0;

  // STALE DATA RULE: totals sum whatever fields exist — a tracker whose last
  // fetch errored still contributes its stored stats instead of dropping out.
  trackers.forEach(t => {
    const s = statsCache[t.id];
    if (!s || !Object.keys(s.fields ?? {}).length) return;
    totalUpGiB   += parseSize(strOf(s, 'uploaded'))   ?? 0;
    totalDownGiB += parseSize(strOf(s, 'downloaded')) ?? 0;

    const ast   = parseSeedTime(strOf(s, 'avg_seed_time'));
    const seeds = numOf(s, 'seeding') ?? 0;
    if (ast !== null && seeds > 0) {
      weightedSeedSec += ast * seeds;
      totalSeeding    += seeds;
    }
  });

  const conn = connectionHealth(trackers, statsCache, scrapeStatus);

  const bufGiB     = totalUpGiB - totalDownGiB;
  const totalRatio = totalDownGiB > 0 ? totalUpGiB / totalDownGiB : 0;
  const agg        = buildAggSeries(historyData);
  const avgSeedTime = totalSeeding > 0 ? weightedSeedSec / totalSeeding : null;

  // 7-day change per card, off the same series buildAggSeries just built —
  // no separate fetch. null when the series is too thin to diff (a fresh
  // install, or a stat that hasn't recorded twice in the window yet).
  const deltaUp    = edgeDelta(agg.up);
  const deltaDown  = edgeDelta(agg.down);
  const deltaBuf   = edgeDelta(agg.buffer);
  // Ratio's delta + sparkline use the POOLED ratio (Σup/Σdown per bucket) —
  // the SAME quantity the big number shows — not buildAggSeries' mean-of-
  // per-tracker-ratios. The two disagree (a pooled 3.60 vs a mean that can
  // swing to 10 when one tracker reports), which made the chip read as a
  // nonsensical "+7.28" against a 3.60 value.
  const ratioTrend = pooledRatioSeries(agg.up, agg.down);
  const deltaRatio = edgeDelta(ratioTrend);
  const deltaSeed  = edgeDelta(agg.avgSeed);

  for (const pfx of ['g', 't']) {
    set(`${pfx}-agg-up`,    fmtGib(totalUpGiB));
    set(`${pfx}-agg-down`,  fmtGib(totalDownGiB));
    set(`${pfx}-agg-buf`,   fmtGib(bufGiB));
    set(`${pfx}-agg-ratio`, totalRatio > 0 ? fmtRatio(totalRatio) : '—');
    set(`${pfx}-agg-health-num`,   conn.tracked > 0 ? String(conn.reachable) : '—');
    set(`${pfx}-agg-health-denom`, conn.tracked > 0 ? `/ ${conn.tracked}` : '');
    set(`${pfx}-agg-health-sub`,   conn.summary);
    // Red is reserved for trackers nothing can reach. A dead API behind a
    // working scrape, or an expired cookie, is degraded rather than dark —
    // amber keeps red meaningful instead of training the eye to ignore it.
    document.getElementById(`${pfx}-health-card`)?.style
      .setProperty('--card-accent',
        conn.dark > 0 ? 'var(--red)' : conn.issues > 0 ? 'var(--amber)' : 'var(--green)');

    const astEl = document.getElementById(`${pfx}-agg-avg-seed`);
    if (astEl) astEl.textContent = avgSeedTime !== null ? fmtSeedTime(avgSeedTime) : '—';
    const astSubEl = document.getElementById(`${pfx}-agg-avg-seed-sub`);
    if (astSubEl) astSubEl.textContent = totalSeeding > 0 ? `across ${totalSeeding.toLocaleString()} seeds` : 'no data';

    // Downloaded rising is normal, not a win or a warning — always muted.
    // Everything else is signed: green when the change is the "good"
    // direction (more uploaded/seed time, buffer/ratio up), red otherwise.
    setDelta(`${pfx}-delta-up`,       deltaUp,    'GiB',     'signed');
    setDelta(`${pfx}-delta-down`,     deltaDown,  'GiB',     'muted');
    setDelta(`${pfx}-delta-buf`,      deltaBuf,   'GiB',     'signed');
    setDelta(`${pfx}-delta-ratio`,    deltaRatio, 'ratio',   'signed');
    setDelta(`${pfx}-delta-avg-seed`, deltaSeed,  'seconds', 'signed');
    // No delta chip on Health: "healthy tracker count" has no recorded
    // history series to diff — a delta here would be fabricated.

    renderSparkline(`${pfx}-spark-up`,       agg.up,     '--green');
    renderSparkline(`${pfx}-spark-down`,     agg.down,   '--purple');
    renderSparkline(`${pfx}-spark-buf`,      agg.buffer, '--blue');
    renderSparkline(`${pfx}-spark-ratio`,    ratioTrend, '--amber');
    // Fleet reachability per day over the last week — a real health series
    // now that contact outcomes are recorded, rather than the overall-ratio
    // stand-in this card used to borrow. Coloured by the CURRENT state so a
    // fleet that is down right now reads red even after a clean week.
    renderSparkline(`${pfx}-spark-health`,   conn.series,
      conn.dark > 0 ? '--red' : conn.issues > 0 ? '--amber' : '--green');
    renderSparkline(`${pfx}-spark-avg-seed`, agg.avgSeed, '--pink');
  }
}

function set(id: string, val: string): void {
  const el = document.getElementById(id);
  if (el) el.textContent = val;
}

/** What the Health card reports: whether Yata can REACH each tracker, not
 *  whether the numbers it brought back look good. Ratio and hit-and-run
 *  problems are stats quality — they're surfaced per-tracker (ratio colouring,
 *  the HnR highlight) and by alert rules, and conflating them with "the server
 *  is down" made the card unable to answer either question honestly. */
interface ConnectionHealth {
  reachable: number;   // trackers currently contactable on every channel
  tracked: number;     // trackers with any connection record at all
  issues: number;      // unreachable + API-failing + dead-cookie trackers
  dark: number;        // trackers nothing reaches at all — the red-vs-amber test
  summary: string;     // the card's sub-line
  series: number[];    // fleet uptime per day, oldest→newest (0..1)
}

function connectionHealth(
  trackers: Tracker[],
  statsCache: StatsMap,
  status: ScrapeStatusMap,
): ConnectionHealth {
  let reachable = 0, tracked = 0, down = 0, stale = 0, apiDown = 0;
  // Per-day sums across the fleet, so a day where two trackers were half-down
  // averages rather than letting whichever reported last win.
  //
  // These MUST stay dense. Assigning only on days that had contact leaves
  // holes for the rest, and a sparse array is quietly poisonous here: .map()
  // preserves holes and .every() skips them, so renderSparkline's
  // "nothing to draw" guard sees only the populated days, passes, and then
  // dereferences a missing point. That threw and aborted this whole function
  // — taking every other card's sparkline down with it — on any instance
  // whose history was younger than the strip (i.e. every instance, for its
  // first week).
  const width = trackers.reduce((n, t) => Math.max(n, (status[t.id]?.uptime ?? []).length), 0);
  const dayTotal = new Array<number>(width).fill(0);
  const dayCount = new Array<number>(width).fill(0);

  trackers.forEach(t => {
    const ss = status[t.id];
    const up = ss?.uptime ?? [];
    up.forEach((v, i) => {
      if (v < 0) return;                       // no contact that day — not an outage
      dayTotal[i] += v;
      dayCount[i] += 1;
    });

    // A tracker counts only once it has a connection record. Until then it is
    // neither reachable nor broken — showing "0 / 12" on a fresh install
    // would be alarming and wrong.
    const hasRecord = up.some(v => v >= 0);
    const cookieDead = ss?.cookie_expired === true;
    if (!hasRecord && !cookieDead) return;
    tracked++;

    // Both halves of "can Yata reach this tracker" count, not just whether
    // anything got through. An expired cookie breaks the scrape half; a dead
    // API breaks the other, and the scrape fallback quietly carrying a
    // tracker still means its stats are degraded. Counting only total
    // blackouts reported a tracker whose API had been failing for days as
    // perfectly healthy.
    if (ss?.unreachable) down++;
    else if (cookieDead) stale++;
    else if (ss?.api_down) apiDown++;
    else reachable++;
  });

  const issues = down + stale + apiDown;
  const parts: string[] = [];
  if (down)    parts.push(`${down} unreachable`);
  if (apiDown) parts.push(`${apiDown} API failing`);
  if (stale)   parts.push(`${stale} expired cookie${stale > 1 ? 's' : ''}`);

  const summary = tracked === 0
    ? (trackers.length ? 'awaiting first contact' : 'awaiting data')
    : parts.length ? parts.join(' · ')
    : 'all reachable';

  // Days nothing was contacted are skipped rather than plotted as 0 — a gap
  // in coverage is not a fleet-wide outage. Same idiom as pooledRatioSeries
  // above, and it keeps the result dense by construction.
  const series: number[] = [];
  for (let i = 0; i < width; i++) {
    if (dayCount[i] > 0) series.push(dayTotal[i] / dayCount[i]);
  }
  return { reachable, tracked, issues, dark: down, summary, series };
}

/** Pooled overall-ratio series (Σuploaded / Σdownloaded per bucket) — the
 *  same quantity the Overall Ratio card's big number shows, so its delta and
 *  sparkline stay consistent with it. Buckets with nothing downloaded yet are
 *  skipped so an early divide-by-zero can't distort the trend. */
function pooledRatioSeries(up: number[], down: number[]): number[] {
  const out: number[] = [];
  for (let i = 0; i < up.length; i++) {
    if (down[i] > 0) out.push(up[i] / down[i]);
  }
  return out;
}

/** Last − first of an aggregate series, or null when there aren't at least
 *  two points to diff (mirrors the Detail delta-chip's "no chip when
 *  nothing moved" rule — see statDeltaChip in views/detail.ts). */
function edgeDelta(series: number[]): number | null {
  if (series.length < 2) return null;
  return series[series.length - 1] - series[0];
}

/** Renders one card's 7-day delta chip, or clears it (via :empty CSS the
 *  chip then takes no layout space) when there's nothing worth showing:
 *  no delta, or a delta that rounds away to nothing at display precision —
 *  a brand-new install should show clean cards, not a wall of "+0". */
function setDelta(id: string, dv: number | null, unit: DeltaUnit, tone: 'signed' | 'muted'): void {
  const el = document.getElementById(id);
  if (!el) return;
  if (dv === null) { el.textContent = ''; return; }
  const text = fmtSignedDelta(unit, dv);
  if (roundsToZero(text)) { el.textContent = ''; return; }
  el.textContent = `${text} · 7d`;
  el.className = `agg-delta ${tone === 'muted' ? 'agg-delta--muted' : dv > 0 ? 'agg-delta--up' : 'agg-delta--down'}`;
}

/** True when a formatted signed delta's leading number is zero regardless of
 *  unit suffix ("+0.00", "-0 KiB", "+0D") — the display-precision analogue of
 *  the Detail chip's raw `!dv` check. */
function roundsToZero(formatted: string): boolean {
  const n = formatted.replace(/^[+-]/, '').match(/^[\d.]+/);
  return !!n && parseFloat(n[0]) === 0;
}
