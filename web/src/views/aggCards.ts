// views/aggCards.ts — aggregate stat cards at the top of both views
import type { AppSettings, HistoryPoint, StatsMap, Tracker } from '../types';
import { numOf, strOf } from '../state';
import { fmtGib, fmtRatio, fmtSeedTime, parseRatio } from '../utils/format';
import { parseSize, parseSeedTime } from '../utils/parse';
import { renderSparkline } from '../components/sparkline';
import { buildAggSeries } from '../utils/history';

export function renderAggCards(
  trackers: Tracker[],
  statsCache: StatsMap,
  historyData: HistoryPoint[],
  settings: AppSettings,
): void {
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

    renderSparkline(`${pfx}-spark-up`,       agg.up,     '--green');
    renderSparkline(`${pfx}-spark-down`,     agg.down,   '--purple');
    renderSparkline(`${pfx}-spark-buf`,      agg.buffer, '--blue');
    renderSparkline(`${pfx}-spark-ratio`,    agg.ratio,  '--amber');
    renderSparkline(`${pfx}-spark-health`,   agg.up.map((v, i) => agg.down[i] ? v / agg.down[i] : 0), issueCount > 0 ? '--red' : '--green');
    renderSparkline(`${pfx}-spark-avg-seed`, agg.avgSeed, '--pink');
  }
}

function set(id: string, val: string): void {
  const el = document.getElementById(id);
  if (el) el.textContent = val;
}
