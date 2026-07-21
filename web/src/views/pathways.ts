// views/pathways.ts — invite-pathways view (target selector + ranked path chains)
//
// Data source: GET /api/pathways/targets (404 = feature off → view button
// stays hidden) and GET /api/pathways/paths?target=… per selection. The
// dataset is community-driven; the disclosure footer is REQUIRED and always
// visible. Selection persists in localStorage.
import * as api from '../api';
import { appSettings, trackers } from '../state';
import { esc, fmtEtaDays } from '../utils/format';
import { unavailEyeSvg } from '../utils/icons';
import { getFaviconUrl } from '../utils/parse';
import type {
  PathwayClassEval, PathwayPath, PathwayPathsResponse, PathwayReqProgress,
  PathwaySource, PathwayStep, PathwayTarget,
} from '../types';

const TARGET_KEY = 'u3d-pathway-target';
const FILTER_KEY = 'u3d-pathway-filter';

// ── Module state ──────────────────────────────────────────────────────────
let targets: PathwayTarget[] = [];
let source: PathwaySource | null = null;
let selected = '';
let lastResult: PathwayPathsResponse | null = null;
let expandedSteps = new Set<string>(); // "pathIdx:stepIdx"
let pathsSeq = 0; // guards out-of-order responses when switching targets fast
let listFilter: 'all' | 'met' | 'fav' =
  (localStorage.getItem(FILTER_KEY) as 'all' | 'met' | 'fav') || 'all';
let lastComboFilter = ''; // the text filter of the currently rendered list

// ── Favourites / not-interested (server-side settings, survive browsers) ──

function favList(): string[] { return appSettings.pathway_favorites ?? []; }
function hiddenList(): string[] { return appSettings.pathway_not_interested ?? []; }
function isFav(name: string): boolean { return favList().includes(name); }
function isHidden(name: string): boolean { return hiddenList().includes(name); }

/** Toggle a list membership; the two lists are mutually exclusive. */
function togglePathwayList(name: string, which: 'fav' | 'hide') {
  const favs = new Set(favList());
  const hid = new Set(hiddenList());
  if (which === 'fav') {
    if (favs.has(name)) favs.delete(name);
    else { favs.add(name); hid.delete(name); }
  } else {
    if (hid.has(name)) hid.delete(name);
    else { hid.add(name); favs.delete(name); }
  }
  appSettings.pathway_favorites = [...favs].sort();
  appSettings.pathway_not_interested = [...hid].sort();
  void api.saveSettings({ ...appSettings });
  renderComboList(lastComboFilter); // reflect immediately, list stays open
}

// ── ETA gating (per spec) ─────────────────────────────────────────────────
// fmtEtaDays now lives in utils/format.ts (shared with the dashboard targets).

/** "Estimated time to reach" (Settings → Display) — gates overall arrival
 *  estimates: path/class ETAs, the minimum note, account-age countdowns, and
 *  the explanation line. Bars/✓/? always show. */
function showEtas(): boolean {
  return appSettings.show_pathway_etas !== false;
}

/** "Stat trend estimates" (Settings → Display) — gates the per-stat 7-day
 *  projection chips (uploaded / seed_size / bonus / seedtime). */
function showTrends(): boolean {
  return appSettings.show_trend_estimates !== false;
}

/** Per-stat projection kinds driven by 7-day growth rates. Account age is
 *  exact (not a trend projection) and belongs to the time-to-reach toggle. */
const TREND_KINDS = new Set(['uploaded', 'seed_size', 'bonus', 'seedtime']);

/** Whether a requirement's eta chip should show, given the two toggles.
 *  Account age (exact "in X") follows show_pathway_etas; trend-projected
 *  stats follow show_trend_estimates; anything else follows show_pathway_etas. */
function etaChipVisible(q: PathwayReqProgress): boolean {
  if (q.kind && TREND_KINDS.has(q.kind)) return showTrends();
  return showEtas();
}

/** One requirement's time chip: account age is EXACT (plain "in X"), data
 *  stats are projections ("≈ X"), floors carry a "+" suffix.
 *  Estimated hops (after the first) never show per-requirement chips — the
 *  user only acts on the first hop; later hops only feed the total. */
function reqEtaChip(q: PathwayReqProgress, estimated: boolean): string {
  if (estimated || !etaChipVisible(q) || q.met || q.eta_days < 0) return '';
  if (q.eta_days === 0 && !q.has_unknown) return '';
  const v = fmtEtaDays(q.eta_days) + (q.has_unknown ? '+' : '');
  return `<span class="pw-req-eta">${q.kind === 'age' ? `in ${v}` : `≈ ${v}`}</span>`;
}

/** Bar requirements (and class containers) sort before plain no-bar rows,
 *  so requirements without a progress bar (e.g. "Uploads: 5") drop to the
 *  bottom of the list. */
function reqSortKey(q: PathwayReqProgress): number {
  // Class and "one of" blocks stay at the top even when they can't be
  // evaluated — they're the headline of the route, not a trailing detail.
  if (q.classes?.length || q.any_of?.length || q.unavail === 'class') return 0;
  const isBar = !!q.kind && (q.need ?? 0) > 0 && (q.have ?? -1) >= 0;
  return isBar ? 0 : 1;
}

function sortedReqs(reqs: PathwayReqProgress[]): PathwayReqProgress[] {
  return [...reqs].sort((a, b) => reqSortKey(a) - reqSortKey(b)); // stable
}

// ── Init ──────────────────────────────────────────────────────────────────

/**
 * Load the targets list. Returns false when the feature is off (404 / no
 * data) — the caller leaves the Pathways button hidden in that case.
 */
export async function initPathways(): Promise<boolean> {
  const { ok, data } = await api.fetchPathwayTargets();
  if (!ok || !Array.isArray(data?.targets)) return false;
  targets = data.targets;
  source = data.source;

  const btn = document.getElementById('btn-pathways-view');
  if (btn) btn.style.display = '';
  renderDisclosure();
  wireCombo();
  wireBody();

  // Restore the last selection (persisted like the view choice).
  const saved = localStorage.getItem(TARGET_KEY) ?? '';
  if (saved && targets.some(t => t.name === saved)) void selectTarget(saved);
  return true;
}

// ── Target combo (filterable dropdown) ────────────────────────────────────

function comboInput(): HTMLInputElement | null {
  return document.getElementById('pw-target-input') as HTMLInputElement | null;
}

function wireCombo() {
  const input = comboInput();
  const list = document.getElementById('pw-combo-list');
  if (!input || !list) return;

  input.addEventListener('focus', () => {
    input.select();
    renderComboList(''); // full list on focus; typing filters
    list.style.display = 'block';
  });
  input.addEventListener('input', () => {
    renderComboList(input.value.trim());
    list.style.display = 'block';
  });
  input.addEventListener('keydown', e => {
    if (e.key === 'Escape') closeComboList();
    if (e.key === 'Enter') {
      // Pick the first selectable entry matching the filter.
      const first = list.querySelector<HTMLElement>('.pw-combo-item:not(.disabled)');
      if (first?.dataset['name']) { void selectTarget(first.dataset['name']); closeComboList(); }
    }
  });
  document.addEventListener('mousedown', e => {
    if (!(e.target as HTMLElement).closest?.('.pw-combo')) closeComboList();
  });
  list.addEventListener('click', e => {
    // Star / not-interested buttons toggle their list without selecting.
    const act = (e.target as HTMLElement).closest<HTMLElement>('.pw-act');
    if (act?.dataset['name'] && act.dataset['act']) {
      e.stopPropagation();
      togglePathwayList(act.dataset['name'], act.dataset['act'] as 'fav' | 'hide');
      return;
    }
    // Filter chips re-render the open list.
    const chip = (e.target as HTMLElement).closest<HTMLElement>('.pw-chip-filter');
    if (chip?.dataset['filter']) {
      listFilter = chip.dataset['filter'] as typeof listFilter;
      localStorage.setItem(FILTER_KEY, listFilter);
      renderComboList(lastComboFilter);
      return;
    }
    const item = (e.target as HTMLElement).closest<HTMLElement>('.pw-combo-item');
    if (!item || item.classList.contains('disabled') || !item.dataset['name']) return;
    void selectTarget(item.dataset['name']);
    closeComboList();
  });
}

function closeComboList() {
  const list = document.getElementById('pw-combo-list');
  if (list) list.style.display = 'none';
  const input = comboInput();
  if (input) input.value = selected; // restore the committed selection text
}

function renderComboList(filter: string) {
  const list = document.getElementById('pw-combo-list');
  if (!list) return;
  lastComboFilter = filter;
  const f = filter.toLowerCase();
  const match = (t: PathwayTarget) =>
    !f || t.name.toLowerCase().includes(f) || (t.abbr ?? '').toLowerCase().includes(f);
  const byName = (a: PathwayTarget, b: PathwayTarget) => a.name.localeCompare(b.name);
  const chip = (t: PathwayTarget) => {
    if (listFilter === 'met') return !!t.reqs_met;
    if (listFilter === 'fav') return isFav(t.name);
    return true;
  };

  // Order: favourites → the rest → not-interested → already joined.
  // Not-interested is excluded from the requirements-met filter entirely
  // (meeting a music tracker's bar doesn't mean you want in), and the two
  // bottom sections only render in the unfiltered view.
  const notMine = targets.filter(t => !t.is_mine && match(t));
  const favs   = notMine.filter(t => isFav(t.name) && chip(t)).sort(byName);
  const avail  = notMine.filter(t => !isFav(t.name) && !isHidden(t.name) && chip(t)).sort(byName);
  const hidden = listFilter === 'all' ? notMine.filter(t => isHidden(t.name)).sort(byName) : [];
  const mine   = listFilter === 'all' ? targets.filter(t => t.is_mine && match(t)).sort(byName) : [];

  const metDot = (t: PathwayTarget) => t.reqs_met
    ? '<span class="pw-met-dot" title="Meets the listed requirements on a direct route — community data, not a guarantee of an invite">✓ reqs met</span>'
    : '';
  const item = (t: PathwayTarget, dimmed = false) => `
    <div class="pw-combo-item${t.is_mine ? ' disabled' : ''}${t.name === selected ? ' selected' : ''}${dimmed ? ' pw-item-muted' : ''}" data-name="${esc(t.name)}">
      <span class="pw-combo-name">${esc(t.name)}${t.abbr ? ` <span class="pw-combo-abbr">[${esc(t.abbr)}]</span>` : ''}</span>
      ${dimmed ? '' : metDot(t)}
      ${t.def_key ? '<span class="pw-def-badge">def</span>' : ''}
      ${t.is_mine ? '<span class="pw-combo-note">(already joined)</span>' : `
      <span class="pw-item-actions">
        <button type="button" class="pw-act pw-act-fav${isFav(t.name) ? ' on' : ''}" data-act="fav" data-name="${esc(t.name)}"
                title="${isFav(t.name) ? 'Remove from favourites' : 'Favourite — keep at the top of the list'}">${isFav(t.name) ? '★' : '☆'}</button>
        <button type="button" class="pw-act pw-act-hide${dimmed ? ' on' : ''}" data-act="hide" data-name="${esc(t.name)}"
                title="${dimmed ? 'Restore to the main list' : 'Not interested — move to the bottom'}"><i class="fas ${dimmed ? 'fa-rotate-left' : 'fa-eye-slash'}"></i></button>
      </span>`}
    </div>`;

  const chips = `<div class="pw-combo-chips">
    <button type="button" class="pw-chip-filter${listFilter === 'all' ? ' on' : ''}" data-filter="all">All</button>
    <button type="button" class="pw-chip-filter${listFilter === 'met' ? ' on' : ''}" data-filter="met">Requirements met</button>
    <button type="button" class="pw-chip-filter${listFilter === 'fav' ? ' on' : ''}" data-filter="fav">★ Favourites</button>
  </div>`;

  const rows = favs.map(t => item(t)).join('') + avail.map(t => item(t)).join('');
  list.innerHTML = chips
    + (rows || '<div class="pw-combo-empty">No matching trackers</div>')
    + (hidden.length ? `<div class="pw-combo-sep">Not interested</div>${hidden.map(t => item(t, true)).join('')}` : '')
    + (mine.length ? `<div class="pw-combo-sep">Already joined</div>${mine.map(t => item(t)).join('')}` : '');
}

// ── Selection → paths ─────────────────────────────────────────────────────

async function selectTarget(name: string) {
  selected = name;
  localStorage.setItem(TARGET_KEY, name);
  const input = comboInput();
  if (input) input.value = name;
  expandedSteps = new Set();

  const body = document.getElementById('pw-body');
  if (!body) return;
  body.innerHTML = '<div class="pw-loading">Finding paths…</div>';

  const seq = ++pathsSeq;
  const { ok, data } = await api.fetchPathwayPaths(name);
  if (seq !== pathsSeq) return; // a newer selection superseded this request
  if (!ok || !data?.target) {
    lastResult = null;
    body.innerHTML = `<div class="pw-notice"><p class="pw-notice-title">Could not load paths for ${esc(name)}.</p></div>`;
    return;
  }
  lastResult = data;
  if (data.source?.url) { source = data.source; renderDisclosure(); }
  renderPaths();
}

// ── Paths rendering ───────────────────────────────────────────────────────

function wireBody() {
  // Delegated click → expand/collapse step chips.
  document.getElementById('pw-body')?.addEventListener('click', e => {
    const chip = (e.target as HTMLElement).closest<HTMLElement>('[data-step-key]');
    if (!chip?.dataset['stepKey']) return;
    const key = chip.dataset['stepKey'];
    if (expandedSteps.has(key)) expandedSteps.delete(key); else expandedSteps.add(key);
    renderPaths();
  });
}

function renderPaths() {
  const body = document.getElementById('pw-body');
  if (!body) return;
  if (!lastResult) { body.innerHTML = ''; return; }
  const paths = lastResult.paths ?? [];
  body.innerHTML = paths.length
    ? paths.map((p, i) => renderPathCard(p, i)).join('')
    : renderNoPath(lastResult);
  renderDisclosure(); // keep the footer explainer in sync with the rendered paths
}

/** Walk a path's requirement tree for an account-age requirement we couldn't
 *  evaluate (e.g. the tracker exposes no join date) — distinct from an age
 *  requirement that's simply met. */
function pathHasUnknownAge(p: PathwayPath): boolean {
  const rowsUnknownAge = (rows: PathwayReqProgress[]): boolean => rows.some(q => {
    if (q.classes?.length) {
      return q.classes.some(ce =>
        rowsUnknownAge(ce.reqs) || (ce.any_of ?? []).some(alt => rowsUnknownAge(alt)));
    }
    if (q.any_of?.length) return !q.met && q.any_of.some(alt => rowsUnknownAge(alt));
    return q.kind === 'age' && !q.met && (q.eta_days < 0 || (q.have ?? -1) < 0);
  });
  return p.steps.some(s => rowsUnknownAge(s.reqs ?? []));
}

function pathEtaLabel(p: PathwayPath): string {
  if (p.total_eta_days === 0 && !p.has_unknown) return 'Ready now';
  if (p.total_eta_days === 0 && p.has_unknown) {
    // Can't compute the age floor (join date unknown) vs. age met, only
    // controllable stat targets left.
    return pathHasUnknownAge(p) ? 'Timeline unknown' : '0D+ - Only stats left';
  }
  // The number is the account-age minimum (the one thing you can't speed up);
  // "+" means other, controllable requirements may make the real time longer.
  return `${fmtEtaDays(p.total_eta_days)}${p.has_unknown ? '+' : ''}`;
}

/** Shortened "how estimates work" text — shown once in the footer when EITHER
 *  estimate toggle is on and there are paths. */
function etaExplainHtml(): string {
  if (!showEtas() && !showTrends()) return '';
  return `<b>How estimates work:</b> the headline is your <b>account-age minimum</b> (from your join date) — the one
    thing you can't speed up. A trailing <b>"+"</b> means other requirements (upload, ratio, seed size …) are still
    unmet, so it may take longer. Per-stat "≈" times project your last 7 days of growth; later hops use community
    averages. Not guarantees — turn off in Settings → Display.`;
}

function renderPathCard(p: PathwayPath, idx: number): string {
  const ready = p.total_eta_days === 0 && !p.has_unknown;
  const start = trackers.find(t => t.id === p.start_tracker_id);
  const favUrl = start?.url ? getFaviconUrl(start.url) : '';
  const startLabel = start?.name || p.start_name;

  const chips: string[] = [`
    <span class="pw-chip pw-chip-start" title="Your tracker">
      ${favUrl ? `<img class="pw-chip-favicon" src="${esc(favUrl)}" alt="" onerror="this.style.display='none'">` : ''}
      ${esc(startLabel)}
    </span>`];

  p.steps.forEach((s, si) => {
    const key = `${idx}:${si}`;
    const isTarget = si === p.steps.length - 1;
    chips.push(`<span class="pw-arrow">→</span>
      <span class="pw-chip pw-chip-step${isTarget ? ' pw-chip-target' : ''}${expandedSteps.has(key) ? ' expanded' : ''}"
            data-step-key="${key}" title="Click for route details">
        ${esc(s.to)}${s.estimated ? '<span class="pw-chip-est" title="Estimate — no live stats beyond the first hop">≈</span>' : ''}
        <svg class="pw-chip-chev" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="6 9 12 15 18 9"/></svg>
      </span>`);
  });

  const details = p.steps
    .map((s, si) => expandedSteps.has(`${idx}:${si}`) ? renderStepDetail(s) : '')
    .join('');

  const etaChip = showEtas() || ready
    ? `<span class="pw-eta${ready ? ' pw-eta-ready' : ''}">${pathEtaLabel(p)}</span>`
    : '';
  // The "+" suffix and the bottom explanation line already convey that the
  // headline is an account-age minimum — no inline note needed.
  return `<div class="pw-path-card${idx === 0 ? ' pw-best' : ''}">
    <div class="pw-path-head">
      ${idx === 0 ? '<span class="pw-best-badge">Best path</span>' : ''}
      ${etaChip}
      <span class="pw-hops">${p.steps.length} hop${p.steps.length === 1 ? '' : 's'}</span>
    </div>
    <div class="pw-chain">${chips.join('')}</div>
    ${details}
  </div>`;
}

function renderStepDetail(s: PathwayStep): string {
  const reqs = sortedReqs(s.reqs ?? []);
  const est = s.estimated;
  return `<div class="pw-step-detail">
    <div class="pw-step-detail-head">
      <span class="pw-step-route">${esc(s.from)} → ${esc(s.to)}</span>
      ${est ? '<span class="pw-est-badge" title="No live stats beyond the first hop — based on community data only">estimate</span>' : ''}
      ${s.updated ? `<span class="pw-updated">data from ${esc(s.updated)}</span>` : ''}
    </div>
    ${s.reqs_raw
      ? `<div class="pw-reqs-raw">${esc(s.reqs_raw)}</div>`
      : '<div class="pw-reqs-raw pw-reqs-raw-empty">No requirement text listed for this route.</div>'}
    ${s.def_note ? `<div class="pw-def-note"><i class="fas fa-circle-info" style="margin-right:5px"></i>${esc(s.def_note)}</div>` : ''}
    ${reqs.length ? `<ul class="pw-req-list">${reqs.map(q => reqRow(q, est)).join('')}</ul>` : ''}
  </div>`;
}

function reqRow(q: PathwayReqProgress, estimated: boolean): string {
  // "Reach class X (or Y)" — render the full per-class breakdown with
  // progress bars for every requirement the class itself demands.
  if (q.classes?.length) {
    return `<li class="pw-req pw-req-classes">
      <div class="pw-class-req-label"${q.note ? ` title="${esc(q.note)}"` : ''}>
        ${statusIcon(q)} <span class="pw-req-label">Reach ${esc(q.label)}</span>
        ${reqEtaChip(q, estimated)}
        ${q.note ? '<span class="pw-plus-hint" title="' + esc(q.note) + '">ⓘ</span>' : ''}
      </div>
      ${q.classes.map(ce => classSection(ce, estimated)).join('')}
    </li>`;
  }
  // "9 months or 6 months and 10+ uploads" — alternatives, any ONE of which
  // satisfies the route. Same block the class breakdowns use for their
  // any_of sets, so the two read identically.
  if (q.any_of?.length) {
    return `<li class="pw-req pw-req-anyof">
      <div class="pw-class-req-label"${q.note ? ` title="${esc(q.note)}"` : ''}>
        ${statusIcon(q)} <span class="pw-req-label">${esc(q.label)}</span>
        ${reqEtaChip(q, estimated)}
      </div>
      <div class="anyof-wrap">
        <div class="anyof-label">One of</div>
        ${q.any_of.map(alt =>
          `<div class="anyof-alt">${sortedReqs(alt).map(r => barOrTextRow(r, 'div', estimated)).join('')}</div>`
        ).join('<div class="anyof-or">or</div>')}
      </div>
    </li>`;
  }
  // Class requirement with no def data behind it — keep the "Reach X" wording
  // of an evaluated class row, but render it as unavailable.
  if (q.unavail === 'class') return barOrTextRow({ ...q, label: `Reach ${q.label}` }, 'li', estimated);
  return barOrTextRow(q, 'li', estimated);
}

function statusIcon(q: { met: boolean; eta_days: number }): string {
  if (q.met) return '<span class="pw-req-icon pw-req-icon--met">✓</span>';
  // In-progress / estimable rows render no leading glyph — the progress bar and
  // eta chip carry that information. Only truly-unknown rows get a "?".
  if (q.eta_days >= 0) return '';
  return '<span class="pw-req-icon pw-req-icon--unknown">?</span>';
}

/** Short reason shown in place of a progress bar, per unavailable kind. The
 *  backend's note carries the long form as the row's tooltip. */
const UNAVAIL_TEXT: Record<NonNullable<PathwayReqProgress['unavail']>, string> = {
  stat: 'Not available',
  text: 'Manual check',
  class: 'No group data',
};

const UNAVAIL_FALLBACK_TIP: Record<NonNullable<PathwayReqProgress['unavail']>, string> = {
  stat: 'This tracker doesn’t report this stat, so progress can’t be tracked — the requirement still applies.',
  text: 'Not a stat Yata can measure — check this one on the tracker.',
  class: 'Yata has no group requirements for this tracker, so class progress can’t be shown.',
};

/** A single requirement — quantitative bar when have/need are known,
 *  plain text row otherwise. */
function barOrTextRow(q: PathwayReqProgress, tag: 'li' | 'div', estimated: boolean): string {
  const open = tag === 'li' ? '<li' : '<div';
  const close = tag === 'li' ? '</li>' : '</div>';
  const title = q.note ? ` title="${esc(q.note)}"` : '';

  // Unmeasurable requirement — mirror an untrackable target row (reason +
  // empty dashed track) instead of a bare "?". These are NOT met: an invite
  // requirement Yata can't see still has to be satisfied on the tracker.
  if (q.unavail) {
    const tip = q.note || UNAVAIL_FALLBACK_TIP[q.unavail];
    const need = q.need_text ? ` <span class="tgt">/ ${esc(q.need_text)}</span>` : '';
    return `${open} class="pw-req pw-req--unavail" title="${esc(tip)}">
      <div class="target-header">
        <span class="target-lbl">${esc(q.label)}</span>
        <span class="pw-unavail-vals">${unavailEyeSvg('pw-unavail-icon')}<span class="pw-unavail-txt">${UNAVAIL_TEXT[q.unavail]}</span>${need}</span>
      </div>
      <div class="pw-track--unavail"></div>
    ${close}`;
  }

  if (q.kind && (q.need ?? 0) > 0 && (q.have ?? -1) >= 0) {
    const pct = Math.min(100, ((q.have ?? 0) / (q.need ?? 1)) * 100);
    const color = q.met ? 'green' : pct >= 60 ? 'amber' : 'red';
    // A bar row carries its own progress — show a ✓ only when met, never a
    // "?" (the bar already shows how far along it is).
    const icon = q.met ? '<span class="pw-req-icon pw-req-icon--met">✓</span> ' : '';
    return `${open} class="pw-req pw-req-bar${q.met ? ' pw-req--met' : ''}"${title}>
      <div class="target-header">
        <span class="target-lbl">${icon}${esc(q.label)}</span>
        <span class="target-vals">${esc(q.have_text ?? '')} <span class="tgt">/ ${esc(q.need_text ?? '')}</span>${reqEtaChip(q, estimated)}</span>
      </div>
      <div class="progress-track"><div class="progress-fill ${color}" style="width:${pct.toFixed(1)}%"></div></div>
    ${close}`;
  }

  const cls = q.met ? 'met' : q.eta_days >= 0 ? 'eta' : 'unknown';
  return `${open} class="pw-req pw-req--${cls}"${title}>
    ${statusIcon(q)}
    <span class="pw-req-label">${esc(q.label)}</span>
    ${reqEtaChip(q, estimated)}
  ${close}`;
}

/** One class alternative: header + base requirement bars + any_of groups. */
function classSection(ce: PathwayClassEval, estimated: boolean): string {
  const head = `<div class="pw-class-head">
    <span class="pw-class-name">${esc(ce.name)}</span>
    ${ce.fastest ? '<span class="pw-fastest-badge" title="Fastest estimable alternative">fastest</span>' : ''}
    ${ce.met ? '<span class="pw-req-icon pw-req-icon--met">✓ met</span>'
      : (!estimated && showEtas() && ce.eta_days > 0) ? `<span class="pw-req-eta">≈ ${fmtEtaDays(ce.eta_days)}${ce.has_unknown ? '+' : ''}</span>` : ''}
  </div>`;
  const base = sortedReqs(ce.reqs).map(r => barOrTextRow(r, 'div', estimated)).join('');
  const anyOf = (ce.any_of ?? []).length
    ? `<div class="anyof-wrap">
        <div class="anyof-label">One of</div>
        ${(ce.any_of ?? []).map(alt =>
          `<div class="anyof-alt">${sortedReqs(alt).map(r => barOrTextRow(r, 'div', estimated)).join('')}</div>`
        ).join('<div class="anyof-or">or</div>')}
      </div>`
    : '';
  return `<div class="pw-class-section">${head}${base}${anyOf}</div>`;
}

// ── No-path state ─────────────────────────────────────────────────────────

function renderNoPath(res: PathwayPathsResponse): string {
  const sugg = res.suggestions ?? [];
  if (!sugg.length) {
    return `<div class="pw-notice">
      <p class="pw-notice-title">No direct path found from your current trackers.</p>
      <p class="pw-notice-sub">No active invite routes into ${esc(res.target)} are listed in the dataset.</p>
    </div>`;
  }
  const rows = sugg.map(s => `
    <div class="pw-suggestion">
      <div class="pw-suggestion-head">
        <span class="pw-suggestion-name">${esc(s.name)}</span>
        <span class="pw-suggestion-days">${
          s.days > 0 ? `min account age ${fmtEtaDays(s.days)}`
          : s.days === 0 ? 'no account-age requirement'
          : 'account age requirement unknown'}</span>
        ${s.updated ? `<span class="pw-updated">data from ${esc(s.updated)}</span>` : ''}
      </div>
      ${s.reqs ? `<div class="pw-reqs-raw">${esc(s.reqs)}</div>` : ''}
    </div>`).join('');
  return `<div class="pw-notice">
    <p class="pw-notice-title">No direct path found from your current trackers.</p>
    <p class="pw-notice-sub">Open registration or other invite routes are required to start one of these:</p>
  </div>
  <div class="pw-suggestions">${rows}</div>`;
}

// ── Disclosure (REQUIRED — always visible at the bottom of the view) ──────

function renderDisclosure() {
  const el = document.getElementById('pw-disclosure');
  if (!el || !source) return;
  const hasPaths = (lastResult?.paths?.length ?? 0) > 0;
  const explain = hasPaths ? etaExplainHtml() : '';
  const credit = `Pathway data from <a href="${esc(source.url)}" target="_blank" rel="noopener noreferrer">${esc(source.name || 'trackerpathways')}</a> (MIT), fetched ${esc(source.fetched)}. Community-driven — may be incorrect or out of date. Reference only: meeting listed requirements does NOT guarantee an invite. Routes marked "+" (e.g. Titan+) mean that class or higher — official invites can carry extra requirements beyond the class itself.`;
  // Estimates explainer (when shown) sits above the source credit, separated by
  // a divider so there's a clear line break before the GitHub attribution.
  el.innerHTML = explain
    ? `<div>${explain}</div><div class="pw-disclosure-credit">${credit}</div>`
    : credit;
}
