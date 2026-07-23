// components/trackersTab.ts — Settings → Trackers tab
// Configured-trackers table (enabled toggle / edit / inline-confirm delete)
// plus the collapsible "Import from Prowlarr" / "Import from Jackett" sections.
import type { OptOutEntry, ProwlarrIndexer, TestStatusMap, Tracker } from '../types';
import { MASKED_KEY } from '../types';
import * as api from '../api';
import { appSettings } from '../state';
import { approvalIcon, approvalTitle, approvalWarns } from '../utils/approval';
import { esc } from '../utils/format';
import { findOptOut } from '../utils/optout';
import { renderTestPills } from './trackerTest';
import type { ToastType } from './toast';

interface TabDeps {
  loadTrackers: () => Promise<void>;
  refreshSingle: (id: string) => Promise<void>;
  loadScrapeStatus: () => Promise<void>;
  toast: (msg: string, type?: ToastType) => void;
}

let _deps: TabDeps | null = null;
let _trackers: Tracker[] = [];
let _pendingRowDelete: string | null = null;
let _testStatus: TestStatusMap = {};
let _testing = new Set<string>(); // tracker ids with a test in flight

// ─────────────────────────────────────────────────────────────────────────────
// Configured trackers table
// ─────────────────────────────────────────────────────────────────────────────

/** Tooltip for the opt-out badge: explains Yata has stopped contacting it. */
function optedOutTitle(t: Tracker): string {
  const note = t.opted_out_note ? ` ${t.opted_out_note}` : '';
  return `${t.name}'s operator has asked not to be supported by Yata — `
    + `all API and scrape traffic to it has stopped. Remove it to clear this.${note}`;
}

export function renderTrackersTable(trackers: Tracker[], deps: TabDeps): void {
  _deps = deps;
  _trackers = trackers;
  const tbody = document.getElementById('trk-tbody');
  if (!tbody) return;

  if (!trackers.length) {
    tbody.innerHTML = `<tr><td colspan="7" class="trk-empty">No trackers configured yet — click “Add Tracker” above or import from Prowlarr below.</td></tr>`;
    return;
  }

  tbody.innerHTML = trackers.map(t => {
    const abbr = t.abbr ? `<span class="trk-abbr-badge">${esc(t.abbr)}</span>` : '';
    const optOutBadge = t.opted_out
      ? `<span class="trk-optout-badge" title="${esc(optedOutTitle(t))}">⛔ opted out</span>`
      : '';
    const defBadge = (t.def_key
      ? `<span class="trk-def-badge">def: ${esc(t.def_key)}</span>`
      : `<span class="trk-def-badge manual">manual</span>`)
      + approvalIcon(t.def_approval, t.def_approval_note)
      + optOutBadge;
    const testCell = _testing.has(t.id)
      ? `<span class="trk-test-untested"><i class="fas fa-spinner fa-spin"></i> Testing…</span>`
      : renderTestPills(_testStatus[t.id]);
    const confirming = _pendingRowDelete === t.id;
    const actions = confirming
      ? `<span class="trk-del-confirm">Delete <strong>${esc(t.name)}</strong>?</span>
         <button class="btn btn-danger btn-sm" onclick="trkConfirmDelete('${esc(t.id)}')">Delete</button>
         <button class="btn btn-ghost btn-sm" onclick="trkCancelDelete()">Cancel</button>`
      : `<button class="btn btn-ghost btn-sm" onclick="trkTest('${esc(t.id)}')" ${_testing.has(t.id) ? 'disabled' : ''} title="Test API & scrape connectivity">
           <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/></svg>
           Test
         </button>
         <button class="btn btn-ghost btn-sm" onclick="openEditModal('${esc(t.id)}')">
           <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
           Edit
         </button>
         <button class="btn btn-ghost btn-sm trk-del-btn" onclick="trkAskDelete('${esc(t.id)}')">
           <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>
           Delete
         </button>`;
    return `<tr class="trk-row${t.enabled === false ? ' trk-row-disabled' : ''}" id="trk-row-${esc(t.id)}">
      <td class="trk-td-toggle">
        <div class="toggle-track trk-toggle ${t.enabled !== false ? 'on' : ''}" role="switch" aria-checked="${t.enabled !== false}"
          title="${t.enabled !== false ? 'Enabled — click to disable' : 'Disabled — click to enable'}"
          onclick="trkToggleEnabled('${esc(t.id)}')"><div class="toggle-thumb"></div></div>
      </td>
      <td class="trk-td-name"><span class="trk-name">${esc(t.name)}</span>${abbr}</td>
      <td class="trk-td-url"><a href="${esc(t.url)}" target="_blank" rel="noopener noreferrer">${esc(t.url)}</a></td>
      <td class="trk-td-type">${esc(t.type)}</td>
      <td class="trk-td-def">${defBadge}</td>
      <td class="trk-td-test">${testCell}</td>
      <td class="trk-td-actions">${actions}</td>
    </tr>`;
  }).join('');
}

/** Load cached test results from the backend, then re-render the table. */
export async function loadTestStatus(): Promise<void> {
  const { ok, data } = await api.fetchTestStatus();
  if (ok && data) {
    _testStatus = data;
    if (_deps) renderTrackersTable(_trackers, _deps);
  }
}

/** Run a live API + scrape test for one tracker (table "Test" button). */
export async function trkTest(id: string): Promise<void> {
  if (!_deps || _testing.has(id)) return;
  const t = _trackers.find(x => x.id === id);
  _testing.add(id);
  renderTrackersTable(_trackers, _deps);
  const { ok, data } = await api.testTracker(id);
  _testing.delete(id);
  if (ok && data) {
    _testStatus[id] = data;
    const failed = data.api.status === 'fail' || data.scrape.status === 'fail';
    _deps.toast(`${t?.name ?? 'Tracker'}: API ${data.api.status}, scrape ${data.scrape.status}`, failed ? 'error' : 'success');
  } else {
    _deps.toast(`Could not test ${t?.name ?? 'tracker'}`, 'error');
  }
  renderTrackersTable(_trackers, _deps);
}

/** Test every configured tracker sequentially ("Test All"). Sequential on
 *  purpose: each test shares the per-tracker locks and the full rate-limit
 *  cascade, so this can never burst-hit the trackers. */
let _testingAll = false;
export async function testAllTrackers(): Promise<void> {
  if (!_deps || _testingAll || !_trackers.length) return;
  const btn = document.getElementById('trk-test-all-btn') as HTMLButtonElement | null;
  _testingAll = true;
  if (btn) btn.disabled = true;
  let i = 0;
  for (const t of [..._trackers]) {
    i++;
    if (btn) btn.textContent = `Testing ${i}/${_trackers.length}…`;
    await trkTest(t.id);
  }
  _testingAll = false;
  if (btn) { btn.disabled = false; btn.textContent = 'Test All'; }
  _deps.toast('All trackers tested', 'success');
}

/** Enabled toggle — immediate PUT {enabled}. */
export async function trkToggleEnabled(id: string): Promise<void> {
  if (!_deps) return;
  const t = _trackers.find(x => x.id === id);
  if (!t) return;
  const newEnabled = t.enabled === false; // toggling
  const { ok } = await api.updateTracker(id, { enabled: newEnabled });
  if (ok) {
    _deps.toast(`${t.name} ${newEnabled ? 'enabled' : 'disabled'}`, 'success');
    await _deps.loadTrackers();
    await _deps.loadScrapeStatus(); // disabling stops scraping — badges must reflect that now
    void import('../views/detail').then(m => m.redrawDetail()); // no-op unless this tracker's detail page is open
  } else {
    _deps.toast(`Failed to ${newEnabled ? 'enable' : 'disable'} ${t.name}`, 'error');
  }
}

export function trkAskDelete(id: string): void {
  _pendingRowDelete = id;
  if (_deps) renderTrackersTable(_trackers, _deps);
}

export function trkCancelDelete(): void {
  _pendingRowDelete = null;
  if (_deps) renderTrackersTable(_trackers, _deps);
}

export async function trkConfirmDelete(id: string): Promise<void> {
  if (!_deps) return;
  const t = _trackers.find(x => x.id === id);
  _pendingRowDelete = null;
  const { ok } = await api.deleteTracker(id);
  if (ok) {
    _deps.toast(`${t?.name ?? 'Tracker'} removed`, 'success');
    await _deps.loadTrackers();
    await _deps.loadScrapeStatus();
    // Redraw an open detail page for this tracker — closing it rather than
    // rendering a "not found" ghost, since it no longer exists in state.
    void import('../views/detail').then(m => m.detailTrackerDeleted(id));
  } else {
    _deps.toast('Failed to remove tracker', 'error');
    renderTrackersTable(_trackers, _deps);
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Indexer-manager imports (Prowlarr / Jackett)
// One generic implementation; each source has an HTML section whose element
// ids are prefixed with the source key ("prowlarr-url", "jackett-url", …).
// The backend saves the connection (URL + secret) on a successful fetch, so
// prefillImportCreds() can restore the inputs from settings next session.
// ─────────────────────────────────────────────────────────────────────────────

type ImportKey = 'prowlarr' | 'jackett';

interface ImportSource {
  label: string;
  fetch: (url: string, secret: string) => Promise<{ ok: boolean; data: ProwlarrIndexer[] }>;
  /** Prowlarr needs the API key up front; Jackett's admin password is optional. */
  secretRequired: boolean;
  saved: () => { url: string; secret: string };
  save: (url: string) => void; // persist locally (server already saved)
}

const IMPORT_SOURCES: Record<ImportKey, ImportSource> = {
  prowlarr: {
    label: 'Prowlarr',
    fetch: (u, s) => api.fetchProwlarrIndexers(u, s),
    secretRequired: true,
    saved: () => ({ url: appSettings.prowlarr_url ?? '', secret: appSettings.prowlarr_api_key ?? '' }),
    save: url => { if (url) appSettings.prowlarr_url = url; appSettings.prowlarr_api_key = MASKED_KEY; },
  },
  jackett: {
    label: 'Jackett',
    fetch: (u, s) => api.fetchJackettIndexers(u, s),
    secretRequired: false,
    saved: () => ({ url: appSettings.jackett_url ?? '', secret: appSettings.jackett_admin_password ?? '' }),
    save: url => { if (url) appSettings.jackett_url = url; },
  },
};

const _importLists: Record<ImportKey, ProwlarrIndexer[]> = { prowlarr: [], jackett: [] };
let _optOuts: OptOutEntry[] = [];
let _optOutsLoaded = false;

/** Opt-out list from /api/defs — loaded once, used to disable import rows. */
async function ensureOptOutsLoaded(): Promise<void> {
  if (_optOutsLoaded) return;
  const { ok, data } = await api.fetchDefs();
  if (ok) { _optOuts = data.opt_outs ?? []; _optOutsLoaded = true; }
}

/** Invalidate the cached opt-out list — the defs it's derived from may have
 *  just changed (Reload Definitions), so the next import fetch must re-fetch
 *  it instead of using the stale cache. */
export function resetOptOutsLoaded(): void {
  _optOutsLoaded = false;
}

/** Prefill the URL/secret inputs from saved settings — only when the user
 *  hasn't typed anything (never clobber in-progress input). The secret shows
 *  the mask sentinel; the backend resolves it to the stored value on fetch. */
export function prefillImportCreds(): void {
  for (const key of Object.keys(IMPORT_SOURCES) as ImportKey[]) {
    const saved = IMPORT_SOURCES[key].saved();
    const urlIn = document.getElementById(`${key}-url`) as HTMLInputElement | null;
    const secIn = document.getElementById(`${key}-key`) as HTMLInputElement | null;
    if (urlIn && !urlIn.value && saved.url) urlIn.value = saved.url;
    if (secIn && !secIn.value && saved.secret) secIn.value = saved.secret;
  }
}

export function toggleImportSection(key: ImportKey): void {
  const body = document.getElementById(`${key}-body`);
  const chev = document.getElementById(`${key}-chev`);
  if (!body) return;
  const open = body.style.display === 'none';
  body.style.display = open ? '' : 'none';
  if (chev) chev.classList.toggle('open', open);
  try { localStorage.setItem(`u3d-import-open-${key}`, open ? '1' : '0'); } catch { /* private mode */ }
}

/** Restore each import section's remembered open/closed state (idempotent —
 *  called on every trackers-tab render alongside prefillImportCreds). */
export function restoreImportSections(): void {
  for (const key of Object.keys(IMPORT_SOURCES) as ImportKey[]) {
    if (localStorage.getItem(`u3d-import-open-${key}`) !== '1') continue;
    const body = document.getElementById(`${key}-body`);
    const chev = document.getElementById(`${key}-chev`);
    if (body) body.style.display = '';
    if (chev) chev.classList.add('open');
  }
}

export async function fetchImportIndexers(key: ImportKey): Promise<void> {
  if (!_deps) return;
  const src = IMPORT_SOURCES[key];
  const url = (document.getElementById(`${key}-url`) as HTMLInputElement)?.value.trim() ?? '';
  const secret = (document.getElementById(`${key}-key`) as HTMLInputElement)?.value.trim() ?? '';
  const results = document.getElementById(`${key}-results`);
  const btn = document.getElementById(`${key}-fetch-btn`) as HTMLButtonElement | null;
  if (!results) return;

  // Blank fields fall back to the saved connection server-side.
  const saved = src.saved();
  if (!url && !saved.url) {
    _deps.toast(`${src.label} URL is required`, 'error');
    return;
  }
  if (src.secretRequired && !secret && !saved.secret) {
    _deps.toast(`${src.label} URL and API key are required`, 'error');
    return;
  }

  if (btn) { btn.disabled = true; btn.textContent = 'Fetching…'; }
  results.innerHTML = `<span style="font-size:12px;color:var(--text3);font-style:italic">Contacting ${src.label}…</span>`;
  const [{ ok, data }] = await Promise.all([src.fetch(url, secret), ensureOptOutsLoaded()]);
  if (btn) { btn.disabled = false; btn.textContent = 'Fetch indexers'; }

  if (!ok || !Array.isArray(data)) {
    const err = (data as unknown as { error?: string })?.error ?? 'connection_error';
    results.innerHTML = `<span style="font-size:12px;color:var(--red)">Could not fetch indexers — ${esc(err)}</span>`;
    _deps.toast(`${src.label}: ${err}`, 'error');
    return;
  }

  // The backend saved the connection — mirror it locally so a later full
  // settings save round-trips instead of clobbering, and re-mask the secret.
  src.save(url);
  const secIn = document.getElementById(`${key}-key`) as HTMLInputElement | null;
  if (secIn && secIn.value && secIn.value !== MASKED_KEY) secIn.value = MASKED_KEY;

  _importLists[key] = data;
  if (!data.length) {
    results.innerHTML = `<span style="font-size:12px;color:var(--text3)">${src.label} returned no indexers</span>`;
    return;
  }
  renderImportResults(key, results);
  updateImportBtn(key);
}

function renderImportResults(key: ImportKey, results: HTMLElement): void {
  results.innerHTML = _importLists[key].map((ix, i) => {
    const optedOut = !!findOptOut(_optOuts, ix.base_url);
    const disabled = ix.already_added || optedOut;
    const checked = !disabled && ix.privacy === 'private' && (ix.has_api_key || !!ix.session_cookie);
    const badges: string[] = [];
    if (ix.def_key)        badges.push(`<span class="prowlarr-badge def">✓ ${esc(ix.def_key)} def</span>`);
    if (ix.def_key && approvalWarns(ix.def_approval))
      badges.push(`<span class="prowlarr-badge approval" title="${esc(approvalTitle(ix.def_approval))}">⚠ not approved</span>`);
    if (ix.has_api_key)    badges.push(`<span class="prowlarr-badge key">has API key</span>`);
    if (ix.session_cookie) badges.push(`<span class="prowlarr-badge key">has cookie</span>`);
    if (ix.privacy)        badges.push(`<span class="prowlarr-badge privacy">${esc(ix.privacy)}</span>`);
    if (ix.already_added)  badges.push(`<span class="prowlarr-badge added">already added</span>`);
    if (optedOut)          badges.push(`<span class="prowlarr-badge optout">opted out</span>`);
    return `<label class="prowlarr-row${disabled ? ' disabled' : ''}">
      <input type="checkbox" class="prowlarr-check" value="${i}" ${checked ? 'checked' : ''} ${disabled ? 'disabled' : ''}
        onchange="updateImportBtn('${key}')">
      <span class="prowlarr-name">${esc(ix.name)}</span>
      <span class="prowlarr-url">${esc(ix.base_url)}</span>
      <span class="prowlarr-badges">${badges.join('')}</span>
    </label>`;
  }).join('');
}

export function updateImportBtn(key: ImportKey): void {
  const btn = document.getElementById(`${key}-import-btn`) as HTMLButtonElement | null;
  const results = document.getElementById(`${key}-results`);
  if (!btn || !results) return;
  const n = results.querySelectorAll<HTMLInputElement>('.prowlarr-check:checked').length;
  btn.textContent = `Import ${n} selected`;
  btn.disabled = n === 0;
}

export async function importSelected(key: ImportKey): Promise<void> {
  if (!_deps) return;
  const results = document.getElementById(`${key}-results`);
  if (!results) return;
  const checks = [...results.querySelectorAll<HTMLInputElement>('.prowlarr-check:checked')];
  const selected = checks
    .map(cb => _importLists[key][parseInt(cb.value, 10)])
    .filter((ix): ix is ProwlarrIndexer => !!ix);
  if (!selected.length) return;

  const btn = document.getElementById(`${key}-import-btn`) as HTMLButtonElement | null;
  if (btn) { btn.disabled = true; btn.textContent = 'Importing…'; }

  let imported = 0, failed = 0;
  for (const ix of selected) {
    // Type comes from the def match server-side; manual ones default unit3d.
    // Jackett entries can carry the stored session cookie → scraping works
    // out of the box (cookies expire — user refreshes via Edit when needed).
    const { ok } = await api.addTracker({
      name: ix.name,
      url: ix.base_url,
      api_key: ix.api_key ?? '',
      ...(ix.session_cookie ? { session_cookie: ix.session_cookie } : {}),
    });
    if (ok) { imported++; ix.already_added = true; }
    else failed++;
  }

  _deps.toast(
    failed ? `${imported} imported, ${failed} failed` : `${imported} imported`,
    failed ? 'error' : 'success',
  );
  await _deps.loadTrackers(); // refreshes trackers table + dashboard state
  await _deps.loadScrapeStatus(); // newly-imported trackers' scrape badges start correct, not stale
  renderImportResults(key, results); // re-render with "already added" rows disabled
  updateImportBtn(key);
}
