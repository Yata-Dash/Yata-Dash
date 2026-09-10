// components/alertsPanel.ts — the header flag and the alert list behind it.
//
// This is Yata's own alert DESTINATION, not a view over data that already
// existed: before it, an alert with no webhook configured was evaluated,
// matched and dropped (ALERTS_PANEL_PLAN.md §2). So an empty list on a fresh
// install means "nothing has fired", never "nothing was recorded".
import { deleteAlert, fetchAlerts, markAlertsRead } from '../api';
import { trackers } from '../state';
import type { AlertSource, AppAlert } from '../types';
import { esc } from '../utils/format';

/** "just now" / "12m" / "3h" / "5d" — the age of one row. Local because the
 *  app's other relative-time helpers are day-granularity (account deadlines),
 *  and an alert from twenty minutes ago should not read "today". */
function fmtAgo(unixSec: number): string {
  const s = Math.max(0, Math.floor(Date.now() / 1000 - unixSec));
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  if (s < 2592000) return `${Math.floor(s / 86400)}d ago`;
  return new Date(unixSec * 1000).toISOString().slice(0, 10);
}

/** How many rows a page holds. Retention caps the table at 500, so "load
 *  more" is a rare path rather than the normal way to read the list. */
const PAGE = 25;

let open = false;
let search = '';
let filter = '';
let loaded: AppAlert[] = [];
let sources: AlertSource[] = [];
let total = 0;

function el<T extends HTMLElement>(id: string): T | null {
  return document.getElementById(id) as T | null;
}

/** Refresh just the header bubble. Cheap enough to call on every poll. */
export async function refreshAlertCount(): Promise<void> {
  const res = await fetchAlerts({ limit: 1 });
  if (!res.ok) return;
  setCount(res.data.unread);
}

function setCount(n: number): void {
  const badge = el('alerts-count');
  const btn = el('alerts-btn');
  if (!badge || !btn) return;
  badge.textContent = n > 99 ? '99+' : String(n);
  badge.hidden = n === 0;
  btn.classList.toggle('has-unread', n > 0);
  btn.title = n === 0 ? 'Alerts' : `Alerts — ${n} unread`;
}

/** Reload the list from the top, honouring the current search/filter. */
async function load(): Promise<void> {
  const res = await fetchAlerts({ q: search, tracker: filter, limit: PAGE });
  if (!res.ok) {
    const list = el('alerts-list');
    if (list) list.innerHTML = `<div class="alerts-empty">Couldn${'’'}t load alerts.</div>`;
    return;
  }
  loaded = res.data.alerts;
  sources = res.data.sources;
  total = res.data.total;
  setCount(res.data.unread);
  render();
}

async function loadMore(): Promise<void> {
  const res = await fetchAlerts({ q: search, tracker: filter, limit: PAGE, offset: loaded.length });
  if (!res.ok) return;
  loaded = loaded.concat(res.data.alerts);
  total = res.data.total;
  render();
}

/** The filter's options, from the server's UNFILTERED source list.
 *
 *  Built from the loaded page instead, it collapsed to whichever tracker was
 *  already selected — so once you filtered you could not reach any other
 *  tracker without clearing first. The option set has to describe every source,
 *  not the view it is producing. */
function renderFilter(): void {
  const sel = el<HTMLSelectElement>('alerts-filter');
  if (!sel) return;
  // Live tracker names win over the stored ones, so a rename shows through;
  // a removed tracker keeps the name its alerts were filed under.
  const opts = [`<option value="">All sources</option>`].concat(
    sources.map(s => s.tracker_id
      ? `<option value="${esc(s.tracker_id)}">${esc(trackers.find(t => t.id === s.tracker_id)?.name || s.tracker_name)}</option>`
      : `<option value="app">Yata itself</option>`));
  const markup = opts.join('');
  // Only touch the DOM when the options actually changed: rewriting innerHTML
  // while the select is open closes it under the user's cursor.
  if (sel.innerHTML !== markup) sel.innerHTML = markup;
  if (sel.value !== filter) sel.value = filter;
}

function row(a: AppAlert): string {
  // The title is "Yata alert: <rule>" — the prefix is for a webhook arriving
  // out of context, and just noise inside Yata's own panel.
  const heading = a.title.replace(/^Yata alert:\s*/, '') || a.rule_name;
  const where = a.tracker_name || 'Yata';
  return `<div class="alert-row${a.read_at ? '' : ' unread'}" data-id="${a.id}">
    <div class="alert-row-main">
      <div class="alert-row-top">
        <span class="alert-rule">${esc(heading)}</span>
        <span class="alert-when" title="${esc(new Date(a.at * 1000).toLocaleString())}">${esc(fmtAgo(a.at))}</span>
      </div>
      <div class="alert-body">${esc(a.body)}</div>
      <div class="alert-where">${esc(where)}</div>
    </div>
    <button type="button" class="alert-clear" data-clear="${a.id}" title="Clear this alert" aria-label="Clear">&times;</button>
  </div>`;
}

function render(): void {
  const list = el('alerts-list');
  if (!list) return;
  if (loaded.length === 0) {
    // Two different nothings: no alerts at all, versus none matching a filter.
    list.innerHTML = search || filter
      ? `<div class="alerts-empty">No alerts match that.</div>`
      : `<div class="alerts-empty">No alerts yet.<br><span class="alerts-empty-sub">Rules that fire will appear here, whether or not you${'’'}ve set up a webhook.</span></div>`;
  } else {
    list.innerHTML = loaded.map(row).join('');
  }
  const more = el('alerts-more');
  if (more) {
    more.hidden = loaded.length >= total;
    more.innerHTML = more.hidden ? ''
      : `<button type="button" class="btn btn-ghost btn-sm" id="alerts-load-more">Show older (${total - loaded.length} more)</button>`;
  }
  renderFilter();
}

function toggle(force?: boolean): void {
  const panel = el('alerts-panel');
  const btn = el('alerts-btn');
  if (!panel || !btn) return;
  open = force ?? !open;
  panel.hidden = !open;
  btn.setAttribute('aria-expanded', String(open));
  if (open) {
    void load();
    el<HTMLInputElement>('alerts-search')?.focus();
  }
}

/** Wire the panel up once, at startup. */
export function initAlertsPanel(): void {
  el('alerts-btn')?.addEventListener('click', e => { e.stopPropagation(); toggle(); });

  // Click-away and Escape close it — it overlays the dashboard, so leaving it
  // open would sit on top of the thing the user just went back to reading.
  document.addEventListener('click', e => {
    if (!open) return;
    const panel = el('alerts-panel');
    if (panel && !panel.contains(e.target as Node)) toggle(false);
  });
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape' && open) toggle(false);
  });

  let timer: number | undefined;
  el<HTMLInputElement>('alerts-search')?.addEventListener('input', ev => {
    search = (ev.target as HTMLInputElement).value;
    window.clearTimeout(timer);
    timer = window.setTimeout(() => { void load(); }, 200);
  });

  el<HTMLSelectElement>('alerts-filter')?.addEventListener('change', ev => {
    filter = (ev.target as HTMLSelectElement).value;
    void load();
  });

  el('alerts-read-all')?.addEventListener('click', async () => {
    const res = await markAlertsRead();
    if (res.ok) setCount(res.data.unread);
    // Reload rather than just restyling: read_at now has a value, and the row
    // classes come from it.
    void load();
  });

  el('alerts-list')?.addEventListener('click', async e => {
    const clear = (e.target as HTMLElement).closest<HTMLElement>('[data-clear]');
    if (!clear) return;
    e.stopPropagation();
    const id = Number(clear.dataset['clear']);
    const res = await deleteAlert(id);
    if (res.ok) setCount(res.data.unread);
    loaded = loaded.filter(a => a.id !== id);
    total = Math.max(0, total - 1);
    render();
  });

  el('alerts-more')?.addEventListener('click', e => {
    if ((e.target as HTMLElement).id === 'alerts-load-more') void loadMore();
  });

  void refreshAlertCount();
}
