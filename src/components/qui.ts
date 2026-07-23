// components/qui.ts — qBittorrent UI live stats bars
import type { AppSettings, QUIInstanceMeta } from '../types';
import { esc, fmtBytes, fmtSpeed, ratioColor } from '../utils/format';
import * as api from '../api';

/** Build the scaffold HTML for one QUI instance bar */
export function buildQuiBarHTML(instId: number, instName: string): string {
  const n = esc(instName || `Instance ${instId}`);
  return `<div class="qui-bar qui-inst-bar" data-inst-id="${instId}">
    <div class="qui-section">
      <div class="qui-bar-label">
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>
        </svg>${n}
      </div>
      <div class="qui-inst-conn" style="display:flex;align-items:center;gap:5px">
        <div class="sdot amber qi-dot" style="width:6px;height:6px;flex-shrink:0"></div>
        <span class="qi-conn-label" style="font-size:11px;color:var(--text3);white-space:nowrap">—</span>
      </div>
    </div>
    <div class="qui-bar-sep"></div>
    <div class="qui-section">
      <div class="qui-speed-group">
        <div class="qui-speed-pill">
          <svg class="qui-icon-dl" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke-width="2.5" stroke-linecap="round">
            <polyline points="22 17 13.5 8.5 8.5 13.5 2 7"/><polyline points="16 17 22 17 22 11"/>
          </svg>
          <span class="qui-speed-label">DL</span>
          <span class="qui-speed-value dn qi-dl-speed">—</span>
        </div>
      </div>
      <div class="qui-speed-group">
        <div class="qui-speed-pill">
          <svg class="qui-icon-ul" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke-width="2.5" stroke-linecap="round">
            <polyline points="22 7 13.5 15.5 8.5 10.5 2 17"/><polyline points="16 7 22 7 22 13"/>
          </svg>
          <span class="qui-speed-label">UL</span>
          <span class="qui-speed-value up qi-ul-speed">—</span>
        </div>
      </div>
    </div>
    <div class="qui-bar-sep"></div>
    <div class="qui-section">
      <div class="qui-meta-pill">
        <svg class="qui-icon-free" width="11" height="11" viewBox="0 0 16 16">
          <path d="M12 2.5a.5.5 0 1 1-1 0 .5.5 0 0 1 1 0m0 11a.5.5 0 1 1-1 0 .5.5 0 0 1 1 0m-7.5.5a.5.5 0 1 0 0-1 .5.5 0 0 0 0 1M5 2.5a.5.5 0 1 1-1 0 .5.5 0 0 1 1 0M8 8a1 1 0 1 0 0-2 1 1 0 0 0 0 2"/>
          <path d="M12 7a4 4 0 0 1-3.937 4c-.537.813-1.02 1.515-1.181 1.677a1.102 1.102 0 0 1-1.56-1.559c.1-.098.396-.314.795-.588A4 4 0 0 1 8 3a4 4 0 0 1 4 4m-1 0a3 3 0 1 0-3.891 2.865c.667-.44 1.396-.91 1.955-1.268.224-.144.483.115.34.34l-.62.96A3 3 0 0 0 11 7"/>
          <path d="M2 2a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2zm2-1a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V2a1 1 0 0 0-1-1z"/>
        </svg>
        <span class="qui-meta-label">Free</span>
        <span class="qui-meta-value qi-free-space">—</span>
      </div>
      <div class="qui-meta-pill">
        <svg class="qui-icon-ratio qi-ratio-icon" width="11" height="11" viewBox="0 0 16 16">
          <path d="M11.534 7h3.932a.25.25 0 0 1 .192.41l-1.966 2.36a.25.25 0 0 1-.384 0l-1.966-2.36a.25.25 0 0 1 .192-.41m-11 2h3.932a.25.25 0 0 0 .192-.41L2.692 6.23a.25.25 0 0 0-.384 0L.342 8.59A.25.25 0 0 0 .534 9"/>
          <path fill-rule="evenodd" d="M8 3c-1.552 0-2.94.707-3.857 1.818a.5.5 0 1 1-.771-.636A6.002 6.002 0 0 1 13.917 7H12.9A5 5 0 0 0 8 3M3.1 9a5.002 5.002 0 0 0 8.757 2.182.5.5 0 1 1 .771.636A6.002 6.002 0 0 1 2.083 9z"/>
        </svg>
        <span class="qui-meta-label">Ratio</span>
        <span class="qui-meta-value qi-global-ratio">—</span>
      </div>
      <div class="qui-meta-pill">
        <svg class="qui-icon-size" width="11" height="11" viewBox="0 0 16 16">
          <path d="M4.318 2.687C5.234 2.271 6.536 2 8 2s2.766.27 3.682.687C12.644 3.125 13 3.627 13 4c0 .374-.356.875-1.318 1.313C10.766 5.729 9.464 6 8 6s-2.766-.27-3.682-.687C3.356 4.875 3 4.373 3 4c0-.374.356-.875 1.318-1.313M13 5.698V7c0 .374-.356.875-1.318 1.313C10.766 8.729 9.464 9 8 9s-2.766-.27-3.682-.687C3.356 7.875 3 7.373 3 7V5.698c.271.202.58.378.904.525C4.978 6.711 6.427 7 8 7s3.022-.289 4.096-.777A5 5 0 0 0 13 5.698M14 4c0-1.007-.875-1.755-1.904-2.223C11.022 1.289 9.573 1 8 1s-3.022.289-4.096.777C2.875 2.245 2 2.993 2 4v9c0 1.007.875 1.755 1.904 2.223C4.978 15.71 6.427 16 8 16s3.022-.289 4.096-.777C13.125 14.755 14 14.007 14 13zm-1 4.698V10c0 .374-.356.875-1.318 1.313C10.766 11.729 9.464 12 8 12s-2.766-.27-3.682-.687C3.356 10.875 3 10.373 3 10V8.698c.271.202.58.378.904.525C4.978 9.71 6.427 10 8 10s3.022-.289 4.096-.777A5 5 0 0 0 13 8.698m0 3V13c0 .374-.356.875-1.318 1.313C10.766 14.729 9.464 15 8 15s-2.766-.27-3.682-.687C3.356 13.875 3 13.373 3 13v-1.302c.271.202.58.378.904.525C4.978 12.71 6.427 13 8 13s3.022-.289 4.096-.777c.324-.147.633-.323.904-.525"/>
        </svg>
        <span class="qui-meta-label">Size</span>
        <span class="qui-meta-value qi-total-size qi-total-size-val">—</span>
      </div>
    </div>
    <div class="qui-bar-sep"></div>
    <div class="qui-section">
      <div class="qui-count-pill"><span class="qui-count-label" style="color:var(--text2)">Total</span><span class="qui-count-value qi-total-torrents" style="color:var(--text2)">—</span></div>
      <div class="qui-count-pill"><span class="qui-count-label" style="color:var(--green)">Seeding</span><span class="qui-count-value qi-seeding" style="color:var(--green)">—</span></div>
      <div class="qui-count-pill"><span class="qui-count-label" style="color:var(--purple)">Downloading</span><span class="qui-count-value qi-downloading" style="color:var(--purple)">—</span></div>
      <div class="qui-count-pill"><span class="qui-count-label" style="color:var(--blue)">Checking</span><span class="qui-count-value qi-checking" style="color:var(--blue)">—</span></div>
      <div class="qui-count-pill"><span class="qui-count-label" style="color:var(--amber)">Paused</span><span class="qui-count-value qi-paused" style="color:var(--amber)">—</span></div>
      <div class="qui-count-pill"><span class="qui-count-label" style="color:var(--red)">Error</span><span class="qui-count-value qi-error" style="color:var(--red)">—</span></div>
      <div class="qui-count-pill qi-unregistered-pill"><span class="qui-count-label" style="color:var(--red)">Unregistered</span><span class="qui-count-value qi-unregistered" style="color:var(--red)">—</span></div>
    </div>
  </div>`;
}

/** Render one bar per enabled instance into both view containers */
export function renderQuiBars(settings: AppSettings, meta: QUIInstanceMeta[]): void {
  // Hide everything if user has turned off QUI bars
  const visible = settings.qui_bars_visible !== false; // default true
  for (const cid of ['qui-bars-grid', 'qui-bars-table']) {
    const el = document.getElementById(cid);
    if (!el) continue;
    if (!visible) { el.innerHTML = ''; el.style.display = 'none'; continue; }
    el.style.display = '';
    const enabled = settings.qui_enabled_instances ?? [];
    el.innerHTML = enabled.length === 0
      ? `<div class="qui-bar" style="margin-bottom:16px"><span style="font-size:12px;color:var(--text3);font-style:italic">QUI not configured — open ⚙ Settings to add instances</span></div>`
      : enabled.map(id => {
          const m = meta.find(x => x.id === id);
          return buildQuiBarHTML(id, m?.name ?? `Instance ${id}`);
        }).join('');
  }
}

/** Push live data into a single instance bar element */
export function updateQuiBar(bar: Element, data: Record<string, unknown>): void {
  const set = (cls: string, val: unknown) => {
    const el = bar.querySelector(cls);
    if (el) el.textContent = String(val ?? '—');
  };
  const setStyle = (cls: string, prop: string, val: string) => {
    const el = bar.querySelector(cls) as HTMLElement | null;
    if (el) el.style.setProperty(prop, val);
  };

  if (!data || data['error']) {
    const dot = bar.querySelector('.qi-dot');
    if (dot) dot.className = 'sdot red';
    set('.qi-conn-label', data?.['error'] === 'connection_error' ? 'Not reachable' : (data?.['error'] ?? '—'));
    ['.qi-dl-speed', '.qi-ul-speed', '.qi-free-space', '.qi-global-ratio',
     '.qi-seeding', '.qi-downloading', '.qi-checking', '.qi-paused', '.qi-error',
     '.qi-unregistered', '.qi-total-torrents', '.qi-total-size'].forEach(c => set(c, '—'));
    setStyle('.qi-global-ratio', 'color', 'var(--text3)');
    setStyle('.qi-ratio-icon', 'fill', 'var(--text3)');
    return;
  }

  const connected = String(data['connection_status'] ?? '').toLowerCase().includes('connect');
  const dot = bar.querySelector('.qi-dot');
  if (dot) dot.className = `sdot ${connected ? 'green pulse' : 'amber'}`;
  set('.qi-conn-label', data['connection_status'] ?? '—');
  set('.qi-dl-speed', fmtSpeed(Number(data['dl_info_speed']) || 0));
  set('.qi-ul-speed', fmtSpeed(Number(data['up_info_speed']) || 0));
  set('.qi-free-space', fmtBytes(Number(data['free_space_on_disk']) || 0));

  // Ratio — same colour scheme as tracker Ratio / Real Ratio columns
  const gr = parseFloat(String(data['global_ratio'] ?? 'NaN'));
  set('.qi-global-ratio', isNaN(gr) ? '—' : gr.toFixed(2));
  const rCol = isNaN(gr) ? 'text3' : ratioColor(gr);
  setStyle('.qi-global-ratio', 'color', `var(--${rCol})`);
  setStyle('.qi-ratio-icon', 'fill', `var(--${rCol})`);

  set('.qi-seeding',        data['seeding'] ?? '—');
  set('.qi-downloading',    data['downloading'] ?? '—');
  set('.qi-checking',       data['checking'] ?? 0);
  set('.qi-paused',         data['paused'] ?? '—');
  set('.qi-error',          data['errors'] ?? 0);
  // Unregistered — absent on qui versions that predate the counter; hide the
  // pill rather than show a fake 0.
  const unregPill = bar.querySelector('.qi-unregistered-pill') as HTMLElement | null;
  if (unregPill) unregPill.style.display = data['unregistered'] == null ? 'none' : '';
  set('.qi-unregistered',   data['unregistered'] ?? '—');
  set('.qi-total-torrents', data['total_torrents'] ?? '—');
  set('.qi-total-size',     fmtBytes(Number(data['total_size']) || 0));
}

/** Fetch and update stats for all enabled instances */
export async function refreshQuiStats(settings: AppSettings): Promise<void> {
  const enabled = settings.qui_enabled_instances ?? [];
  if (!enabled.length) return;

  await Promise.all(enabled.map(async instId => {
    const { ok, data } = await api.fetchQUIStats(instId);
    document.querySelectorAll(`.qui-inst-bar[data-inst-id="${instId}"]`)
      .forEach(bar => updateQuiBar(bar, ok ? data as Record<string, unknown> : { error: (data as {error?:string})?.error ?? 'failed' }));
  }));
}

/**
 * Render the QUI instance checklist in the settings page.
 * `override` passes the url/key currently TYPED in the form (unsaved) so the
 * "Reload instances" button can test credentials before Save. A key equal to
 * the mask sentinel makes the backend use the stored key.
 * Returns ok/error so the caller can toast a clean failure message.
 */
export async function renderQUIInstanceChecklist(
  settings: AppSettings,
  meta: QUIInstanceMeta[],
  override?: { url?: string; key?: string },
): Promise<{ ok: boolean; error?: string }> {
  void meta;
  const listEl = document.getElementById('s-qui-instance-list');
  if (!listEl) return { ok: false, error: 'no list element' };

  listEl.innerHTML = '<span style="font-size:12px;color:var(--text3);font-style:italic">Loading…</span>';
  const { ok, data } = await api.fetchQUIInstances(override?.url, override?.key);

  if (!ok || !Array.isArray(data)) {
    const err = (data as unknown as { error?: string })?.error ?? 'connection_error';
    listEl.innerHTML = '<span style="font-size:12px;color:var(--red)">Could not reach QUI — check URL and API key</span>';
    return { ok: false, error: err };
  }

  const enabled = settings.qui_enabled_instances ?? [];
  if (!data.length) {
    listEl.innerHTML = '<span style="font-size:12px;color:var(--text3)">No instances found at this URL</span>';
    return { ok: true };
  }

  listEl.innerHTML = data.map(inst => {
    const checked = enabled.includes(inst.id) ? 'checked' : '';
    const dot = inst.connected
      ? '<span class="sdot green" style="width:6px;height:6px;flex-shrink:0"></span>'
      : '<span class="sdot red" style="width:6px;height:6px;flex-shrink:0"></span>';
    return `<label class="qui-inst-row">
      <input type="checkbox" class="qui-inst-toggle" value="${inst.id}" ${checked} onchange="onQuiInstanceToggle()">
      ${dot}
      <span class="qui-inst-label">${esc(inst.name || 'Instance ' + inst.id)}</span>
      <span class="qui-inst-host">${esc(inst.host || 'id:' + inst.id)}</span>
    </label>`;
  }).join('');
  return { ok: true };
}
