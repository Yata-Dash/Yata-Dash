// views/aggCards.ts — aggregate stat cards at the top of both views
import type { AppSettings, HistoryPoint, StatsMap, Tracker } from '../types';
import { numOf, strOf } from '../state';
import { fmtGib, fmtRatio, fmtSeedTime, fmtSignedDelta, parseRatio } from '../utils/format';
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

  let totalUpGiB = 0, totalDownGiB = 0, healthyCount = 0, issueCount = 0;
  let weightedSeedSec = 0, totalSeeding = 0;

  // STALE DATA RULE: totals sum whatever fields exist — a tracker whose last
  // fetch errored still contributes its stored stats instead of dropping out.
  trackers.forEach(t => {
    const s = statsCache[t.id];
    if (!s || !Object.keys(s.fields ?? {}).length) return;
    totalUpGiB   += parseSize(strOf(s, 'uploaded'))   ?? 0;
    totalDownGiB += parseSize(strOf(s, 'downloaded')) ?? 0;
    const ratio = parseRatio(strOf(s, 'ratio')); // ∞ → Infinity, counts as healthy
    const hnr   = numOf(s, 'hit_and_runs') ?? 0;
    if (ratio >= 1 && hnr === 0 && s.ok) healthyCount++; else issueCount++;

    const ast   = parseSeedTime(strOf(s, 'avg_seed_time'));
    const seeds = numOf(s, 'seeding') ?? 0;
    if (ast !== null && seeds > 0) {
      weightedSeedSec += ast * seeds;
      totalSeeding    += seeds;
    }
  });

  const bufGiB     = totalUpGiB - totalDownGiB;
  const totalRatio = totalDownGiB > 0 ? totalUpGiB / totalDownGiB : 0;
  const active     = healthyCount + issueCount;
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
    set(`${pfx}-agg-health-num`,   String(healthyCount));
    set(`${pfx}-agg-health-denom`, `/ ${trackers.length}`);
    set(`${pfx}-agg-health-sub`,
      issueCount > 0 ? `${issueCount} with issue${issueCount > 1 ? 's' : ''}` :
      active > 0 ? 'all healthy' : 'awaiting data');
    document.getElementById(`${pfx}-health-card`)?.style
      .setProperty('--card-accent', issueCount > 0 ? 'var(--red)' : 'var(--green)');

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
    // Overall-ratio trend (up÷down), not raw up/down — a meaningful
    // trajectory for "is health improving or worsening", coloured by the
    // CURRENT health state rather than the trend's own sign.
    renderSparkline(`${pfx}-spark-health`,   agg.up.map((v, i) => agg.down[i] ? v / agg.down[i] : 0), issueCount > 0 ? '--red' : '--green');
    renderSparkline(`${pfx}-spark-avg-seed`, agg.avgSeed, '--pink');
  }
}

function set(id: string, val: string): void {
  const el = document.getElementById(id);
  if (el) el.textContent = val;
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
