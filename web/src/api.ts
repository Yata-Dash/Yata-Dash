// api.ts — all HTTP calls to the Go backend (v2 unified-stats API)
// To add a new endpoint: add a typed function here. Nothing else needs changing.
import type {
  AlertRule, ApiTokenInfo, AppSettings, AuthStatus, BackupsResponse, DefsPayload, DefsReloadResult, DryRunResult, HistoryPoint,
  HistorySeriesResponse,
  LogsResponse, NotificationConfig, NotifyDestination, PathwayFromResponse, PathwayPathsResponse,
  PathwayTargetsResponse, ProwlarrIndexer,
  ScrapeStatusMap, StatsMap, TestStatusMap, ThemeInfo, Tracker, TrackerGroupMap,
  TrackerPayload, TrackerStatsResponse, TrackerTestOverrides, TrackerTestResult, UpdateStatus,
} from './types';

const JSON_HEADERS = { 'Content-Type': 'application/json' };

export type ApiResult<T> = { ok: boolean; status: number; data: T };

// Fired when a protected endpoint returns 401 (session expired / not logged in).
// main.ts registers a handler that shows the login gate. Auth endpoints are
// exempt so a failed login doesn't recurse into the gate.
let onUnauthorized: (() => void) | null = null;
export function setUnauthorizedHandler(fn: () => void) { onUnauthorized = fn; }

async function call<T>(path: string, opts: RequestInit = {}): Promise<ApiResult<T>> {
  try {
    const res = await fetch(path, { headers: JSON_HEADERS, ...opts });
    if (res.status === 401 && !path.startsWith('/api/auth/')) onUnauthorized?.();
    const data = await res.json().catch(() => ({}) as T);
    return { ok: res.ok, status: res.status, data: data as T };
  } catch {
    // Network failure (server unreachable) — callers must keep cached data.
    return { ok: false, status: 0, data: {} as T };
  }
}

// ── Auth (single-user basic auth) ──────────────────────────────────────────
export type AuthResult = { ok: boolean; username?: string; error?: string; retry_after?: number; can_reset?: boolean };

export const fetchAuthStatus = () => call<AuthStatus>('/api/auth/status');

export const authLogin = (username: string, password: string) =>
  call<AuthResult>('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) });

export const authSetup = (username: string, password: string) =>
  call<AuthResult>('/api/auth/setup', { method: 'POST', body: JSON.stringify({ username, password }) });

export const authLogout = () =>
  call<AuthResult>('/api/auth/logout', { method: 'POST' });

export const authChangePassword = (password: string, new_password: string) =>
  call<AuthResult>('/api/auth/password', { method: 'POST', body: JSON.stringify({ password, new_password }) });

export const authDisable = (password: string) =>
  call<AuthResult>('/api/auth/disable', { method: 'POST', body: JSON.stringify({ password }) });

/** Recovery: wipe the account + all config/data so a locked-out or
 *  forgotten-password user can get back in (only works when not logged in).
 *  Requires the recovery code printed to the server console/log at startup. */
export const authReset = (reset_code: string) =>
  call<AuthResult>('/api/auth/reset', { method: 'POST', body: JSON.stringify({ reset_code }) });

// ── Logs (rolling logger) ───────────────────────────────────────────────────
export const fetchLogs = (limit = 500) =>
  call<LogsResponse>(`/api/logs?limit=${limit}`);

export const clearLogs = () =>
  call<{ ok: boolean }>('/api/logs', { method: 'DELETE' });

/** Download URL for the full rotating log file. */
export const logsDownloadUrl = () => '/api/logs/download';

// ── Config import/export + backups ──────────────────────────────────────────
export const configExportUrl = () => '/api/config/export';
export const historyCsvUrl = () => '/api/history/export.csv';

/** Import a full config (raw JSON text). Backend backs up the current one first. */
export const importConfig = (json: string) =>
  call<{ ok: boolean; error?: string }>('/api/config/import', { method: 'POST', body: json });

export const fetchBackups = () => call<BackupsResponse>('/api/backups');
export const createBackup = () => call<{ ok: boolean }>('/api/backups', { method: 'POST' });

// ── Alerts & notifications ──────────────────────────────────────────────────
export const fetchNotifications = () => call<NotificationConfig>('/api/notifications');
export const saveNotifications = (n: NotificationConfig) =>
  call<NotificationConfig>('/api/notifications', { method: 'PUT', body: JSON.stringify(n) });
export const testNotification = (dest: NotifyDestination) =>
  call<{ ok: boolean; error?: string }>('/api/notifications/test', { method: 'POST', body: JSON.stringify(dest) });
export const dryRunRule = (rule: AlertRule) =>
  call<{ results: DryRunResult[]; error?: string }>('/api/notifications/dryrun', { method: 'POST', body: JSON.stringify(rule) });
export const notificationsExportUrl = () => '/api/notifications/export';

/** Builds the weekly digest text without sending or mutating any state. */
export const fetchDigestPreview = () => call<{ text: string }>('/api/notifications/digest/preview');

/** Builds and sends the digest right now, independent of the schedule. */
export const sendDigestNow = () =>
  call<{ ok: boolean; sent_to: number; error?: string }>('/api/notifications/digest/send', { method: 'POST' });

// ── Trackers ──────────────────────────────────────────────────────────────

export const fetchTrackers = () => call<Tracker[]>('/api/trackers');

export const addTracker = (payload: TrackerPayload) =>
  call<Tracker>('/api/trackers', { method: 'POST', body: JSON.stringify(payload) });

export const updateTracker = (id: string, payload: TrackerPayload) =>
  call<Tracker>(`/api/trackers/${id}`, { method: 'PUT', body: JSON.stringify(payload) });

export const deleteTracker = (id: string) =>
  call<{ ok: boolean }>(`/api/trackers/${id}`, { method: 'DELETE' });

/** Actively test a tracker's API + profile scrape (real requests). Optional
 *  overrides test the CURRENT edit-panel form values (e.g. an unsaved
 *  cookie) instead of only what's saved — omit to test the stored values. */
export const testTracker = (id: string, overrides?: TrackerTestOverrides) =>
  call<TrackerTestResult>(`/api/trackers/${id}/test`, {
    method: 'POST',
    ...(overrides ? { body: JSON.stringify(overrides) } : {}),
  });

/** Ad-hoc connectivity test for a tracker that hasn't been added yet
 *  (Add-mode Test button). Body is the same shape as addTracker's payload.
 *  Never persisted — the modal shows the result directly. */
export const testTrackerAdhoc = (payload: TrackerPayload) =>
  call<TrackerTestResult>('/api/trackers/test-adhoc', { method: 'POST', body: JSON.stringify(payload) });

/** Cached last-test results for all trackers (absent = not tested yet). */
export const fetchTestStatus = () =>
  call<TestStatusMap>('/api/trackers/test-status');

// ── Stats (unified merged view) ───────────────────────────────────────────

/** force=true (manual refresh button / post-import) bypasses the server's
 *  min-age guard so the API is hit immediately; the auto-poll omits it. */
export const fetchBulkStats = (force = false) =>
  call<StatsMap>(`/api/stats${force ? '?force=1' : ''}`);

export const fetchSingleStats = (id: string) =>
  call<TrackerStatsResponse>(`/api/stats/${id}`);

// ── Profile scraping ──────────────────────────────────────────────────────

/** Run a profile scrape. 200 → fresh TrackerStatsResponse; 429 → ScrapeBlocked. */
export const runScrape = (id: string) =>
  call<TrackerStatsResponse>(`/api/scrape/${id}`, { method: 'POST' });

export const fetchScrapeStatus = () =>
  call<ScrapeStatusMap>('/api/scrape-status');

// ── History ───────────────────────────────────────────────────────────────

export const fetchHistory = (hours = 48) =>
  call<HistoryPoint[]>(`/api/history?hours=${hours}`);

/** History-view data feed. Omitted trackers/fields = all recorded. */
export const fetchPathwaysFrom = (trackerId: string) =>
  call<PathwayFromResponse>(`/api/pathways/from?tracker=${encodeURIComponent(trackerId)}`);

export const fetchHistorySeries = (opts: { trackers?: string[]; fields?: string[]; range?: string; granularity?: string }) => {
  const qs = new URLSearchParams();
  if (opts.trackers?.length) qs.set('trackers', opts.trackers.join(','));
  if (opts.fields?.length)   qs.set('fields', opts.fields.join(','));
  if (opts.range)            qs.set('range', opts.range);
  if (opts.granularity)      qs.set('granularity', opts.granularity);
  const q = qs.toString();
  return call<HistorySeriesResponse>(`/api/history/series${q ? `?${q}` : ''}`);
};

// ── API tokens (read-only integration tokens) ─────────────────────────────

export const fetchApiTokens = () => call<ApiTokenInfo[]>('/api/tokens');

/** Create a token. The response's `token` is the plaintext — shown ONCE. */
export const createApiToken = (name: string) =>
  call<{ token: string; info: ApiTokenInfo }>('/api/tokens', {
    method: 'POST',
    body: JSON.stringify({ name }),
  });

export const revokeApiToken = (id: string) =>
  call<{ ok: boolean }>(`/api/tokens/${id}`, { method: 'DELETE' });

// ── Settings ──────────────────────────────────────────────────────────────

export const fetchSettings = () => call<AppSettings>('/api/settings');

export const fetchVersion = () => call<{ version: string }>('/api/version');

// ── Update check (versions.json on the repo; contacts GitHub only on demand) ──
export const fetchUpdateStatus = () => call<UpdateStatus>('/api/updates');
export const runUpdateCheck = () => call<UpdateStatus>('/api/updates/check', { method: 'POST' });

/** PUT /api/settings is a FULL REPLACE — always send the complete object. */
export const saveSettings = (payload: AppSettings) =>
  call<AppSettings>('/api/settings', { method: 'PUT', body: JSON.stringify(payload) });

// ── Tracker definitions ───────────────────────────────────────────────────

export const fetchDefs = () => call<DefsPayload>('/api/defs');

export const reloadDefs = () =>
  call<DefsReloadResult>('/api/defs/reload', { method: 'POST' });

export const fetchTrackerGroups = () =>
  call<TrackerGroupMap>('/api/tracker-groups');

// ── Pathways ──────────────────────────────────────────────────────────────

/** 404 ({error:"pathways_data_missing"}) = feature off — hide the view. */
export const fetchPathwayTargets = () =>
  call<PathwayTargetsResponse>('/api/pathways/targets');

export const fetchPathwayPaths = (target: string) =>
  call<PathwayPathsResponse>(`/api/pathways/paths?target=${encodeURIComponent(target)}`);

// ── Mock / demo trackers ──────────────────────────────────────────────────

export const fetchMockScenarios = () => call<string[]>('/api/mock/scenarios');

// ── QUI ───────────────────────────────────────────────────────────────────

/**
 * GET /api/qui/instances — the optional url/key override the STORED settings
 * so the settings form can test credentials that haven't been saved yet.
 * A key equal to the mask sentinel makes the backend use the stored key.
 */
export const fetchQUIInstances = (url?: string, key?: string) => {
  const qs = new URLSearchParams();
  if (url) qs.set('url', url);
  if (key) qs.set('key', key);
  const q = qs.toString();
  return call<{ id: number; name: string; connected: boolean; host: string }[]>(
    `/api/qui/instances${q ? `?${q}` : ''}`);
};

export const fetchQUIStats = (instanceId: number) =>
  call<Record<string, unknown>>(`/api/qui/stats?id=${instanceId}`);

// ── Prowlarr / Jackett imports ────────────────────────────────────────────
// Both proxy the manager's indexer list; the backend saves the connection
// (URL + secret) on a successful fetch so the sections come prefilled.

export const fetchProwlarrIndexers = (url: string, apiKey: string) =>
  call<ProwlarrIndexer[]>('/api/prowlarr/indexers', {
    method: 'POST',
    body: JSON.stringify({ url, api_key: apiKey }),
  });

export const fetchJackettIndexers = (url: string, adminPassword: string) =>
  call<ProwlarrIndexer[]>('/api/jackett/indexers', {
    method: 'POST',
    body: JSON.stringify({ url, admin_password: adminPassword }),
  });

// ── Themes ────────────────────────────────────────────────────────────────

export const fetchThemes = () =>
  call<ThemeInfo[]>('/api/themes');

/**
 * Apply a theme by setting data-theme on <html> and loading/unloading the
 * theme stylesheet.  Safe to call with "" or "default" to reset to defaults.
 */
export function applyTheme(themeId: string) {
  const id = (!themeId || themeId === 'default') ? '' : themeId;
  const html = document.documentElement;

  if (id) {
    html.setAttribute('data-theme', id);
  } else {
    html.removeAttribute('data-theme');
  }

  // Load / swap the theme stylesheet
  let link = document.getElementById('theme-stylesheet') as HTMLLinkElement | null;
  if (id) {
    if (!link) {
      link = document.createElement('link');
      link.id   = 'theme-stylesheet';
      link.rel  = 'stylesheet';
      document.head.appendChild(link);
    }
    // Fire themechange once the new CSS has parsed so sparklines can re-read variables
    const dispatch = () => document.dispatchEvent(new CustomEvent('themechange'));
    link.onload = dispatch;
    link.href = `/static/themes/${id}.css?v=${Date.now()}`;
    // If same href base (e.g. re-selecting active theme) force a refresh
    if (link.sheet) dispatch();
  } else {
    link?.remove();
    // Default theme removes the sheet — dispatch immediately since :root vars apply instantly
    document.dispatchEvent(new CustomEvent('themechange'));
  }
}
