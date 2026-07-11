// main.ts — application bootstrap and refresh orchestration (v2 API)
//
// Boot flow: loadSettings → loadTrackers → bulk stats → scrape-status →
// render. A 5-minute timer refreshes stats/history; profile scrapes fire via
// /api/scrape/{id} from the per-tracker button or auto-sync (when allowed).
//
// STALE DATA RULE: statsCache entries are only ever *merged*, never blanked.
// When a refresh/scrape fails (error, 429, network down) the previously
// stored fields stay in the cache and keep rendering.
import * as api from './api';
import * as state from './state';
import { startIconFallback } from './utils/icons';
import { renderGrid, renderCard } from './views/grid';
import { renderAggCards } from './views/aggCards';
import { renderQuiBars, refreshQuiStats, renderQUIInstanceChecklist } from './components/qui';
import { toast } from './components/toast';
import { openColCustomizer, toggleColVisible } from './components/cols';
import { initTargetsPopover, openTargetsPopover, closeTargetsPopover } from './components/targetsPopover';
import { loadColPrefs, resetColPrefs, setScrapeStatus, scrapeStatus } from './state';
import { errLabel } from './utils/format';
import * as trackersTab from './components/trackersTab';
import * as logsTab from './components/logs';
import * as alertsTab from './components/alertsTab';
import { initPathways } from './views/pathways';
import type { ColPref, ScrapeBlocked, TrackerStatsResponse, ViewMode } from './types';

// modals.ts is loaded dynamically (it's large); store the promise so route
// handlers can await the module.
const modalsReady = import('./components/modals');

// ── Column prefs (persisted) ──────────────────────────────────────────────
let colPrefs: ColPref[] = loadColPrefs();

// ── Timers ────────────────────────────────────────────────────────────────
// Cadences are user-settable; the guards mirror the backend floors. The manual
// refresh button and Tracker Test bypass these entirely.
const refreshMs = () =>
  Math.max(15, state.appSettings.refresh_interval_minutes || 30) * 60 * 1000;
const quiRefreshMs = () =>
  Math.max(1, state.appSettings.qui_refresh_seconds || 10) * 1000;
let refreshTimer: ReturnType<typeof setInterval> | null = null;
let quiTimer:     ReturnType<typeof setInterval> | null = null;

// ── Boot ──────────────────────────────────────────────────────────────────
window.addEventListener('DOMContentLoaded', async () => {
  applyView(state.currentView, false);
  applyRoute();
  await boot();
});

async function boot() {
  void loadSelfHostedIcons(); // swap to a self-hosted Font Awesome (e.g. Pro) if present
  // Auth gate: when an account is configured but this session isn't logged in,
  // show the login overlay and stop — boot resumes after a successful login.
  if (!(await ensureAuthenticated())) return;
  await loadSettings();
  await loadTrackers();
  void initPathwaysFeature(); // independent of stats — don't block the refresh
  await refreshAllStats();
  await loadScrapeStatus();
  await Promise.all([loadHistory(), loadTrackerGroups()]);
  await loadQUIInstances();
  renderQuiBars(state.appSettings, state.quiInstancesMeta);
  await refreshQuiStats(state.appSettings);
  scheduleRefresh();
  scheduleQuiRefresh();
  autoSyncScrapes();
}

// ── Auth gate (single-user basic auth) ─────────────────────────────────────
/** Returns true if the app may load; false when the login gate was shown. */
async function ensureAuthenticated(): Promise<boolean> {
  const { ok, data } = await api.fetchAuthStatus();
  if (ok && data.configured && !data.authenticated) {
    showLogin();
    return false;
  }
  // No account configured → show the first-run security nudge (once per session).
  if (ok && !data.configured) showAuthNudge();
  return true;
}

function showAuthNudge() {
  if (sessionStorage.getItem('yata-nudge-dismissed') === '1') return;
  const el = document.getElementById('auth-nudge');
  if (el) el.style.display = 'flex';
}
function dismissAuthNudge() {
  sessionStorage.setItem('yata-nudge-dismissed', '1');
  const el = document.getElementById('auth-nudge');
  if (el) el.style.display = 'none';
}
(window as any).dismissAuthNudge = dismissAuthNudge;

function showLogin() {
  const ov = document.getElementById('login-overlay');
  if (ov) ov.style.display = 'flex';
  (document.getElementById('login-username') as HTMLInputElement | null)?.focus();
}

function hideLogin() {
  const ov = document.getElementById('login-overlay');
  if (ov) ov.style.display = 'none';
}

async function submitLogin(e: Event) {
  e.preventDefault();
  const u = (document.getElementById('login-username') as HTMLInputElement).value.trim();
  const p = (document.getElementById('login-password') as HTMLInputElement).value;
  const errEl = document.getElementById('login-error');
  const btn = document.getElementById('login-submit') as HTMLButtonElement;
  if (errEl) errEl.style.display = 'none';
  btn.disabled = true; btn.textContent = 'Signing in…';
  const { ok, data, status } = await api.authLogin(u, p);
  btn.disabled = false; btn.textContent = 'Sign in';
  if (ok && data.ok) {
    (document.getElementById('login-password') as HTMLInputElement).value = '';
    hideLogin();
    await boot();
    return;
  }
  // Locked out after too many attempts → show the wait + recovery option.
  if (status === 429 && data.error === 'locked') {
    const mins = Math.max(1, Math.ceil((data.retry_after ?? 0) / 60));
    const locked = document.getElementById('login-locked');
    const msg = document.getElementById('login-locked-msg');
    if (msg) msg.textContent = `Too many failed attempts — temporarily locked out. Try again in about ${mins} minute${mins === 1 ? '' : 's'}, or reset your login now.`;
    if (locked) locked.style.display = 'block';
    return;
  }
  if (errEl) {
    errEl.textContent = status === 401 ? 'Invalid username or password.' : 'Sign in failed — please try again.';
    errEl.style.display = 'block';
  }
}
(window as any).submitLogin = submitLogin;

/** Locked-out / forgotten-password recovery — wipes the account + all data. */
async function resetLogin() {
  if (!confirm('Reset login and ERASE ALL DATA?\n\nThis deletes your account, trackers, stats, settings and alerts so you can get back in. Your tracker accounts themselves are NOT affected. This cannot be undone.')) return;
  const { ok } = await api.authReset();
  if (ok) location.reload();
}
(window as any).resetLogin = resetLogin;

/** Log out and re-show the gate (used by Settings → Account). */
async function doLogout() {
  await api.authLogout();
  showLogin();
}
(window as any).doLogout = doLogout;

// A 401 from any protected endpoint (expired session) re-shows the gate.
api.setUnauthorizedHandler(showLogin);

// ── Settings ──────────────────────────────────────────────────────────────
async function loadSettings() {
  const { ok, data } = await api.fetchSettings();
  if (ok) {
    state.setAppSettings(data);
    applyPrivateMode(data.private_mode ?? false);
    api.applyTheme(data.theme ?? '');
    updateScrapeAlert();
  }
}

/** Auto-load a self-hosted Font Awesome (e.g. Pro) if the user has dropped the
 *  files at /static/fontawesome/css/all.min.css — no setting needed. It
 *  supersedes the bundled free CDN, so tracker Pro icons (lobster, crab, …)
 *  render. Absent → silently keeps the free set. */
async function loadSelfHostedIcons(): Promise<void> {
  if (document.getElementById('icon-selfhost')) return;
  const url = '/static/fontawesome/css/all.min.css';
  let selfHosted = false;
  try {
    // no-store: a cached 200 must not outlive a removed/renamed Pro set.
    const res = await fetch(url, { method: 'HEAD', cache: 'no-store' });
    selfHosted = res.ok;
  } catch { /* keep the free set */ }

  // Runs after the fallback engine's font probe: writes the icon-set status
  // line in Settings → Display, and — when even the SOLID face of a
  // self-hosted kit is dead (fundamentally broken install) — re-enables the
  // free CDN so the fallback stars themselves can render.
  const finish = (dead: { label: string; prefix: string }[]) => {
    renderIconSetStatus(selfHosted, dead);
    // Only when the DEFAULT solid style itself is dead is the kit broken
    // enough that even fallback stars can't render — rescue with the free CDN.
    if (selfHosted && dead.some(d => d.prefix === 'fas')) {
      const cdn = document.querySelector('link[href*="font-awesome"]') as HTMLLinkElement | null;
      if (cdn) cdn.disabled = false;
    }
  };

  if (!selfHosted) {
    // Free set only: Pro-only icon classes in tracker defs have no CSS rule
    // here — start the fallback engine once every stylesheet has loaded so
    // missing glyphs get swapped for a free icon instead of blank space.
    if (document.readyState === 'complete') void startIconFallback().then(finish);
    else window.addEventListener('load', () => void startIconFallback().then(finish), { once: true });
    return;
  }

  const link = document.createElement('link');
  link.id = 'icon-selfhost';
  link.rel = 'stylesheet';
  link.href = url;
  // Even a Pro set can miss icons (older kits) — run the fallback once the
  // self-hosted CSS has actually parsed, never before (a premature sweep
  // would "fix" Pro icons whose rules simply hadn't arrived yet).
  link.onload = () => void startIconFallback().then(finish);
  link.onerror = () => void startIconFallback().then(finish);
  document.head.appendChild(link);
  // Disable the free CDN — the self-hosted set is a superset, so loading both
  // is wasteful and could create font-family ambiguity.
  const cdn = document.querySelector('link[href*="font-awesome"]') as HTMLLinkElement | null;
  if (cdn) cdn.disabled = true;
}

/** Icon-set health line under the Font Awesome hint in Settings → Display. */
function renderIconSetStatus(selfHosted: boolean, dead: { label: string }[]) {
  const el = document.getElementById('fa-status');
  if (!el) return;
  el.style.display = '';
  if (!selfHosted) {
    el.innerHTML = `<i class="fas fa-circle-info" style="margin-right:5px"></i>Using the bundled free icon set — Pro-only tracker icons show a generic fallback.`;
    el.style.color = 'var(--text3)';
  } else if (!dead.length) {
    el.innerHTML = `<i class="fas fa-circle-check" style="margin-right:5px"></i>Self-hosted Font Awesome loaded — all icon styles available.`;
    el.style.color = 'var(--green)';
  } else {
    el.innerHTML = `<i class="fas fa-triangle-exclamation" style="margin-right:5px"></i>` +
      `Self-hosted Font Awesome found, but these styles failed to load: <strong>${
        dead.map(d => d.label).join(', ')}</strong>. ` +
      `Copy the missing <code>.woff2</code> files from your Font Awesome download into ` +
      `<code>static/fontawesome/webfonts/</code> (next to <code>css/</code>) — affected icons show a fallback star until then.`;
    el.style.color = 'var(--amber)';
  }
}

function applyPrivateMode(on: boolean) {
  document.body.classList.toggle('private-mode', on);
  // Topbar eye reflects the state: open eye = visible, slashed = blurred.
  const eye = document.getElementById('privacy-eye');
  const eyeOff = document.getElementById('privacy-eye-off');
  if (eye) eye.style.display = on ? 'none' : '';
  if (eyeOff) eyeOff.style.display = on ? '' : 'none';
  document.getElementById('privacy-btn')?.classList.toggle('active', on);
  // Keep the settings-page toggle in sync if it's rendered.
  const track = document.getElementById('s-private-track');
  if (track) track.className = `toggle-track ${on ? 'on' : ''}`;
}

/** Topbar eye button: toggle username blur without opening settings. */
async function togglePrivacyQuick() {
  const next = !(state.appSettings.private_mode ?? false);
  applyPrivateMode(next); // instant feedback
  state.appSettings.private_mode = next;
  // Persist. appSettings round-trips safely (QUI key is the mask sentinel).
  const { ok } = await api.saveSettings({ ...state.appSettings, private_mode: next });
  if (!ok) {
    // Revert on failure so the UI never lies about what's stored.
    state.appSettings.private_mode = !next;
    applyPrivateMode(!next);
  }
}

// ── Scrape status ──────────────────────────────────────────────────────────
async function loadScrapeStatus() {
  const { ok, data } = await api.fetchScrapeStatus();
  if (ok) { setScrapeStatus(data); updateScrapeAlert(); }
}

function updateScrapeAlert() {
  const bar = document.getElementById('scrape-alert-bar');
  if (!bar) return;
  const apiOnly = state.appSettings.api_only_mode ?? false;
  let limitedCount = 0;
  for (const entry of Object.values(scrapeStatus)) {
    if (!entry.allowed && entry.reason === 'daily_limit') limitedCount++;
  }
  const parts: string[] = [];
  if (apiOnly) parts.push('<span style="color:var(--text3);font-size:12px"><i class="fas fa-ban" style="margin-right:5px;opacity:.7"></i>API only mode — profile scraping is disabled</span>');
  if (limitedCount > 0) parts.push(`<span style="color:var(--red);font-size:12px;font-weight:500"><i class="fas fa-exclamation-triangle" style="margin-right:5px"></i>${limitedCount} tracker${limitedCount > 1 ? 's have' : ' has'} hit the daily maximum scrapes</span>`);
  if (parts.length > 0) { bar.innerHTML = parts.join('<span style="color:var(--border2);margin:0 8px">|</span>'); bar.style.display = 'flex'; }
  else { bar.style.display = 'none'; }
}

// ── Trackers ──────────────────────────────────────────────────────────────
async function loadTrackers() {
  const { ok, data } = await api.fetchTrackers();
  if (!ok) return;
  state.setTrackers(state.applySavedOrder(data));
  renderGridFull();
  renderTable();
  renderTrackersTab();
  updateSummary();
}

/** Render the Settings → Trackers tab table from current state. */
function renderTrackersTab() {
  trackersTab.renderTrackersTable(state.trackers, { loadTrackers, refreshSingle, toast });
  trackersTab.prefillImportCreds(); // saved Prowlarr/Jackett connections
  trackersTab.restoreImportSections(); // remembered open/closed state
  void trackersTab.loadTestStatus(); // fill in cached test-status pills
}

async function loadTrackerGroups() {
  const { ok, data } = await api.fetchTrackerGroups();
  if (ok && data) {
    state.setGroupDefs(data);
    renderGridFull();
    renderTable(); // re-render with group styling
  }
}

/** Drag-reorder — persisted locally (the v2 backend has no reorder endpoint). */
function handleReorder(srcId: string, dstId: string) {
  const fi = state.trackers.findIndex(t => t.id === srcId);
  const ti = state.trackers.findIndex(t => t.id === dstId);
  if (fi === -1 || ti === -1) return;
  const [moved] = state.trackers.splice(fi, 1);
  state.trackers.splice(ti, 0, moved);
  state.saveOrder(state.trackers.map(t => t.id));
  renderGridFull();
  renderTable();
}

/** Render grid with current full state (settings + groupDefs) */
function renderGridFull() {
  renderGrid(state.trackers, state.statsCache, handleReorder, state.appSettings, state.groupDefs);
}

// ── Stats refresh ─────────────────────────────────────────────────────────

/**
 * Merge one fresh stats response into the cache. STALE DATA RULE: when the
 * fresh response carries no fields (e.g. error_kind "disabled" before any
 * data, or a degenerate error) but we already have stored fields, keep the
 * old fields and only update the ok/error metadata. Cached stats are never
 * cleared by an error.
 */
function mergeStatsEntry(id: string, fresh: TrackerStatsResponse) {
  const old = state.statsCache[id];
  const freshHasFields = fresh.fields && Object.keys(fresh.fields).length > 0;
  if (!freshHasFields && old?.fields && Object.keys(old.fields).length > 0) {
    fresh = { ...fresh, fields: old.fields };
  }
  if (!fresh.fields) fresh = { ...fresh, fields: {} };
  state.setStatsCacheEntry(id, fresh);
}

async function refreshAllStats(force = false) {
  const icon = document.getElementById('refresh-icon');
  const btn  = document.getElementById('refresh-btn') as HTMLButtonElement;
  icon?.classList.add('spinning');
  if (btn) btn.disabled = true;
  state.trackers.forEach(t => setCardLoading(t.id, true));

  const { ok, data } = await api.fetchBulkStats(force);
  if (ok && data) {
    // Merge each entry — never wholesale-replace the cache, so trackers
    // missing from the response (or errored without fields) keep old data.
    for (const [id, resp] of Object.entries(data)) mergeStatsEntry(id, resp);
    state.trackers.forEach(t => {
      renderCard(t, state.statsCache[t.id], state.appSettings, state.groupDefs);
    });
    renderTable();
  } else {
    // Server unreachable — keep everything currently displayed.
    state.trackers.forEach(t => setCardLoading(t.id, false));
  }

  icon?.classList.remove('spinning');
  if (btn) btn.disabled = false;
  lastRefreshAt = Date.now();
  renderLastRefresh();
  updateSummary();
  renderAggCards(state.trackers, state.statsCache, state.historyData, state.appSettings);
}

// Relative freshness stamp — "Updated 2m ago" reads faster than a clock
// time; the absolute timestamp lives in the hover title. Re-rendered on a
// timer so it stays honest between refreshes.
let lastRefreshAt = 0;
function renderLastRefresh() {
  const lr = document.getElementById('last-refresh');
  if (!lr || !lastRefreshAt) return;
  const s = Math.floor((Date.now() - lastRefreshAt) / 1000);
  const rel = s < 10 ? 'just now'
    : s < 60 ? `${s}s ago`
    : s < 3600 ? `${Math.floor(s / 60)}m ago`
    : `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m ago`;
  lr.textContent = `Updated ${rel}`;
  lr.title = new Date(lastRefreshAt).toLocaleString();
}
setInterval(renderLastRefresh, 15_000);

async function refreshSingle(id: string) {
  setCardLoading(id, true);
  const { ok, data } = await api.fetchSingleStats(id);
  if (ok && data) {
    mergeStatsEntry(id, data);
  }
  // !ok (network/404): leave the cached entry untouched — stale beats blank.
  const t = state.trackers.find(x => x.id === id);
  if (t) renderCard(t, state.statsCache[id], state.appSettings, state.groupDefs);
  setCardLoading(id, false);
  renderTable();
  updateSummary();
  renderAggCards(state.trackers, state.statsCache, state.historyData, state.appSettings);
}
(window as any).refreshSingle = refreshSingle;

// ── History ───────────────────────────────────────────────────────────────
async function loadHistory() {
  const { ok, data } = await api.fetchHistory(48);
  if (ok && Array.isArray(data)) {
    state.setHistoryData(data);
    renderAggCards(state.trackers, state.statsCache, state.historyData, state.appSettings);
    renderTable(); // expanded-row sparklines
  }
}

// ── Profile scraping (POST /api/scrape/{id}) ──────────────────────────────

/**
 * Run a profile scrape for one tracker. 200 → merge the fresh merged view
 * into statsCache. 429 / errors → cache untouched (stale data stays up),
 * with a toast unless silent.
 */
async function scrapeProfile(id: string, silent = false): Promise<void> {
  const t = state.trackers.find(x => x.id === id);
  const res = await api.runScrape(id);
  if (res.ok && res.data) {
    mergeStatsEntry(id, res.data);
    if (t) renderCard(t, state.statsCache[id], state.appSettings, state.groupDefs);
    renderTable();
    updateSummary();
    renderAggCards(state.trackers, state.statsCache, state.historyData, state.appSettings);
    if (!silent) toast(`${t?.name ?? 'Tracker'} profile scraped`, 'success');
  } else if (res.status === 429) {
    // Rate-limited — keep displaying what we have.
    const blocked = res.data as unknown as ScrapeBlocked;
    const reasons: Record<string, string> = {
      opted_out:         'tracker operator opted out — Yata no longer contacts it',
      api_only:          'API only mode is on',
      no_scrape_support: 'tracker type does not support scraping',
      scrape_disabled:   'scraping disabled by tracker operator',
      no_username:       'add your username first (Settings → Trackers)',
      no_cookie:         'add your session cookie first (Settings → Trackers)',
      daily_limit:       'daily scrape limit reached',
      cooldown:          'scrape cooldown active',
    };
    if (!silent) toast(`Scrape blocked — ${reasons[blocked?.error ?? ''] ?? blocked?.error ?? 'rate limited'}`, 'error');
  } else if (res.status !== 0) {
    if (!silent) toast(`Scrape failed: ${errLabel((res.data as any)?.error ?? 'error')}`, 'error');
  }
  await loadScrapeStatus(); // refresh cooldown/limit badges either way
  renderTable();
}
(window as any).scrapeProfile = scrapeProfile;

// ── Targets quick-edit popover (the ONE edit that stays on the dashboard) ──
initTargetsPopover({
  trackers:  () => state.trackers,
  groupDefs: () => state.groupDefs,
  loadTrackers,
  toast,
});
(window as any).openTargetsPopover = openTargetsPopover;

/** Auto-sync: fire allowed profile scrapes during the refresh cycle. */
function autoSyncScrapes() {
  if (state.appSettings.profile_auto_sync === false) return;
  state.trackers
    .filter(t => t.enabled !== false && t.username && t.supports_html_scrape)
    .filter(t => scrapeStatus[t.id]?.allowed)
    .forEach(t => { void scrapeProfile(t.id, true); });
}

// ── Table view ────────────────────────────────────────────────────────────
function renderTable() {
  // Minimal stub — full table rendering is in table.ts but imported here
  // to avoid circular deps; this triggers a re-render via the module.
  import('./views/table').then(m => m.renderTable(
    state.trackers, state.statsCache,
    state.historyData, state.appSettings, state.expandedRows,
    state.sortCol, state.sortDir, colPrefs,
    { onSort: handleSort, onToggleRow: toggleRow },
    state.groupDefs,
  ));
}

function handleSort(col: string) {
  if (state.sortCol === col) state.setSortDir(state.sortDir === 'asc' ? 'desc' : 'asc');
  else { state.setSortCol(col); state.setSortDir('asc'); }
  renderTable();
}

function toggleRow(id: string) {
  state.toggleExpanded(id);
  renderTable();
}
(window as any).toggleRow = toggleRow;

// ── View switching ────────────────────────────────────────────────────────
function applyView(v: ViewMode, rerender: boolean) {
  const gridDiv  = document.getElementById('view-grid');
  const tableDiv = document.getElementById('view-table');
  const pwDiv    = document.getElementById('view-pathways');
  const btnGrid  = document.getElementById('btn-grid-view');
  const btnTable = document.getElementById('btn-table-view');
  const btnPw    = document.getElementById('btn-pathways-view');
  const onSettings = isSettingsRoute();
  if (gridDiv)  gridDiv.style.display  = (!onSettings && v === 'grid')  ? 'block' : 'none';
  if (tableDiv) tableDiv.style.display = (!onSettings && v === 'table') ? 'block' : 'none';
  if (pwDiv)    pwDiv.style.display    = (!onSettings && v === 'pathways') ? 'block' : 'none';
  btnGrid?.classList.toggle('active',  v === 'grid');
  btnTable?.classList.toggle('active', v === 'table');
  btnPw?.classList.toggle('active',    v === 'pathways');
  if (rerender) { renderGridFull(); renderTable(); renderAggCards(state.trackers, state.statsCache, state.historyData, state.appSettings); }
}
(window as any).setView = (v: ViewMode) => {
  state.setCurrentView(v);
  localStorage.setItem('u3d-view', v);
  if (isSettingsRoute()) location.hash = '#/'; // leave settings, then show the view
  applyView(v, true);
};

/** Pathways: show the view button only when the backend has route data. */
async function initPathwaysFeature() {
  const available = await initPathways();
  // A persisted 'pathways' view with the feature off would show an empty
  // page — fall back to the grid in that case.
  if (!available && state.currentView === 'pathways') (window as any).setView('grid');
}

// ── Settings page routing (#/settings ↔ dashboard, no router lib) ─────────
function isSettingsRoute(): boolean {
  return location.hash.startsWith('#/settings');
}

function applyRoute() {
  const onSettings = isSettingsRoute();
  const sp = document.getElementById('settings-page');
  if (sp) sp.style.display = onSettings ? 'block' : 'none';
  applyView(state.currentView, false); // hides both dashboard views on settings
  if (onSettings) void initSettingsPage();
  else logsTab.stopLogsAuto(); // don't poll logs off the settings page
}
window.addEventListener('hashchange', applyRoute);

/** Populate the settings page form + trackers tab from fresh settings. */
async function initSettingsPage() {
  await loadSettings();
  const m = await modalsReady;
  m.openSettingsPage(state.appSettings, state.quiInstancesMeta, settingsDeps());
  renderTrackersTab();
}

/** Switch the active settings tab (sidebar) and panel. */
function switchSettingsTab(tab: string) {
  document.querySelectorAll<HTMLElement>('.settings-tab').forEach(b =>
    b.classList.toggle('active', b.dataset['tab'] === tab));
  document.querySelectorAll<HTMLElement>('.settings-panel').forEach(p => {
    p.style.display = p.id === `settings-tab-${tab}` ? '' : 'none';
  });
  // The Logs tab polls live only while it's the visible tab.
  if (tab === 'logs') logsTab.startLogsAuto({ toast });
  else logsTab.stopLogsAuto();
  // Load the Alerts editor the first time its tab is opened.
  if (tab === 'alerts') void alertsTab.loadAlerts({ toast });
}
(window as any).switchSettingsTab = switchSettingsTab;
(window as any).exportAlerts     = () => alertsTab.exportAlerts();
(window as any).importAlertsFile = (input: HTMLInputElement) => { void alertsTab.importAlertsFile(input); };
(window as any).toggleLogLevel = (lvl: string) => logsTab.toggleLogLevel(lvl);
(window as any).toggleLogPause = logsTab.toggleLogPause;
(window as any).clearLogs      = () => { void logsTab.clearLogs(); };
(window as any).downloadLogs   = logsTab.downloadLogs;

// ── Data: config import/export + history CSV (Settings → General → Data) ────
(window as any).exportConfig    = () => window.open(api.configExportUrl(), '_blank');
(window as any).exportHistoryCsv = () => window.open(api.historyCsvUrl(), '_blank');

/** Import a config file — destructive replace, guarded by a confirm dialog.
 *  The backend backs up the current config first. */
async function importConfigFile(input: HTMLInputElement) {
  const file = input.files?.[0];
  if (!file) return;
  const text = await file.text();
  input.value = ''; // allow re-picking the same file later
  if (!confirm('Import this config?\n\nThis OVERWRITES your current trackers and settings — that data will be lost. A backup of your current config is saved automatically first.')) return;
  const { ok, data } = await api.importConfig(text);
  if (ok && data.ok) {
    toast('Config imported — previous config backed up', 'success');
    await loadSettings();
    await loadTrackers();
    await refreshAllStats(true); // fresh config → force through the guard
    renderGridFull();
    renderTable();
    if (isSettingsRoute()) await initSettingsPage();
  } else {
    toast(`Import failed: ${errLabel((data as any)?.error ?? 'invalid config')}`, 'error');
  }
}
(window as any).importConfigFile = (input: HTMLInputElement) => { void importConfigFile(input); };

/** Navigate to Settings → Trackers (used by dashboard Edit / Add buttons). */
function gotoSettingsTrackers() {
  if (!isSettingsRoute()) { location.hash = '#/settings'; applyRoute(); }
  switchSettingsTab('trackers');
}

// ── QUI settings form helpers ─────────────────────────────────────────────
/** Read the url/key currently TYPED in the QUI form (may be unsaved). */
function quiFormOverride(): { url?: string; key?: string } {
  const url = (document.getElementById('s-qui-url') as HTMLInputElement | null)?.value.trim();
  const key = (document.getElementById('s-qui-key') as HTMLInputElement | null)?.value.trim();
  return { ...(url ? { url } : {}), ...(key ? { key } : {}) };
}

/** "Reload" button — tests the credentials currently in the form. */
async function loadQuiInstancesForSettings() {
  const btn = document.getElementById('s-qui-refresh-btn') as HTMLButtonElement | null;
  if (btn) btn.disabled = true;
  const res = await renderQUIInstanceChecklist(state.appSettings, state.quiInstancesMeta, quiFormOverride());
  if (btn) btn.disabled = false;
  if (!res.ok) toast(`Could not reach QUI — ${errLabel(res.error ?? 'connection_error')}`, 'error');
}
(window as any).loadQuiInstancesForSettings = loadQuiInstancesForSettings;

/** Debounced silent reload while typing in the QUI url/key fields. */
let quiInstLoadTimer: ReturnType<typeof setTimeout> | null = null;
function scheduleQuiInstanceLoad() {
  if (quiInstLoadTimer) clearTimeout(quiInstLoadTimer);
  quiInstLoadTimer = setTimeout(() => {
    void renderQUIInstanceChecklist(state.appSettings, state.quiInstancesMeta, quiFormOverride());
  }, 700);
}
(window as any).scheduleQuiInstanceLoad = scheduleQuiInstanceLoad;

// ── Summary pills ─────────────────────────────────────────────────────────
// The grid-view summary pills (Trackers/Active/Seeding/Leeching) were removed
// in the v2.4 UI tweaks — the big agg cards cover these. This function is kept
// as a safe no-op so all existing call sites keep working; the per-pill DOM
// writes are gone (their target elements no longer exist).
function updateSummary() { /* summary pills removed — agg cards cover totals */ }

// ── Refresh scheduling ────────────────────────────────────────────────────
async function fullRefreshCycle(force = false) {
  await refreshAllStats(force);
  await loadHistory();
  await loadScrapeStatus();
  autoSyncScrapes();
}

function scheduleRefresh() {
  if (refreshTimer) clearInterval(refreshTimer);
  refreshTimer = setInterval(() => { void fullRefreshCycle(); }, refreshMs());
}

function scheduleQuiRefresh() {
  if (quiTimer) clearInterval(quiTimer);
  quiTimer = setInterval(() => refreshQuiStats(state.appSettings), quiRefreshMs());
}

/** Re-arm both timers with the latest settings — call after a settings save so
 *  a changed interval takes effect immediately (not on next app load). */
function rescheduleTimers() {
  scheduleRefresh();
  scheduleQuiRefresh();
}

async function loadQUIInstances() {
  if (!state.appSettings.qui_url?.trim()) return;
  const { ok, data } = await api.fetchQUIInstances();
  if (ok && Array.isArray(data)) state.setQUIInstancesMeta(data);
}

// ── Global event wiring ───────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', () => {
  document.getElementById('refresh-btn')?.addEventListener('click', async () => {
    scheduleRefresh();
    await fullRefreshCycle(true); // explicit user action → force, bypass guard
  });
  // Gear button → full settings page (hash route, not a modal)
  document.getElementById('settings-btn')?.addEventListener('click', () => {
    if (!isSettingsRoute()) { location.hash = '#/settings'; applyRoute(); }
  });
  // Eye button → quick privacy (username blur) toggle, persisted
  document.getElementById('privacy-btn')?.addEventListener('click', () => { void togglePrivacyQuick(); });

  // Settings sidebar tabs
  document.querySelectorAll<HTMLElement>('.settings-tab').forEach(b =>
    b.addEventListener('click', () => switchSettingsTab(b.dataset['tab'] ?? 'general')));

  // Main settings tabs save automatically on modify (qui-style) — only
  // editor-style tabs (Trackers, Alerts) keep explicit actions.
  void modalsReady.then(m => m.wireSettingsAutoSave());

  // Trackers tab buttons
  document.getElementById('trk-add-btn')?.addEventListener('click', () => (window as any).openAddModal?.());
  document.getElementById('trk-test-all-btn')?.addEventListener('click', () => { void trackersTab.testAllTrackers(); });
  document.getElementById('prowlarr-toggle')?.addEventListener('click', () => (window as any).prowlarrToggle?.());
  document.getElementById('jackett-toggle')?.addEventListener('click', () => (window as any).jackettToggle?.());

  // Overlay close — only when BOTH mousedown and mouseup land on the backdrop
  // itself. A click that starts inside the dialog (e.g. selecting text) and
  // ends on the backdrop must NOT close it.
  wireOverlayClose(document.getElementById('col-modal'), closeColModal);

  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') { (window as any).closeModal?.(); closeColModal(); closeTargetsPopover(); }
  });

  // Re-render sparklines whenever a theme is applied so they pick up the new CSS variable values
  document.addEventListener('themechange', () => {
    renderAggCards(state.trackers, state.statsCache, state.historyData, state.appSettings);
  });

  // ── Mirror scrollbar (top of table) ──────────────────────────────────────
  // Inject a thin div directly above .table-scroll that shows the same
  // horizontal scrollbar. This means it's always reachable right under the
  // column headers without needing to scroll to the bottom of a long list.
  const scrollEl = document.querySelector<HTMLElement>('.table-scroll');
  if (scrollEl) {
    // Create mirror strip
    const mirror = document.createElement('div');
    mirror.className = 'table-scroll-mirror';
    const mirrorInner = document.createElement('div');
    mirrorInner.className = 'table-scroll-mirror-inner';
    mirror.appendChild(mirrorInner);
    scrollEl.parentElement!.insertBefore(mirror, scrollEl);

    // Sync inner width → mirror inner, so scrollbar range matches table
    const syncMirrorWidth = () => {
      mirrorInner.style.width = `${scrollEl.scrollWidth}px`;
      scrollEl.style.setProperty('--table-scroll-w', `${scrollEl.clientWidth}px`);
    };
    syncMirrorWidth();
    const ro = new ResizeObserver(syncMirrorWidth);
    ro.observe(scrollEl);
    // Also watch the table itself — its min-width changes when columns are added/removed
    const tableEl = scrollEl.querySelector('.tracker-table');
    if (tableEl) ro.observe(tableEl);

    // Bidirectional scroll sync — no feedback loop guard needed since
    // setting scrollLeft on the listener target during its own event is a no-op
    let syncing = false;
    mirror.addEventListener('scroll', () => {
      if (syncing) return; syncing = true;
      scrollEl.scrollLeft = mirror.scrollLeft;
      syncing = false;
    });
    scrollEl.addEventListener('scroll', () => {
      if (syncing) return; syncing = true;
      mirror.scrollLeft = scrollEl.scrollLeft;
      syncing = false;
    });
  }
});

// ── Countdown tickers ─────────────────────────────────────────────────────
setInterval(() => {
  const now = Date.now();
  document.querySelectorAll<HTMLElement>('.event-countdown[data-ends-at]').forEach(el => {
    const endsAt = parseInt(el.dataset['endsAt'] ?? '0', 10);
    if (!endsAt || isNaN(endsAt)) { el.textContent = '—'; return; }
    const rem = Math.max(0, Math.floor((endsAt * 1000 - now) / 1000));
    if (rem <= 0) { el.textContent = 'Ended'; return; }
    const d = Math.floor(rem / 86400);
    const h = Math.floor((rem % 86400) / 3600);
    const m = Math.floor((rem % 3600) / 60);
    const s = rem % 60;
    el.textContent = (d > 0 ? d + 'd ' : '') +
      String(h).padStart(2, '0') + 'h ' +
      String(m).padStart(2, '0') + 'm ' +
      String(s).padStart(2, '0') + 's';
  });
}, 1000);

// ── Loading helper ────────────────────────────────────────────────────────
function setCardLoading(id: string, loading: boolean) {
  document.getElementById(`card-${id}`)?.classList.toggle('loading', loading);
}

// ── Settings / tracker-panel dependency bundles ───────────────────────────
const settingsDeps = () => ({
  loadSettings, loadQUIInstances,
  renderQuiBarsWrapper: () => { renderQuiBars(state.appSettings, state.quiInstancesMeta); },
  refreshQuiStatsWrapper: () => refreshQuiStats(state.appSettings),
  renderQUIInstanceChecklist: async () => { await renderQUIInstanceChecklist(state.appSettings, state.quiInstancesMeta, quiFormOverride()); },
  renderTable,
  renderGrid: () => renderGridFull(),
  applyQuiInstances: (ids: number[]) => { state.appSettings.qui_enabled_instances = ids; renderQuiBars(state.appSettings, state.quiInstancesMeta); refreshQuiStats(state.appSettings); },
  applyFavicon: (on: boolean) => { state.appSettings.show_favicons = on; renderTable(); renderGridFull(); },
  applyNameMode: (mode: string) => { state.appSettings.tracker_name_mode = mode; renderTable(); renderGridFull(); },
  applyQuiBarsVisible: (on: boolean) => { state.appSettings.qui_bars_visible = on; renderQuiBars(state.appSettings, state.quiInstancesMeta); },
  applyStatSources: (on: boolean) => { state.appSettings.show_stat_sources = on; renderTable(); renderGridFull(); },
  rescheduleTimers,
  toast,
});

// ── Tracker panel / settings wiring (modals.ts, exposed on window) ────────
modalsReady.then(m => {
  const editDeps = () => ({
    loadTrackers, refreshSingle, toast,
    scrapeProfile: (id: string) => scrapeProfile(id),
    loadSettings, renderTable,
    renderGrid: () => renderGridFull(),
    renderAggCards: () => renderAggCards(state.trackers, state.statsCache, state.historyData, state.appSettings),
    updateSummary,
  });

  // Edit/Add open the inline panel on the Settings → Trackers tab; navigate
  // there first when invoked from the dashboard.
  (window as any).openEditModal   = (id: string) => { gotoSettingsTrackers(); m.openEditModal(id, state.trackers, state.statsCache, state.appSettings, editDeps()); };
  (window as any).openAddModal    = () => { gotoSettingsTrackers(); void m.openAddModal({ loadTrackers, refreshSingle, toast }); };
  (window as any).closeModal      = m.closeModal;
  (window as any).toggleSettingsSync      = m.toggleSettingsSync;
  (window as any).toggleSettingsFavicon   = m.toggleSettingsFavicon;
  (window as any).toggleSettingsPrivate   = m.toggleSettingsPrivate;
  (window as any).toggleSettingsQuiBars   = m.toggleSettingsQuiBars;
  (window as any).toggleSettingsStatSources = m.toggleSettingsStatSources;
  (window as any).onQuiInstanceToggle     = m.onQuiInstanceToggle;
  (window as any).saveSettings            = () => m.saveSettings(settingsDeps());
  (window as any).reloadDefs              = () => m.reloadDefs(toast);
  (window as any).openColCustomizer  = () => { openColCustomizer(colPrefs); };
  (window as any).closeColModal   = () => { closeColModal(); };
  (window as any).toggleColVisible = (key: string, el: Element) => { toggleColVisible(key, el, colPrefs); };
  (window as any).resetColPrefs   = () => { colPrefs = resetColPrefs(); openColCustomizer(colPrefs); };
  (window as any).saveTracker     = () => m.saveTracker({ loadTrackers, refreshSingle, toast });
  (window as any).modalTestTracker = () => { void m.modalTestTracker(); };
  (window as any).modalAddTargetRow    = m.modalAddTargetRow;
  (window as any).modalRemoveTargetRow = m.modalRemoveTargetRow;
  (window as any).showDeleteConfirm    = m.showDeleteConfirm;
  (window as any).closeDeletePopup     = m.closeDeletePopup;
  (window as any).confirmDeletePopup   = () => m.confirmDeletePopup(state.trackers, state.statsCache, state.expandedRows, { loadTrackers, toast });
  (window as any).toggleEnabled        = m.toggleEnabled;
  (window as any).onAddTrackerSelect   = m.onAddTrackerSelect;
  (window as any).onAddTypeSelect      = m.onAddTypeSelect;
  (window as any).modalToggleApiOnly       = m.modalToggleApiOnly;
  (window as any).modalValidateInterval    = m.modalValidateInterval;
  (window as any).modalOnAutoIntervalChange = m.modalOnAutoIntervalChange;
  (window as any).modalOnMaxScrapesChange  = m.modalOnMaxScrapesChange;
  (window as any).selectTheme          = m.selectTheme;
  (window as any).checkTrackerOptOut   = m.checkTrackerOptOut;
  (window as any).accountSetup          = () => m.accountSetup();
  (window as any).accountChangePassword = () => m.accountChangePassword();
  (window as any).accountDisable        = () => m.accountDisable();
  (window as any).backupNow             = () => m.backupNow();
  (window as any).checkForUpdates       = () => { void m.checkForUpdates(); };
  (window as any).toggleAutoUpdate      = () => { void m.toggleAutoUpdate(); };
});

// ── Trackers tab wiring (trackersTab.ts, exposed on window) ───────────────
(window as any).trkToggleEnabled = (id: string) => { void trackersTab.trkToggleEnabled(id); };
(window as any).trkTest          = (id: string) => { void trackersTab.trkTest(id); };
(window as any).trkAskDelete     = trackersTab.trkAskDelete;
(window as any).trkCancelDelete  = trackersTab.trkCancelDelete;
(window as any).trkConfirmDelete = (id: string) => { void trackersTab.trkConfirmDelete(id); };
(window as any).prowlarrToggle  = () => trackersTab.toggleImportSection('prowlarr');
(window as any).prowlarrFetch   = () => { void trackersTab.fetchImportIndexers('prowlarr'); };
(window as any).prowlarrImport  = () => { void trackersTab.importSelected('prowlarr'); };
(window as any).jackettToggle   = () => trackersTab.toggleImportSection('jackett');
(window as any).jackettFetch    = () => { void trackersTab.fetchImportIndexers('jackett'); };
(window as any).jackettImport   = () => { void trackersTab.importSelected('jackett'); };
(window as any).updateImportBtn = trackersTab.updateImportBtn;

function closeColModal() {
  document.getElementById('col-modal')?.classList.remove('open');
  renderTable();
}

/**
 * Global modal-backdrop close behaviour: close only when BOTH mousedown AND
 * mouseup hit the backdrop element itself. Fixes the text-selection bug where
 * releasing the mouse outside the dialog closed it and lost changes.
 */
function wireOverlayClose(el: HTMLElement | null, close: () => void) {
  if (!el) return;
  let downOnBackdrop = false;
  el.addEventListener('mousedown', e => { downOnBackdrop = e.target === el; });
  el.addEventListener('mouseup', e => {
    if (downOnBackdrop && e.target === el) close();
    downOnBackdrop = false;
  });
}
