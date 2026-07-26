// views/detail.ts — the per-tracker detail page: identity header, mini-charts
// driven by the tracker's targets (fallback: the main six; user-overridable,
// up to ten), then a row of cards — every reported stat, targets progress and
// account rules, the direct invite routes leaving this tracker, and the group
// + connection timelines.
// Entered by clicking a tracker's name on a card/row (or the edit screen's
// Details button); NOT a persisted view — switching views or reloading
// returns to the dashboard.
import * as api from '../api';
import { renderChart } from '../components/chart';
import type { ChartSeries } from '../components/chart';
import { buildStatRows } from '../components/profile';
import { appSettings, groupDefs, numOf, statsCache, strOf, trackers } from '../state';
import { connectionEventText, esc, fieldLabel, fmtDay, fmtEtaDays, fmtStamp, fmtTrackerName, isConnectionKind } from '../utils/format';
import { buildTargets, fmtDateTime } from './grid';
import { findGroupDef, renderGroupBadge, renderUsername } from '../utils/group';
import { eventGlobeSvg } from '../utils/icons';
import { getFaviconUrl, memberDur } from '../utils/parse';
import {
  fmtGiB, HISTORY_METRICS, metricLabel, metricUnit, recentRatePerDay, targetRefLinesFor,
} from '../utils/series';
import type { HistoryRangeKey, SeriesUnit } from '../utils/series';
import type { ActiveEvent, HistoryEvent, HistorySeriesResponse, PathwayStep, Tracker, TrackerStatsResponse } from '../types';

// ── State (not persisted except the per-tracker chart picks) ────────────────

const METRICS_KEY = 'yata.detail.metrics'; // Record<trackerId, string[]>
const RANGE_KEY = 'yata.detail.range';
const PROJECTION_KEY = 'yata.detail.projection';
const DELTAS_KEY = 'yata.detail.deltas'; // '0' = per-stat change chips hidden

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

/** Every canonical recorded field, requested in the one series call below —
 *  see loadData() for why this is a superset of chartMetrics(t). */
const ALL_METRIC_KEYS = HISTORY_METRICS.map(m => m.key);

let trackerId: string | null = null;
let range: HistoryRangeKey = (localStorage.getItem(RANGE_KEY) as HistoryRangeKey) || '90d';
let projection = localStorage.getItem(PROJECTION_KEY) === '1';
let deltas = localStorage.getItem(DELTAS_KEY) !== '0'; // default ON
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

/** Invite routes listed before "+N more". Its own card now, so it can afford
 *  more than it could when it shared one with the timelines. */
const MAX_ROUTES = 12;

/** Rows the timelines card shows across BOTH timelines combined — see
 *  renderTimelinesCol for why one shared budget rather than one cap each. */
const TIMELINE_ROWS = 12;

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

/** Called after a tracker delete succeeds. If the deleted tracker is the one
 *  currently open in the detail page, close it — a redrawDetail() here would
 *  just render "Tracker not found" once it's gone from state.trackers.
 *  If the detail page isn't the visible view right now (user navigated away
 *  without an explicit close, so `trackerId` is stale), just forget the id
 *  rather than forcing navigation via closeTrackerDetail()'s setView() call —
 *  that would yank the user out of, say, the Settings page they're deleting
 *  from. Deleting some other tracker while detail IS open is a no-op. */
export function detailTrackerDeleted(id: string): void {
  if (trackerId !== id) return;
  if (document.getElementById('view-detail')?.style.display === 'none') {
    trackerId = null;
    return;
  }
  closeTrackerDetail();
}

function current(): Tracker | undefined {
  return trackers.find(t => t.id === trackerId);
}

// ── Data ────────────────────────────────────────────────────────────────────

async function loadData(): Promise<void> {
  const t = current();
  if (!t) return;
  const seq = ++fetchSeq;
  // Request every canonical metric, not just the currently-charted ones: the
  // Stats section's per-stat delta chips (statDeltaChip below) want a series
  // for any stat that's ever recorded, and this is still ONE call — same
  // shape as before, just a wider (and bounded — 11 keys, one tracker) field
  // list — so it stays within "no per-stat network calls".
  const [seriesRes, fromRes] = await Promise.all([
    api.fetchHistorySeries({ trackers: [t.id], fields: ALL_METRIC_KEYS, range }),
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

  const eventBanner = renderEventBanners(stats);

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
    <button type="button" class="history-range-btn${deltas ? ' active' : ''}" id="detail-deltas-btn"
      title="Show each stat's change over the selected range">Changes</button>
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
      <div class="detail-col" id="detail-pathways"></div>
      <div class="detail-col" id="detail-timelines"></div>
    </div>`;

  renderStats(t);
  renderTargetsCol(t);
  renderPathwaysCol();
  renderTimelinesCol(t);
  wireControls(t);
}

/**
 * Active-event banners — the countdown ticker in main.ts drives any
 * .event-countdown in the document, so these only need the data attribute.
 *
 * Two shapes are handled. Trackers that expose the structured `active_events`
 * list (expanded UNIT3D stats) get one banner per event, carrying everything
 * they bothered to send: the description, a link to the event page, the user's
 * own standing in it. Everything else — every scrape, and every UNIT3D install
 * that hasn't shipped the new endpoint — has only the flat `active_event`
 * string, and gets the single banner it always had. The Detail page is the one
 * surface with room for the long form; cards and table rows stay on the flat
 * summary the backend derives from the same list.
 */
function renderEventBanners(stats: TrackerStatsResponse | undefined): string {
  const list = eventList(stats);
  if (list.length) return list.map(eventBanner).join('');

  const text = strOf(stats, 'active_event');
  if (!text) return '';
  const endsAt = numOf(stats, 'active_event_ends_at');
  return `<div class="exp-event-banner">
    ${eventGlobeSvg('flex-shrink:0')}
    <span class="exp-event-text">${esc(text)}</span>
    ${endsAt ? countdownHtml(endsAt) : ''}
  </div>`;
}

/** The structured event list, or [] when this tracker doesn't report one. */
function eventList(stats: TrackerStatsResponse | undefined): ActiveEvent[] {
  const raw = stats?.fields?.['active_events']?.value;
  return Array.isArray(raw) ? raw as ActiveEvent[] : [];
}

function countdownHtml(at: number, verb: 'ends' | 'starts' = 'ends'): string {
  return `<span class="exp-event-timer-wrap">
    <span class="event-countdown exp-event-timer" data-ends-at="${at}">…</span>
    <span class="exp-event-ends">${verb} ${esc(fmtStamp(at))}</span>
  </span>`;
}

/** The countdown an event actually wants. A live event is counting down to its
 *  end; one that hasn't started is counting down to its START — ticking toward
 *  the end of something that isn't running yet tells you nothing. */
function eventCountdown(ev: ActiveEvent, live: boolean): string {
  const now = Date.now() / 1000;
  if (!live && ev.starts_at && ev.starts_at > now) return countdownHtml(ev.starts_at, 'starts');
  return ev.ends_at ? countdownHtml(ev.ends_at) : '';
}

function eventBanner(ev: ActiveEvent): string {
  const name = ev.name || ev.type || 'Event';
  // The tracker's own Font Awesome class when it sent one — the icon is part of
  // how the event is presented on the tracker, so borrowing it keeps the two
  // recognisably the same thing. Falls back to Yata's globe.
  const icon = ev.icon
    ? `<i class="${esc(ev.icon)}" style="flex-shrink:0"></i>`
    : eventGlobeSvg('flex-shrink:0');
  // Anything not currently running is dimmed and labelled, so an event that
  // starts next week can be listed without reading as active now.
  const live = !ev.status || ev.status.toLowerCase() === 'live';
  const badge = live ? '' : `<span class="exp-event-status">${esc(ev.status!)}</span>`;
  const title = ev.url
    ? `<a class="exp-event-link" href="${esc(ev.url)}" target="_blank" rel="noopener noreferrer">${esc(name)}</a>`
    : esc(name);
  const progress = eventProgress(ev);
  // has-desc lets the name keep its width and the description absorb the
  // shrinking; without one, the name itself has to give way (see the CSS).
  return `<div class="exp-event-banner${live ? '' : ' is-upcoming'}${ev.description ? ' has-desc' : ''}">
    ${icon}
    <span class="exp-event-text">${title}${badge}</span>
    ${ev.description ? `<span class="exp-event-desc">${esc(ev.description)}</span>` : ''}
    ${progress}
    ${eventCountdown(ev, live)}
  </div>`;
}

/** The user's own standing in an event ("Rank 2 · Uploads 6"). Free-form — the
 *  tracker decides the keys, so they're rendered generically rather than
 *  guessed at.
 *
 *  Label first, value second: the keys mix counts with positions, and
 *  value-first turns "rank: 2" into "2 rank", which reads as a quantity of
 *  ranks. "Rank 2" and "Uploads 6" both work. */
function eventProgress(ev: ActiveEvent): string {
  const p = ev.user_progress;
  if (!p || typeof p !== 'object') return '';
  const parts = Object.entries(p)
    .filter(([, v]) => v !== null && v !== undefined && v !== '')
    .map(([k, v]) => `${esc(fieldLabel(k))} ${esc(String(v))}`);
  if (!parts.length) return '';
  return `<span class="exp-event-progress" title="Your standing in this event">${parts.join(' · ')}</span>`;
}

function renderStats(t: Tracker): void {
  const el = document.getElementById('detail-stats');
  if (!el) return;
  // Settings passed so hit_and_runs honours the highlight_hnr display toggle.
  const rows = buildStatRows(statsCache[t.id], undefined, t.min_ratio, appSettings);
  el.innerHTML = `<div class="exp-section-title">Stats</div>
    <div class="exp-stat-list">${rows.map(r => `<div class="exp-stat">
      <span class="exp-stat-label">${esc(r.label)}</span>
      <span class="exp-stat-value" style="color:var(--${r.color})">${statDeltaChip(t, r.key)}${esc(r.value)}</span>
    </div>`).join('') || '<div class="detail-empty">No stats recorded yet.</div>'}</div>`;
}

/** Sign-preserving delta formatting per unit — mirrors targetRefLinesFor's
 *  unit switch in utils/series.ts, but always shows a sign (target labels
 *  never do) and works from a raw delta rather than an absolute value.
 *  Seconds go through fmtEtaDays (which respects the duration_format
 *  setting) rather than fmtSeedTime, matching how series.ts already formats
 *  seconds-unit reference-line labels. */
function fmtSignedDelta(unit: SeriesUnit, dv: number): string {
  const sign = dv > 0 ? '+' : '-';
  const abs = Math.abs(dv);
  switch (unit) {
    case 'GiB':     return sign + fmtGiB(abs, 2);
    case 'ratio':   return sign + abs.toFixed(2);
    case 'seconds': return sign + fmtEtaDays(abs / 86400);
    default:        return sign + Math.round(abs).toLocaleString();
  }
}

/** Small muted "(+2.30 TiB /30d)" chip before a Stats-section value — the
 *  change over the currently selected range chip, read off the same series
 *  data loadData() already fetched for the mini-charts (see the comment
 *  there). Sits BEFORE the value so the coloured values stay flush on the
 *  right. Gated by the Changes toggle; omitted when the stat has no matching
 *  history series, too few points to diff, or a zero delta — a silent stat
 *  reads better than a "+0" chip on every row. */
function statDeltaChip(t: Tracker, key: string): string {
  if (!deltas) return '';
  const s = lastResp?.series.find(x => x.tracker_id === t.id && x.field === key);
  if (!s || s.points.length < 2) return '';
  const dv = s.points[s.points.length - 1][1] - s.points[0][1];
  if (!dv) return '';
  const unit = s.unit ?? metricUnit(key);
  const label = DETAIL_RANGES.find(r => r.key === range)?.label ?? range;
  return `<span class="stat-delta-chip">(${esc(fmtSignedDelta(unit, dv))} /${esc(label)})</span> `;
}

function renderTargetsCol(t: Tracker): void {
  const el = document.getElementById('detail-targets');
  if (!el) return;
  const targetsHtml = buildTargets(t, statsCache[t.id], appSettings, groupDefs, t.def_key ?? '', 'full');
  const rules: string[] = [];
  if (t.min_ratio && t.min_ratio > 0) rules.push(`<div class="exp-stat"><span class="exp-stat-label">Min Ratio</span><span class="exp-stat-value">${esc(String(t.min_ratio))}</span></div>`);
  if (t.min_seed_days_episode && t.min_seed_days_episode > 0) rules.push(`<div class="exp-stat"><span class="exp-stat-label">Episode Seed Time</span><span class="exp-stat-value">${t.min_seed_days_episode} day${t.min_seed_days_episode === 1 ? '' : 's'}</span></div>`);
  if (t.min_seed_days_season && t.min_seed_days_season > 0) rules.push(`<div class="exp-stat"><span class="exp-stat-label">Season Seed Time</span><span class="exp-stat-value">${t.min_seed_days_season} day${t.min_seed_days_season === 1 ? '' : 's'}</span></div>`);
  if (t.min_seed_hours && t.min_seed_hours > 0) rules.push(`<div class="exp-stat"><span class="exp-stat-label">Min Seed Time</span><span class="exp-stat-value">${t.min_seed_hours} hours</span></div>`);
  if (!t.min_seed_hours && !t.min_seed_days_episode && !t.min_seed_days_season && t.min_seed_days && t.min_seed_days > 0) rules.push(`<div class="exp-stat"><span class="exp-stat-label">Min Seed Time</span><span class="exp-stat-value">${t.min_seed_days} day${t.min_seed_days === 1 ? '' : 's'}</span></div>`);
  if (t.rule_note) rules.push(`<div class="exp-stat"><span class="exp-stat-label">Details</span><span class="exp-stat-value">${esc(t.rule_note)}</span></div>`);
  el.innerHTML = (targetsHtml || '<div class="exp-section-title">Targets</div><div class="detail-empty">No targets set — add some from the edit screen.</div>')
    + (rules.length ? `<div style="margin-top:14px">
        <div class="exp-section-title" title="Reference from the tracker's rules page — full details stay on the tracker">Rules</div>
        <div class="exp-stat-list">${rules.join('')}</div>
      </div>` : '');
}

/** Direct invite routes leaving this tracker (community data, first-hop
 *  evaluation — same engine as the Pathways view), in their own card. The
 *  Pathways lists apply here too: favourites get their star and sort first,
 *  "not interested" targets are hidden.
 *
 *  Hidden entirely when there's nothing to list — an empty card in the row
 *  would be worse than one fewer card. */
function renderPathwaysCol(): void {
  const el = document.getElementById('detail-pathways');
  if (!el) return;
  const favs = new Set(appSettings.pathway_favorites ?? []);
  const notInterested = new Set(appSettings.pathway_not_interested ?? []);
  const routes = (lastRoutes ?? [])
    .filter(s => !notInterested.has(s.to))
    .sort((a, b) => Number(favs.has(b.to)) - Number(favs.has(a.to))); // stable: keeps met-first within each half
  if (!routes.length) {
    el.style.display = 'none';
    el.innerHTML = '';
    return;
  }
  el.style.display = '';
  const showEtas = appSettings.show_pathway_etas !== false;
  const rows = routes.slice(0, MAX_ROUTES).map(s => {
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
  el.innerHTML = `<div class="exp-section-title" title="Active direct invite routes in the community pathways dataset — reference only">Pathways from here</div>
    <div class="detail-routes">${rows}</div>
    ${routes.length > MAX_ROUTES ? `<div class="detail-empty">+ ${routes.length - MAX_ROUTES} more in the Pathways view.</div>` : ''}`;
}

/** The two event timelines, sharing one card and one row budget.
 *
 *  Group changes and connection changes answer different questions — "how is
 *  this account doing" versus "can I trust the numbers above are current" — but
 *  they're the same kind of record, so they live together. They also arrive in
 *  wildly different volumes: a tracker whose API is failing while its scrape
 *  still works records a down/up pair on EVERY refresh, which runs to hundreds
 *  of rows a day and buries the handful of group changes beside it. Hence
 *  fairShare: the card shows the most recent TIMELINE_ROWS between them, split
 *  so neither list can starve the other, and each says how many it left out. */
function renderTimelinesCol(t: Tracker): void {
  const el = document.getElementById('detail-timelines');
  if (!el) return;
  const evs = lastResp?.events ?? [];
  // Newest first, so a cap keeps the most recent — what you came to see.
  const groups = evs.filter(e => e.kind === 'group_change').reverse();
  const conns = evs.filter(e => isConnectionKind(e.kind)).reverse();
  const [gShow, cShow] = fairShare([groups.length, conns.length], TIMELINE_ROWS);

  const groupRows = groups.slice(0, gShow).map(e => {
    const dir = groupDirection(t, e);
    const icon = dir === 'promotion' ? '<span style="color:var(--green)">▲</span>'
      : dir === 'demotion' ? '<span style="color:var(--red)">▼</span>' : '•';
    return eventRow(icon, esc(e.detail.replace('→', ' → ')), e.at);
  }).join('');

  const connRows = conns.slice(0, cShow).map(e => {
    const c = connectionEventText(e.kind, e.detail);
    const icon = c.up ? '<span style="color:var(--green)">▲</span>' : '<span style="color:var(--red)">▼</span>';
    return eventRow(icon, esc(c.text), e.at);
  }).join('');

  el.innerHTML = `
    <div class="exp-section-title" title="Recorded promotions and demotions within the selected range">Group timeline</div>
    ${groupRows ? `<div class="detail-events">${groupRows}</div>`
      : '<div class="detail-empty">No group changes in this range</div>'}
    ${moreLine(groups.length - gShow)}
    <div class="exp-section-title" style="margin-top:14px"
      title="Recorded outages and recoveries within the selected range — whether Yata could reach the tracker, not how the account is doing">Connection timeline</div>
    ${connRows ? `<div class="detail-events">${connRows}</div>`
      // Recorded only on a CHANGE of state (internal/api/stats.go), so nothing
      // here is the good outcome, not missing data — say that rather than
      // nudging for a longer range the way the group timeline does.
      : '<div class="detail-empty">No connection changes in this range — nothing went down or came back.</div>'}
    ${moreLine(conns.length - cShow)}`;
}

/** "+ N more" footer for a capped timeline, pointing at where the rest can be
 *  seen — History draws every event in the range as a chart marker. */
function moreLine(hidden: number): string {
  if (hidden <= 0) return '';
  return `<div class="detail-empty" style="margin-top:6px">+ ${hidden} earlier — all of them are marked on the History chart.</div>`;
}

/** One timeline row — shared by the group and connection timelines so the two
 *  read as the same kind of record rather than two different widgets. */
function eventRow(icon: string, detailHtml: string, at: number): string {
  const when = fmtDay(new Date(at * 1000));
  return `<div class="detail-event">${icon}
    <span class="detail-event-detail">${detailHtml}</span>
    <span class="detail-event-when">${esc(when)}</span>
  </div>`;
}

/**
 * Split `budget` rows between lists that want `wants[i]` each, so no list can
 * starve another. Everyone gets an equal share; whatever a short list doesn't
 * need is redistributed to the ones that still want more, repeatedly.
 *
 *   [10, 10] of 12 → [6, 6]   — not [12, 0]
 *   [2, 200] of 12 → [2, 10]  — the short list doesn't waste its share
 *   [0, 200] of 12 → [0, 12]
 *
 * Odd rows left at the end go to the earlier list, which keeps the result
 * deterministic; with two lists that's at most a one-row difference.
 */
function fairShare(wants: number[], budget: number): number[] {
  const out = wants.map(() => 0);
  let left = budget;
  let active = wants.map((_, i) => i).filter(i => wants[i] > 0);
  while (left > 0 && active.length) {
    const share = Math.floor(left / active.length);
    if (share === 0) break; // fewer rows left than lists — handed out one each below
    let used = 0;
    for (const i of active) {
      const take = Math.min(share, wants[i] - out[i]);
      out[i] += take;
      used += take;
    }
    left -= used;
    active = active.filter(i => out[i] < wants[i]);
  }
  for (const i of active) {
    if (left <= 0) break;
    out[i]++;
    left--;
  }
  return out;
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
      // Uptime's missing days are days nobody measured — break, don't join.
      ...(unit === 'percent' ? { gapSec: 1.5 * 86400 } : {}),
    }];
    // Projecting a bounded metric walks the tail straight off the scale, and
    // "your uptime will be 140% next month" is not a forecast worth drawing.
    if (projection && unit !== 'percent') {
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

  // Changes (per-stat delta chips) toggle — persisted; re-renders the Stats
  // section only (the series data is already loaded).
  const deltasBtn = document.getElementById('detail-deltas-btn');
  if (deltasBtn) deltasBtn.onclick = () => {
    deltas = !deltas;
    try { localStorage.setItem(DELTAS_KEY, deltas ? '1' : '0'); } catch { /* private mode */ }
    deltasBtn.classList.toggle('active', deltas);
    renderStats(t);
  };

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
      // Escape closes it too — matches the pattern other popovers/dropdowns
      // use (main.ts's global handler, targetsPopover.ts's own listener).
      document.addEventListener('keydown', (e) => {
        if (e.key !== 'Escape') return;
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
