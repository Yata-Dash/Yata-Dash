// views/history.ts — the History / Growth view (HISTORY_VIEW_PLAN.md §4.3).
// One metric, N trackers overlaid, range toggle, crosshair readout, and a
// two-point pin for delta analysis. Read-only; flag-gated via FEATURES.history
// (main.ts never routes here while the flag is off).
import * as api from '../api';
import { exportChart, renderChart } from '../components/chart';
import type { ChartEvent, ChartMilestone, ChartRefLine, ChartSeries } from '../components/chart';
import {
  deltaStats, fmtPointTime, fmtUnitValue, HISTORY_METRICS, HISTORY_RANGES,
  isSummableUnit, metricLabel, milestonesFor, portfolioSeries, recentRatePerDay,
  smoothSeries, targetRefLinesFor, toRateSeries, valueAt,
} from '../utils/series';
import type { HistoryRangeKey, SeriesUnit } from '../utils/series';
import { esc, fmtTrackerName } from '../utils/format';
import type { HistorySeriesResponse, Tracker } from '../types';
import { appSettings, groupDefs, statsCache } from '../state';

// Tracker line colors, assigned by position in the enabled-tracker list so a
// tracker keeps its color across metric/range switches.
const PALETTE = ['--accent', '--teal', '--green', '--amber', '--blue', '--pink', '--orange', '--purple', '--red'];

// ── Persisted UI state ───────────────────────────────────────────────────────

interface HistoryUIState {
  metric: string;
  range: HistoryRangeKey;
  trackers: string[] | null; // null = all enabled; [] = none
  mode: 'value' | 'rate';    // cumulative curve vs per-day delta
  // Overlays (annotations, single-tracker) + series transforms.
  targets: boolean;          // target reference lines
  milestones: boolean;       // threshold-crossing markers
  groupChanges: boolean;     // promotion/demotion timeline markers
  portfolio: boolean;        // synthetic "sum of selected" line
  projection: boolean;       // dashed growth-rate continuation
  smoothing: boolean;        // moving-average the drawn line
}

const LS_KEY = 'yata.history.ui';

function loadUIState(): HistoryUIState {
  const dflt: HistoryUIState = {
    metric: 'uploaded', range: '30d', trackers: null, mode: 'value',
    targets: true, milestones: false, groupChanges: true,
    portfolio: false, projection: false, smoothing: false,
  };
  try {
    const raw = JSON.parse(localStorage.getItem(LS_KEY) ?? '{}');
    const bool = (v: unknown, d: boolean) => (typeof v === 'boolean' ? v : d);
    return {
      metric: HISTORY_METRICS.some(m => m.key === raw.metric) ? raw.metric : dflt.metric,
      range: HISTORY_RANGES.some(r => r.key === raw.range) ? raw.range : dflt.range,
      trackers: Array.isArray(raw.trackers) ? raw.trackers : (raw.trackers === null ? null : dflt.trackers),
      mode: raw.mode === 'rate' ? 'rate' : 'value',
      targets: bool(raw.targets, dflt.targets),
      milestones: bool(raw.milestones, dflt.milestones),
      groupChanges: bool(raw.groupChanges, dflt.groupChanges),
      portfolio: bool(raw.portfolio, dflt.portfolio),
      projection: bool(raw.projection, dflt.projection),
      smoothing: bool(raw.smoothing, dflt.smoothing),
    };
  } catch {
    return dflt;
  }
}

function saveUIState() {
  try { localStorage.setItem(LS_KEY, JSON.stringify(ui)); } catch { /* private mode */ }
}

const ui = loadUIState();

// ── Module state (not persisted) ─────────────────────────────────────────────

let allTrackers: Tracker[] = [];
let lastResp: HistorySeriesResponse | null = null;
let pins: number[] = [];
let hoverT: number | null = null;
let fetchSeq = 0;
let resizeWired = false;

// ── Entry point (called by main.ts when the view becomes active) ────────────

export function renderHistory(trackers: Tracker[]): void {
  allTrackers = trackers.filter(t => t.enabled !== false);
  const root = document.getElementById('view-history');
  if (!root) return;

  if (!root.dataset['built']) {
    root.dataset['built'] = '1';
    root.innerHTML = `
      <div class="page-header">
        <div>
          <div class="page-title">History</div>
          <div class="page-sub">Long-range growth from your recorded stats · hover to read, click to pin two points</div>
        </div>
      </div>
      <div class="history-controls">
        <select class="form-input history-metric" id="hist-metric" title="Metric"></select>
        <div class="history-ranges" id="hist-ranges"></div>
        <div class="history-ranges" id="hist-modes"></div>
        <div class="history-menu-wrap">
          <button type="button" class="history-menu-btn" id="hist-overlays-btn">Overlays ▾</button>
          <div class="history-menu" id="hist-overlays-menu" hidden>
            <div class="history-menu-label">Overlays <span class="history-menu-hint">(one tracker)</span></div>
            <label><input type="checkbox" data-ov="targets"> Targets</label>
            <label><input type="checkbox" data-ov="milestones"> Milestones</label>
            <label><input type="checkbox" data-ov="groupChanges"> Group changes</label>
            <div class="history-menu-label">Series</div>
            <label><input type="checkbox" data-ov="portfolio"> Σ Portfolio</label>
            <label><input type="checkbox" data-ov="projection"> Projection</label>
            <label><input type="checkbox" data-ov="smoothing"> Smoothing</label>
          </div>
        </div>
        <div class="history-menu-wrap">
          <button type="button" class="history-menu-btn" id="hist-export-btn" title="Save the chart as an image">⭳ Save ▾</button>
          <div class="history-menu" id="hist-export-menu" hidden>
            <button type="button" class="history-menu-item" data-export="png">PNG image</button>
            <button type="button" class="history-menu-item" data-export="svg">SVG vector</button>
          </div>
        </div>
      </div>
      <div class="history-tracker-bar">
        <button type="button" class="history-selbtn" id="hist-sel-all">All</button>
        <button type="button" class="history-selbtn" id="hist-sel-none">None</button>
        <div class="history-trackers" id="hist-trackers"></div>
      </div>
      <div class="history-chart-card">
        <div id="hist-chart"></div>
        <div class="history-readout" id="hist-readout"></div>
      </div>`;
    buildControls();
  }
  syncControls();
  if (!resizeWired) {
    resizeWired = true;
    let raf = 0;
    window.addEventListener('resize', () => {
      if (!isActive()) return;
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(drawChart);
    });
  }
  void refetch();
}

function isActive(): boolean {
  const root = document.getElementById('view-history');
  return !!root && root.style.display !== 'none';
}

/** Re-derive the chart from already-fetched data — used after tracker groups
 *  finish loading so target reference lines (which read the group defs) appear
 *  without needing a user interaction. No-op until the view has data. */
/** External pre-filter — the Tracker Detail page's chart click-through sets
 *  the metric + tracker before switching to this view. */
export function presetHistory(metric: string, trackerIds: string[]): void {
  if (HISTORY_METRICS.some(m => m.key === metric)) ui.metric = metric;
  ui.trackers = trackerIds;
  ui.mode = 'value';
  saveUIState();
}

export function redrawHistory(): void {
  if (isActive() && lastResp) drawChart();
}

// ── Controls ─────────────────────────────────────────────────────────────────

function buildControls() {
  const sel = document.getElementById('hist-metric') as HTMLSelectElement;
  sel.innerHTML = HISTORY_METRICS
    .map(m => `<option value="${m.key}">${esc(m.label)}</option>`).join('');
  sel.onchange = () => { ui.metric = sel.value; pins = []; saveUIState(); syncControls(); void refetch(); };

  const ranges = document.getElementById('hist-ranges')!;
  ranges.innerHTML = HISTORY_RANGES
    .map(r => `<button type="button" class="history-range-btn" data-range="${r.key}">${r.label}</button>`).join('');
  ranges.querySelectorAll<HTMLButtonElement>('button').forEach(b => {
    b.onclick = () => { ui.range = b.dataset['range'] as HistoryRangeKey; pins = []; saveUIState(); syncControls(); void refetch(); };
  });

  // Value↔rate is a client-side transform of the fetched series — redraw only.
  const modes = document.getElementById('hist-modes')!;
  modes.innerHTML = `
    <button type="button" class="history-range-btn" data-mode="value">Value</button>
    <button type="button" class="history-range-btn" data-mode="rate">Rate/day</button>`;
  modes.querySelectorAll<HTMLButtonElement>('button').forEach(b => {
    b.onclick = () => { ui.mode = b.dataset['mode'] as 'value' | 'rate'; pins = []; saveUIState(); syncControls(); drawChart(); };
  });

  // Overlays menu — checkboxes drive the ui.* flags; all are pure redraws.
  const ovMenu = document.getElementById('hist-overlays-menu')!;
  ovMenu.querySelectorAll<HTMLInputElement>('input[data-ov]').forEach(cb => {
    cb.onchange = () => {
      (ui as unknown as Record<string, boolean>)[cb.dataset['ov']!] = cb.checked;
      saveUIState(); syncControls(); drawChart();
    };
  });
  wireMenu('hist-overlays-btn', 'hist-overlays-menu');

  // Export menu.
  const exMenu = document.getElementById('hist-export-menu')!;
  exMenu.querySelectorAll<HTMLButtonElement>('button[data-export]').forEach(b => {
    b.onclick = () => { exportChartImage(b.dataset['export'] as 'png' | 'svg'); closeMenus(); };
  });
  wireMenu('hist-export-btn', 'hist-export-menu');

  // Select all / none.
  document.getElementById('hist-sel-all')!.onclick = () => { ui.trackers = null; pins = []; saveUIState(); syncControls(); void refetch(); };
  document.getElementById('hist-sel-none')!.onclick = () => { ui.trackers = []; pins = []; saveUIState(); syncControls(); drawChart(); };
}

/** Toggle a dropdown menu; clicking the button opens it, an outside click or a
 *  second button click closes it. Only one menu open at a time. */
function wireMenu(btnId: string, menuId: string) {
  const btn = document.getElementById(btnId)!;
  const menu = document.getElementById(menuId)!;
  btn.onclick = e => {
    e.stopPropagation();
    const wasOpen = !menu.hidden;
    closeMenus();
    menu.hidden = wasOpen;
  };
}

function closeMenus() {
  document.querySelectorAll<HTMLElement>('.history-menu').forEach(m => (m.hidden = true));
}
// One document-level listener closes any open menu on an outside click.
document.addEventListener('click', () => closeMenus());
// Escape closes an open Overlays/Save menu too — matches detail.ts's Charts menu.
document.addEventListener('keydown', e => { if (e.key === 'Escape') closeMenus(); });

function selectedIds(): string[] {
  const known = new Set(allTrackers.map(t => t.id));
  if (ui.trackers === null) return allTrackers.map(t => t.id);
  return ui.trackers.filter(id => known.has(id)); // [] = none
}

function trackerColor(id: string): string {
  const idx = allTrackers.findIndex(t => t.id === id);
  return PALETTE[(idx < 0 ? 0 : idx) % PALETTE.length];
}

function trackerLabel(t: Tracker): string {
  return fmtTrackerName(t.name, t.abbr ?? '', appSettings.tracker_name_mode || 'name');
}

/** Whether each overlay/series option currently applies, given metric + mode +
 *  selection. Single-tracker overlays need exactly one tracker. */
function overlayApplicable(key: string): boolean {
  const single = selectedIds().length === 1;
  switch (key) {
    case 'targets':      return single && ui.mode === 'value';
    case 'milestones':   return single && ui.mode === 'value';
    case 'groupChanges': return single;
    case 'portfolio':    return isSummableUnit(metricUnit()) && selectedIds().length >= 2;
    // Projection works for every metric now that the rate falls back to the
    // charted points' recent slope (flat/declining stats project too).
    case 'projection':   return ui.mode === 'value';
    case 'smoothing':    return true;
    default:             return true;
  }
}

function syncControls() {
  const sel = document.getElementById('hist-metric') as HTMLSelectElement | null;
  if (sel && sel.value !== ui.metric) sel.value = ui.metric;

  document.querySelectorAll<HTMLButtonElement>('#hist-ranges button').forEach(b =>
    b.classList.toggle('active', b.dataset['range'] === ui.range));
  document.querySelectorAll<HTMLButtonElement>('#hist-modes button').forEach(b =>
    b.classList.toggle('active', b.dataset['mode'] === ui.mode));

  // Overlays checkboxes — reflect state, disable (dim) when not applicable.
  document.querySelectorAll<HTMLInputElement>('#hist-overlays-menu input[data-ov]').forEach(cb => {
    const key = cb.dataset['ov']!;
    cb.checked = !!(ui as unknown as Record<string, boolean>)[key];
    const ok = overlayApplicable(key);
    cb.disabled = !ok;
    cb.closest('label')?.classList.toggle('is-disabled', !ok);
  });
  // A hint when any single-tracker overlay is enabled but multiple are selected.
  const anyAnnot = ui.targets || ui.milestones || ui.groupChanges;
  const hint = document.querySelector('#hist-overlays-menu .history-menu-hint') as HTMLElement | null;
  if (hint) hint.style.color = anyAnnot && selectedIds().length !== 1 ? 'var(--amber)' : '';

  // Select all/none active state.
  const n = selectedIds().length;
  document.getElementById('hist-sel-all')?.classList.toggle('active', ui.trackers === null || n === allTrackers.length);
  document.getElementById('hist-sel-none')?.classList.toggle('active', n === 0);

  const wrap = document.getElementById('hist-trackers');
  if (!wrap) return;
  const active = new Set(selectedIds());
  wrap.innerHTML = allTrackers.map(t => `
    <button type="button" class="history-tracker-chip${active.has(t.id) ? ' active' : ''}" data-id="${esc(t.id)}">
      <span class="history-chip-dot" style="background:var(${trackerColor(t.id)})"></span>${esc(trackerLabel(t))}
    </button>`).join('');
  wrap.querySelectorAll<HTMLButtonElement>('button').forEach(b => {
    b.onclick = () => toggleTracker(b.dataset['id']!);
  });
}

function toggleTracker(id: string) {
  const current = new Set(selectedIds());
  current.has(id) ? current.delete(id) : current.add(id);
  // null when everything is selected (so newly added trackers join in);
  // a plain array otherwise, which may be empty (none).
  ui.trackers = current.size === allTrackers.length ? null : [...current];
  pins = [];
  saveUIState();
  syncControls();
  void refetch();
}

// ── Data + drawing ───────────────────────────────────────────────────────────

async function refetch() {
  // No trackers selected — nothing to fetch (empty trackers means "all" to the
  // API, which we don't want here); show the empty state instead.
  if (selectedIds().length === 0) { drawChart(); return; }
  const seq = ++fetchSeq;
  const chartEl = document.getElementById('hist-chart');
  if (chartEl && !lastResp) {
    chartEl.innerHTML = '<div class="history-empty">Loading…</div>';
  }
  const { ok, data } = await api.fetchHistorySeries({
    trackers: selectedIds(),
    fields: [ui.metric],
    range: ui.range,
  });
  if (seq !== fetchSeq || !isActive()) return; // stale response / view left
  lastResp = ok ? data : null;
  pins = pins.filter(p => lastResp && p >= lastResp.range.from && p <= lastResp.range.to);
  drawChart();
}

const PORTFOLIO_ID = '__portfolio__';

function metricUnit(): SeriesUnit {
  return HISTORY_METRICS.find(m => m.key === ui.metric)?.unit ?? 'count';
}

function portfolioActive(): boolean { return ui.portfolio && overlayApplicable('portfolio'); }
function projectionActive(): boolean { return ui.projection && overlayApplicable('projection'); }
function targetsActive(): boolean { return ui.targets && overlayApplicable('targets'); }
function milestonesActive(): boolean { return ui.milestones && overlayApplicable('milestones'); }
function groupChangesActive(): boolean { return ui.groupChanges && overlayApplicable('groupChanges'); }

/** Promotion/demotion direction from the ordered group defs (rank ascending). */
function groupDirection(defKey: string | undefined, oldName: string, newName: string): ChartEvent['kind'] {
  if (!defKey) return 'neutral';
  const groups = groupDefs[defKey];
  if (!groups) return 'neutral';
  const idx = (name: string) => groups.findIndex(g => g.name.toLowerCase() === name.trim().toLowerCase());
  const a = idx(oldName), b = idx(newName);
  if (a < 0 || b < 0) return 'neutral';
  return b > a ? 'promotion' : b < a ? 'demotion' : 'neutral';
}

/** Group-change markers for the single selected tracker within the window. */
function chartEvents(win: { from: number; to: number }): ChartEvent[] {
  if (!groupChangesActive()) return [];
  const id = selectedIds()[0];
  const defKey = allTrackers.find(t => t.id === id)?.def_key;
  return (lastResp?.events ?? [])
    .filter(e => e.tracker_id === id && e.kind === 'group_change' && e.at >= win.from && e.at <= win.to)
    .map(e => {
      const [oldG = '', newG = ''] = e.detail.split('→');
      return { at: e.at, label: newG.trim(), detail: e.detail, kind: groupDirection(defKey, oldG, newG) };
    });
}

/** Milestone (threshold-crossing) markers for the single selected tracker. */
function chartMilestones(): ChartMilestone[] {
  if (!milestonesActive()) return [];
  const s = chartSeries()[0];
  if (!s || !s.points.length) return [];
  return milestonesFor(s.points, s.unit);
}

function exportChartImage(format: 'png' | 'svg') {
  const el = document.getElementById('hist-chart');
  if (el) exportChart(el, format, `yata-history-${ui.metric}-${ui.range}`);
}

function chartSeries(): ChartSeries[] {
  if (!lastResp) return [];
  const byId = new Map(lastResp.series.map(s => [s.tracker_id, s]));
  const unit = metricUnit();
  let out: ChartSeries[] = selectedIds().flatMap(id => {
    const t = allTrackers.find(tr => tr.id === id);
    const s = byId.get(id);
    if (!t) return [];
    return [{
      id,
      label: trackerLabel(t),
      color: trackerColor(id),
      unit: s?.unit ?? unit,
      points: s?.points ?? [],
    }];
  });
  // Portfolio sums the VALUE series (summing first keeps rate mode honest:
  // the rate of the sum is the sum of the rates).
  if (portfolioActive()) {
    out.push({
      id: PORTFOLIO_ID, label: 'Portfolio', color: '--text2', unit,
      points: portfolioSeries(out.map(s => s.points)),
    });
  }
  if (ui.mode === 'rate') out = out.map(s => ({ ...s, points: toRateSeries(s.points) }));
  return out;
}

/** Per-day rate used for a series' projection tail. Prefers the backend's
 *  stable growth rate (same number behind the dashboard ETAs — only present
 *  for growing stats), then falls back to the signed recent slope of the
 *  charted points, then to 0. A flat or downward tail is still a correct
 *  projection — "nothing is changing" / "this is shrinking" is information,
 *  so the tail always draws (user request 2026-07-12). */
function rateFor(s: ChartSeries): number {
  if (s.id !== PORTFOLIO_ID) {
    const r = statsCache[s.id]?.rates?.[ui.metric];
    if (r != null) return r;
  }
  // Portfolio projects from its own summed points — the sum of member slopes
  // is exactly the slope of the sum, and this handles flat/declining members.
  return recentRatePerDay(s.points) ?? 0;
}

/** The selected tracker's target(s) for the viewed metric as reference lines —
 *  single-tracker value mode only (user request 2026-07-11). Resolution lives
 *  in utils/series.ts (targetRefLinesFor), shared with the Tracker Detail
 *  mini-charts so every surface agrees with the dashboard TARGETS bars. */
function targetRefLines(): ChartRefLine[] {
  if (!targetsActive()) return [];
  const sel = selectedIds();
  const t = allTrackers.find(tr => tr.id === sel[0]);
  if (!t) return [];
  return targetRefLinesFor(t, ui.metric, groupDefs);
}

/** The window actually drawn: the requested range clamped to the oldest
 *  recorded point. A 1y range over 2 months of data starts at the data, not
 *  at ten empty months — and "All" (from=0) would otherwise map the x axis
 *  from the Unix epoch. */
function effectiveWindow(): { from: number; to: number } {
  const to = lastResp!.range.to;
  let from = lastResp!.range.from;
  let oldest = Infinity;
  for (const s of chartSeries()) {
    if (s.points.length && s.points[0][0] < oldest) oldest = s.points[0][0];
  }
  if (isFinite(oldest) && oldest > from) from = oldest;
  return { from, to };
}

function drawChart() {
  const chartEl = document.getElementById('hist-chart');
  if (!chartEl || !lastResp) return;
  if (selectedIds().length === 0) {
    chartEl.innerHTML = `<div class="history-empty">No trackers selected — pick one or more above, or hit “All”.</div>`;
    renderReadout();
    return;
  }
  const series = chartSeries();
  const total = series.reduce((n, s) => n + s.points.length, 0);
  if (total === 0) {
    chartEl.innerHTML = `<div class="history-empty">No ${esc(metricLabel(ui.metric).toLowerCase())} history recorded for this selection yet — check back in a day or two.</div>`;
    renderReadout();
    return;
  }
  const win = effectiveWindow();
  let to = win.to;
  // Drawn series may be smoothed; the readout stays on the raw `series`.
  const drawn = ui.smoothing ? series.map(s => ({ ...s, points: smoothSeries(s.points) })) : series;
  const ghosts: ChartSeries[] = [];
  if (projectionActive()) {
    // Extend the window ~25% (1 day – 90 days) and continue every line at its
    // recent rate — flat and downward tails included (a stat that stopped
    // moving, or is shrinking, projects exactly that). Ghosts draw dashed and
    // ignore the crosshair.
    to = win.to + Math.min(Math.max((win.to - win.from) * 0.25, 86400), 90 * 86400);
    for (const s of drawn) {
      if (!s.points.length) continue;
      const rate = rateFor(s);
      const [lt, lv] = s.points[s.points.length - 1];
      ghosts.push({
        ...s, id: `${s.id}:proj`, ghost: true,
        points: [[lt, lv], [to, lv + rate * ((to - lt) / 86400)]],
      });
    }
  }
  renderChart(chartEl, {
    series: [...drawn, ...ghosts],
    from: win.from,
    to,
    pins,
    refLines: targetRefLines(),
    events: chartEvents(win),
    milestones: chartMilestones(),
    integerTicks: metricUnit() === 'count' && ui.mode === 'value',
    onHover: t => { hoverT = t; renderReadout(); },
    onPin: p => { pins = p; drawChart(); },
  });
  renderReadout();
}

// ── Readout panel ────────────────────────────────────────────────────────────

function renderReadout() {
  const out = document.getElementById('hist-readout');
  if (!out || !lastResp) return;
  const series = chartSeries().filter(s => s.points.length > 0);
  if (!series.length) { out.innerHTML = ''; return; }
  // Format timestamps against the drawn window, not the requested range —
  // "All" over 3 days of data should still show intraday times.
  const win = effectiveWindow();
  const span = win.to - win.from;

  // Header line: what the values refer to.
  const metricName = metricLabel(ui.metric) + (ui.mode === 'rate' ? ' · per-day rate' : '');
  let head: string;
  if (pins.length === 2) {
    head = `${esc(metricName)} · ${esc(fmtPointTime(pins[0], span))} → ${esc(fmtPointTime(pins[1], span))}`;
  } else if (hoverT != null) {
    head = `${esc(metricName)} · ${esc(fmtPointTime(hoverT, span))}`;
  } else if (pins.length === 1) {
    head = `${esc(metricName)} · pinned ${esc(fmtPointTime(pins[0], span))} — click another point for the delta`;
  } else {
    head = `${esc(metricName)} · latest — hover the chart to inspect, click to pin`;
  }

  // In rate mode every value is already a per-day figure.
  const fmtV = (unit: SeriesUnit, v: number) =>
    fmtUnitValue(unit, v) + (ui.mode === 'rate' ? '/day' : '');

  const rows = series.map(s => {
    const swatch = `<span class="history-chip-dot" style="background:var(${s.color})"></span>`;
    if (pins.length === 2) {
      const d = deltaStats(s.points, pins[0], pins[1]);
      if (!d) return `<div class="history-readout-row">${swatch}<span class="hr-name">${esc(s.label)}</span><span class="hr-val">not enough points</span></div>`;
      const sign = d.dv >= 0 ? '+' : '';
      // The per-day tail is redundant (rate mode) — values already are rates.
      const tail = ui.mode === 'rate' ? '' :
        `<span class="hr-rate">≈ ${esc(fmtV(s.unit, d.perDay))}/day over ${d.days.toFixed(d.days < 3 ? 1 : 0)}d</span>`;
      return `<div class="history-readout-row">${swatch}<span class="hr-name">${esc(s.label)}</span>
        <span class="hr-val">${esc(fmtV(s.unit, d.from))} → ${esc(fmtV(s.unit, d.to))}</span>
        <span class="hr-delta ${d.dv >= 0 ? 'up' : 'down'}">${sign}${esc(fmtV(s.unit, d.dv))}</span>
        ${tail}</div>`;
    }
    const t = hoverT ?? pins[0] ?? s.points[s.points.length - 1][0];
    const v = valueAt(s.points, t);
    return `<div class="history-readout-row">${swatch}<span class="hr-name">${esc(s.label)}</span>
      <span class="hr-val">${v == null ? '—' : esc(fmtV(s.unit, v))}</span></div>`;
  }).join('');

  out.innerHTML = `<div class="history-readout-head">${head}</div>${rows}`;
}
