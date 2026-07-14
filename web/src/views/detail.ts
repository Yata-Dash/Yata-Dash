// views/detail.ts — the per-tracker detail page: identity header, mini-charts
// driven by the tracker's targets (fallback: the main six; user-overridable,
// up to ten), every reported stat, targets progress, account rules,
// direct invite routes leaving this tracker, and the group-change timeline.
// Entered by clicking a tracker's name on a card/row (or the edit screen's
// Details button); NOT a persisted view — switching views or reloading
// returns to the dashboard.
import * as api from '../api';
import { renderChart } from '../components/chart';
import type { ChartSeries } from '../components/chart';
import { buildStatRows } from '../components/profile';
import { appSettings, groupDefs, numOf, statsCache, strOf, trackers } from '../state';
import { esc, fmtEtaDays, fmtTrackerName } from '../utils/format';
import { buildTargets, fmtDateTime } from './grid';
import { findGroupDef, renderGroupBadge, renderUsername } from '../utils/group';
import { eventGlobeSvg } from '../utils/icons';
import { getFaviconUrl, memberDur } from '../utils/parse';
import {
  HISTORY_METRICS, metricLabel, metricUnit, recentRatePerDay, targetRefLinesFor,
} from '../utils/series';
import type { HistoryRangeKey } from '../utils/series';
import type { HistoryEvent, HistorySeriesResponse, PathwayStep, Tracker } from '../types';

// ── State (not persisted except the per-tracker chart picks) ────────────────

const METRICS_KEY = 'yata.detail.metrics'; // Record<trackerId, string[]>
const RANGE_KEY = 'yata.detail.range';
const PROJECTION_KEY = 'yata.detail.projection';

const DETAIL_RANGES: { key: HistoryRangeKey; label: string }[] = [
  { key: '7d', label: '7d' },
  { key: '30d', label: '30d' },
  { key: '90d', label: '90d' },
  { key: '365d', label: '1y' },
  { key: 'all', label: 'All' },
];

/** The fallback six when a tracker has no chartable targets. */
const DEFAULT_SIX = ['ratio', 'seed_size', 'uploaded', 'downloaded', 'buffer', 'avg_seed_time'];

/** targets-map key → history metric (inverse of TARGET_KEYS_FOR_METRIC). */
const TARGET_KEY_TO_METRIC: Record<string, string> = {
  total_uploads: 'uploads_approved',
  avg_seed: 'avg_seed_time',
};

let trackerId: string | null = null;
let range: HistoryRangeKey = (localStorage.getItem(RANGE_KEY) as HistoryRangeKey) || '90d';
let projection = localStorage.getItem(PROJECTION_KEY) === '1';
let lastResp: HistorySeriesResponse | null = null;
let lastRoutes: PathwayStep[] | null = null; // null = pathways unavailable
let fetchSeq = 0;
let menuCloserWired = false; // document-level close handler, wired once

function savedMetricsMap(): Record<string, string[]> {
  try { return JSON.parse(localStorage.getItem(METRICS_KEY) ?? '{}'); } catch { return {}; }
}

function saveMetrics(id: string, keys: string[]) {
  const map = savedMetricsMap();
  map[id] = keys;
  try { localStorage.setItem(METRICS_KEY, JSON.stringify(map)); } catch { /* private mode */ }
}

const MAX_CHARTS = 10;

/** Chart metrics for a tracker: its set targets first (that's what the page
 *  is for — watching progress toward them), padded to six with the classics.
 *  The Charts menu lets the user go up to MAX_CHARTS. */
function defaultMetrics(t: Tracker): string[] {
  const out: string[] = [];
  for (const key of Object.keys(t.targets ?? {})) {
    const m = TARGET_KEY_TO_METRIC[key] ?? key;
    if (HISTORY_METRICS.some(hm => hm.key === m) && !out.includes(m)) out.push(m);
  }
  for (const m of DEFAULT_SIX) {
    if (out.length >= 6) break;
    if (!out.includes(m)) out.push(m);
  }
  return out.slice(0, MAX_CHARTS);
}

function chartMetrics(t: Tracker): string[] {
  const saved = savedMetricsMap()[t.id];
  if (Array.isArray(saved)) {
    const valid = saved.filter(m => HISTORY_METRICS.some(hm => hm.key === m));
    if (valid.length) return valid.slice(0, MAX_CHARTS);
  }
  return defaultMetrics(t);
}

// ── Entry / exit ────────────────────────────────────────────────────────────

export function openTrackerDetail(id: string): void {
  trackerId = id;
  lastResp = null;
  lastRoutes = null;
  // Hide whichever dashboard view is up; the view buttons stay unhighlighted
  // (detail is a drill-down, not a fourth tab). Any view switch exits it.
  for (const vid of ['view-grid', 'view-table', 'view-pathways', 'view-history']) {
    const el = document.getElementById(vid);
    if (el) el.style.display = 'none';
  }
  const root = document.getElementById('view-detail');
  if (root) root.style.display = 'block';
  render();
  void loadData();
  window.scrollTo(0, 0);
}

export function closeTrackerDetail(): void {
  trackerId = null;
  const root = document.getElementById('view-detail');
  if (root) { root.style.display = 'none'; root.innerHTML = ''; }
  // Restore the persisted dashboard view.
  (window as unknown as { setView: (v: string) => void }).setView(
    localStorage.getItem('u3d-view') || 'grid');
}

/** Called from main.ts after tracker/stat reloads so an open page stays live.
 *  Re-fetches the series too: a target change can shift the default chart
 *  metrics and always shifts the target reference lines, so a redraw from
 *  cached data alone would look stale. */
export function redrawDetail(): void {
  if (!trackerId) return;
  if (document.getElementById('view-detail')?.style.display === 'none') return;
  render();       // immediate: fresh stats, targets, rules
  void loadData(); // async: fresh series (new metrics/reflines) + routes, then redraws
}

function current(): Tracker | undefined {
  return trackers.find(t => t.id === trackerId);
}

// ── Data ────────────────────────────────────────────────────────────────────

async function loadData(): Promise<void> {
  const t = current();
  if (!t) return;
  const seq = ++fetchSeq;
  const [seriesRes, fromRes] = await Promise.all([
    api.fetchHistorySeries({ trackers: [t.id], fields: chartMetrics(t), range }),
    api.fetchPathwaysFrom(t.id),
  ]);
  if (seq !== fetchSeq || trackerId !== t.id) return; // superseded / page left
  lastResp = seriesRes.ok ? seriesRes.data : null;
  lastRoutes = fromRes.ok ? (fromRes.data.routes ?? []) : null;
  render();
  drawCharts();
}

// ── Rendering ───────────────────────────────────────────────────────────────

function render(): void {
  const root = document.getElementById('view-detail');
  if (!root || !trackerId) return;
  const t = current();
  if (!t) {
    root.innerHTML = `<div class="pw-notice"><p class="pw-notice-title">Tracker not found.</p></div>`;
    return;
  }
  const stats = statsCache[t.id];
  const liveGroup = String(stats?.fields?.['group']?.value ?? '');
  const gDef = findGroupDef(groupDefs, t.def_key ?? '', liveGroup);
  const username = String(stats?.fields?.['username']?.value ?? t.username ?? '');
  const joinDate = String(stats?.fields?.['join_date']?.value ?? '');
  const favicon = t.url
    ? `<img class="detail-favicon" src="${esc(getFaviconUrl(t.url))}" alt="" onerror="this.style.display='none'">`
    : '';

  // Unread mail/notification flags — same icons and Display toggles as the
  // grid cards and table rows; only shown when the flag is actually "true".
  const unreadFlags =
    (appSettings.show_unread_mail !== false && strOf(stats, 'unread_mail') === 'true'
      ? `<span class="unread-flag" title="Unread mail on ${esc(t.name)} (as of the last scrape) — check your inbox"><i class="fas fa-envelope"></i></span>` : '') +
    (appSettings.show_unread_notifications !== false && strOf(stats, 'unread_notifications') === 'true'
      ? `<span class="unread-flag" title="Unread notifications on ${esc(t.name)} (as of the last scrape)"><i class="fas fa-bell"></i></span>` : '');

  // Active event banner — freeleech/announcement, with live countdown (the
  // global ticker in main.ts drives any .event-countdown in the document).
  const evText = strOf(stats, 'active_event') || null;
  const evEndsAt = numOf(stats, 'active_event_ends_at');
  const evEndsLabel = evEndsAt ? new Date(evEndsAt * 1000).toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }) : '';
  const eventBanner = evText ? `<div class="exp-event-banner">
    ${eventGlobeSvg('flex-shrink:0')}
    <span class="exp-event-text">${esc(evText)}</span>
    ${evEndsAt ? `<span class="exp-event-timer-wrap"><span class="event-countdown" data-ends-at="${evEndsAt}">…</span><span class="exp-event-ends">ends ${esc(evEndsLabel)}</span></span>` : ''}
  </div>` : '';

  const header = `<div class="detail-header">
    <button type="button" class="btn btn-ghost btn-sm" onclick="closeTrackerDetail()" title="Back to the dashboard">
      <i class="fas fa-arrow-left" style="margin-right:5px"></i>Back</button>
    ${favicon}
    <div class="detail-title-wrap">
      <div class="detail-title">${esc(fmtTrackerName(t.name, t.abbr ?? '', appSettings.tracker_name_mode))}
        ${liveGroup ? renderGroupBadge(gDef, liveGroup, appSettings, 'badge-group') : ''}
        ${unreadFlags}
      </div>
      <div class="detail-sub">
        ${username ? renderUsername(username, gDef, appSettings, 'private-blur') : ''}
        ${joinDate ? `<span title="Joined ${esc(joinDate)}">member ${memberDur(joinDate)}</span>` : ''}
        ${stats?.fetched_at ? `<span>updated ${esc(fmtDateTime(stats.fetched_at))}</span>` : ''}
      </div>
    </div>
    <div class="detail-actions">
      <button type="button" class="btn btn-ghost btn-sm" onclick="refreshSingle('${esc(t.id)}')" title="Refresh stats now">
        <i class="fas fa-rotate"></i></button>
      ${t.profile_url ? `<a class="btn btn-ghost btn-sm" href="${esc(t.profile_url)}" target="_blank" rel="noopener noreferrer" title="Open your profile on the tracker">
        <i class="fas fa-arrow-up-right-from-square"></i></a>` : ''}
      <button type="button" class="btn btn-ghost btn-sm" onclick="openEditModal('${esc(t.id)}')" title="Edit tracker">
        <i class="fas fa-pen"></i></button>
    </div>
  </div>`;

  const rangeChips = DETAIL_RANGES.map(r =>
    `<button type="button" class="history-range-btn${r.key === range ? ' active' : ''}" data-range="${r.key}">${r.label}</button>`).join('');
  const metricsMenu = `<div class="detail-controls-right">
    <button type="button" class="history-range-btn${projection ? ' active' : ''}" id="detail-projection-btn"
      title="Extend each line at its recent rate — dashed, turning green where it reaches a target">Projection</button>
    <div class="history-menu-wrap">
      <button type="button" class="history-menu-btn" id="detail-metrics-btn">Charts <span style="opacity:.6">▾</span></button>
      <div class="history-menu" id="detail-metrics-menu" style="display:none">
        ${HISTORY_METRICS.map(m => `<label><input type="checkbox" data-metric="${m.key}"
          ${chartMetrics(t).includes(m.key) ? 'checked' : ''}> ${esc(m.label)}</label>`).join('')}
        <div class="detail-menu-hint">Up to ten charts.</div>
      </div>
    </div>
  </div>`;

  const chartCards = chartMetrics(t).map(m => `
    <div class="detail-chart-card">
      <div class="detail-chart-head">
        <span>${esc(metricLabel(m))}</span>
        <button type="button" class="detail-chart-open" data-metric="${m}"
          title="Open in History"><i class="fas fa-chart-line"></i></button>
      </div>
      <div class="detail-chart" id="dchart-${m}"></div>
    </div>`).join('');

  root.innerHTML = `${header}
    ${eventBanner}
    <div class="detail-controls">
      <div class="history-ranges">${rangeChips}</div>
      ${metricsMenu}
    </div>
    <div class="detail-charts">${chartCards}</div>
    <div class="detail-cols">
      <div class="detail-col" id="detail-stats"></div>
      <div class="detail-col" id="detail-targets"></div>
      <div class="detail-col" id="detail-side"></div>
    </div>`;

  renderStats(t);
  renderTargetsCol(t);
  renderSideCol(t);
  wireControls(t);
}

function renderStats(t: Tracker): void {
  const el = document.getElementById('detail-stats');
  if (!el) return;
  const rows = buildStatRows(statsCache[t.id], undefined, t.min_ratio);
  el.innerHTML = `<div class="exp-section-title">Stats</div>
    <div class="exp-stat-list">${rows.map(r => `<div class="exp-stat">
      <span class="exp-stat-label">${esc(r.label)}</span>
      <span class="exp-stat-value" style="color:var(--${r.color})">${esc(r.value)}</span>
    </div>`).join('') || '<div class="detail-empty">No stats recorded yet.</div>'}</div>`;
}

function renderTargetsCol(t: Tracker): void {
  const el = document.getElementById('detail-targets');
  if (!el) return;
  const targetsHtml = buildTargets(t, statsCache[t.id], appSettings, groupDefs, t.def_key ?? '');
  const rules: string[] = [];
  if (t.min_ratio && t.min_ratio > 0) rules.push(`<div class="exp-stat"><span class="exp-stat-label">Min Ratio</span><span class="exp-stat-value">${esc(String(t.min_ratio))}</span></div>`);
  if (t.min_seed_days && t.min_seed_days > 0) rules.push(`<div class="exp-stat"><span class="exp-stat-label">Min Seed Time</span><span class="exp-stat-value">${t.min_seed_days} day${t.min_seed_days === 1 ? '' : 's'}</span></div>`);
  el.innerHTML = (targetsHtml || '<div class="exp-section-title">Targets</div><div class="detail-empty">No targets set — add some from the edit screen.</div>')
    + (rules.length ? `<div style="margin-top:14px">
        <div class="exp-section-title" title="Reference from the tracker's rules page — full details stay on the tracker">Rules</div>
        <div class="exp-stat-list">${rules.join('')}</div>
      </div>` : '');
}

function renderSideCol(t: Tracker): void {
  const el = document.getElementById('detail-side');
  if (!el) return;

  // Direct invite routes leaving this tracker (community data, first-hop
  // evaluation — same engine as the Pathways view). The Pathways lists apply
  // here too: favourites get their star and sort first, "not interested"
  // targets are hidden.
  let pathsHtml = '';
  const favs = new Set(appSettings.pathway_favorites ?? []);
  const notInterested = new Set(appSettings.pathway_not_interested ?? []);
  const routes = (lastRoutes ?? [])
    .filter(s => !notInterested.has(s.to))
    .sort((a, b) => Number(favs.has(b.to)) - Number(favs.has(a.to))); // stable: keeps met-first within each half
  if (routes.length) {
    const showEtas = appSettings.show_pathway_etas !== false;
    const rows = routes.slice(0, 8).map(s => {
      const met = s.eta_days === 0 && !s.has_unknown;
      const chip = met
        ? '<span class="pw-met-dot" title="Meets the listed requirements — community data, not a guarantee of an invite">✓ reqs met</span>'
        : (showEtas && s.eta_days > 0 ? `<span class="pw-req-eta">${fmtEtaDays(s.eta_days)}${s.has_unknown ? '+' : ''}</span>` : '');
      const star = favs.has(s.to) ? '<span class="detail-route-fav" title="Pathways favourite">★</span>' : '';
      return `<div class="detail-route">
        <span class="detail-route-to">→ ${esc(s.to)}</span>${star}${chip}
        ${s.reqs_raw ? `<div class="detail-route-reqs">${esc(s.reqs_raw)}</div>` : ''}
      </div>`;
    }).join('');
    pathsHtml = `<div class="exp-section-title" title="Active direct invite routes in the community pathways dataset — reference only">Pathways from here</div>
      <div class="detail-routes">${rows}</div>
      ${routes.length > 8 ? `<div class="detail-empty">+ ${routes.length - 8} more in the Pathways view.</div>` : ''}`;
  }

  // Group-change timeline (recorded events, newest first, selected range).
  const events = (lastResp?.events ?? []).filter(e => e.kind === 'group_change');
  const rows = [...events].reverse().map(e => {
    const dir = groupDirection(t, e);
    const icon = dir === 'promotion' ? '<span style="color:var(--green)">▲</span>'
      : dir === 'demotion' ? '<span style="color:var(--red)">▼</span>' : '•';
    const when = new Date(e.at * 1000).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
    return `<div class="detail-event">${icon}
      <span class="detail-event-detail">${esc(e.detail.replace('→', ' → '))}</span>
      <span class="detail-event-when">${esc(when)}</span>
    </div>`;
  }).join('');
  const timelineHtml = `<div class="exp-section-title" style="margin-top:${pathsHtml ? '14px' : '0'}"
      title="Recorded promotions and demotions within the selected range">Group timeline</div>
    ${rows ? `<div class="detail-events">${rows}</div>`
      : '<div class="detail-empty">No group changes in this range — try a longer one. (Recording started with Beta-20260712.)</div>'}`;

  el.innerHTML = pathsHtml + timelineHtml;
}

/** Promotion or demotion, by the two groups' positions in the def order. */
function groupDirection(t: Tracker, e: HistoryEvent): 'promotion' | 'demotion' | 'neutral' {
  const parts = e.detail.split('→').map(s => s.trim());
  if (parts.length !== 2 || !t.def_key) return 'neutral';
  const groups = groupDefs[t.def_key] ?? [];
  const oldIdx = groups.findIndex(g => g.name.toLowerCase() === parts[0].toLowerCase());
  const newIdx = groups.findIndex(g => g.name.toLowerCase() === parts[1].toLowerCase());
  if (oldIdx < 0 || newIdx < 0 || oldIdx === newIdx) return 'neutral';
  return newIdx > oldIdx ? 'promotion' : 'demotion';
}

// ── Charts ──────────────────────────────────────────────────────────────────

function drawCharts(): void {
  const t = current();
  if (!t) return;
  for (const m of chartMetrics(t)) {
    const el = document.getElementById(`dchart-${m}`);
    if (!el) continue;
    const s = lastResp?.series.find(x => x.tracker_id === t.id && x.field === m);
    if (!lastResp || !s || s.points.length < 2) {
      el.innerHTML = `<div class="detail-empty" style="padding:24px 10px;text-align:center">${lastResp ? 'No history yet' : 'Loading…'}</div>`;
      continue;
    }
    const unit = s.unit ?? metricUnit(m);
    const refLines = targetRefLinesFor(t, m, groupDefs);
    // Clamp the window to the data (mirrors History's effectiveWindow).
    let to = lastResp.range.to;
    const from = Math.max(lastResp.range.from, s.points[0][0]);
    const series: ChartSeries[] = [{
      id: t.id, label: metricLabel(m), color: '--accent', unit, points: s.points,
    }];
    if (projection) {
      // Continue the line at its recent rate (backend's stable rate if present,
      // else the charted slope). Extend the window ~25% (1–90 days).
      const [lt, lv] = s.points[s.points.length - 1];
      const rate = statsCache[t.id]?.rates?.[m] ?? recentRatePerDay(s.points) ?? 0;
      to = to + Math.min(Math.max((to - from) * 0.25, 86400), 90 * 86400);
      const projEnd = lv + rate * ((to - lt) / 86400);
      // Turn the tail green where it rises to meet a target the current value is
      // still below — "on this trajectory you reach it".
      const crosses = refLines.some(r => lv < r.value && projEnd >= r.value);
      series.push({
        id: `${t.id}:proj`, label: metricLabel(m), unit, ghost: true,
        color: crosses ? '--green' : '--accent', points: [[lt, lv], [to, projEnd]],
      });
    }
    renderChart(el as HTMLElement, {
      series, from, to, height: 130, pins: [],
      refLines, integerTicks: unit === 'count', tooltip: true,
    });
  }
}

// ── Controls ────────────────────────────────────────────────────────────────

function wireControls(t: Tracker): void {
  document.querySelectorAll<HTMLElement>('#view-detail [data-range]').forEach(btn => {
    btn.onclick = () => {
      range = btn.dataset['range'] as HistoryRangeKey;
      try { localStorage.setItem(RANGE_KEY, range); } catch { /* private mode */ }
      render();
      void loadData();
    };
  });

  // Projection toggle — persisted; redraws the charts (no refetch needed).
  const projBtn = document.getElementById('detail-projection-btn');
  if (projBtn) projBtn.onclick = () => {
    projection = !projection;
    try { localStorage.setItem(PROJECTION_KEY, projection ? '1' : '0'); } catch { /* private mode */ }
    projBtn.classList.toggle('active', projection);
    drawCharts();
  };

  // Charts menu — checkbox picks persist per tracker.
  const menuBtn = document.getElementById('detail-metrics-btn');
  const menu = document.getElementById('detail-metrics-menu');
  if (menuBtn && menu) {
    menuBtn.onclick = (e) => {
      e.stopPropagation();
      menu.style.display = menu.style.display === 'none' ? 'block' : 'none';
    };
    menu.onclick = (e) => e.stopPropagation();
    if (!menuCloserWired) {
      menuCloserWired = true;
      document.addEventListener('click', () => {
        const m = document.getElementById('detail-metrics-menu');
        if (m) m.style.display = 'none';
      });
    }
    menu.querySelectorAll<HTMLInputElement>('input[data-metric]').forEach(cb => {
      cb.onchange = () => {
        let picked = [...menu.querySelectorAll<HTMLInputElement>('input[data-metric]:checked')]
          .map(x => x.dataset['metric']!);
        if (picked.length > MAX_CHARTS) { cb.checked = false; picked = picked.filter(m => m !== cb.dataset['metric']); }
        if (!picked.length) { cb.checked = true; return; } // keep at least one
        saveMetrics(t.id, picked);
        render();
        void loadData();
      };
    });
  }

  // Mini-chart → History, pre-filtered to this tracker + metric (through the
  // History module's own seam — its UI state lives in module memory, so
  // writing localStorage directly would be ignored once it's loaded).
  document.querySelectorAll<HTMLElement>('#view-detail .detail-chart-open').forEach(btn => {
    btn.onclick = () => {
      void import('./history').then(h => {
        h.presetHistory(btn.dataset['metric']!, [t.id]);
        trackerId = null;
        const root = document.getElementById('view-detail');
        if (root) { root.style.display = 'none'; root.innerHTML = ''; }
        (window as unknown as { setView: (v: string) => void }).setView('history');
      });
    };
  });
}
