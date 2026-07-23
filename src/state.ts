// state.ts — single source of truth for all mutable app state
import type {
  AppSettings, ColDef, ColPref, HistoryPoint, QUIInstanceMeta, ScrapeStatusMap,
  SortDir, StatField, StatsMap, Tracker, TrackerGroupMap, TrackerStatsResponse,
  ViewMode,
} from './types';

// ── Column definitions ────────────────────────────────────────────────────
// To add a new column: add an entry here. Nothing else needs to change.
// All stat columns read from statsCache[id].fields (the merged stats view).
export const COL_DEFS: ColDef[] = [
  { key: 'name',          label: 'Tracker',       sortable: true,  always: true,  center: false, group: 'core',     defaultVisible: true,  minWidth: 180 },
  { key: 'username',      label: 'User / Group',  sortable: true,  always: true,  center: false, group: 'core',     defaultVisible: true,  minWidth: 130 },
  { key: 'uploaded',      label: 'Uploaded',      sortable: true,  always: false, center: false, group: 'core',     defaultVisible: true,  minWidth:  90 },
  { key: 'downloaded',    label: 'Downloaded',    sortable: true,  always: false, center: false, group: 'core',     defaultVisible: true,  minWidth:  90 },
  { key: 'ratio',         label: 'Ratio',         sortable: true,  always: false, center: false, group: 'core',     defaultVisible: true,  minWidth:  70 },
  { key: 'buffer',        label: 'Buffer',        sortable: true,  always: false, center: false, group: 'core',     defaultVisible: true,  minWidth:  90 },
  { key: 'seed_size',     label: 'Seed Size',     sortable: true,  always: false, center: true,  group: 'core',     defaultVisible: true,  minWidth:  90 },
  { key: 'avg_seed_time', label: 'Avg Seed Time', sortable: true,  always: false, center: true,  group: 'core',     defaultVisible: true,  minWidth: 130 },
  { key: 'seeding',       label: 'Seeding',       sortable: true,  always: false, center: true,  group: 'core',     defaultVisible: true,  minWidth:  70 },
  { key: 'leeching',      label: 'Leeching',      sortable: true,  always: false, center: true,  group: 'core',     defaultVisible: true,  minWidth:  75 },
  { key: 'hit_and_runs',  label: 'H&Rs',          sortable: true,  always: false, center: true,  group: 'core',     defaultVisible: true,  minWidth:  60 },
  { key: 'account_age',   label: 'Account Age',   sortable: true,  always: false, center: false, group: 'core',     defaultVisible: true,  minWidth: 100 },
  { key: 'bonus_points',    label: 'Bonus Points',    sortable: true, always: false, center: true, group: 'extended', defaultVisible: false, minWidth:  95 },
  { key: 'snatched',        label: 'Snatched',        sortable: true, always: false, center: true, group: 'extended', defaultVisible: false, minWidth:  80 },
  { key: 'upload_snatches', label: 'Upload Snatches', sortable: true, always: false, center: true, group: 'extended', defaultVisible: false, minWidth: 110 },
  { key: 'real_ratio',    label: 'Real Ratio',    sortable: true,  always: false, center: true,  group: 'extended', defaultVisible: false, minWidth:  80 },
  { key: 'fl_tokens',     label: 'FL Tokens',     sortable: true,  always: false, center: true,  group: 'extended', defaultVisible: false, minWidth:  80 },
  { key: 'invites',       label: 'Invites',       sortable: true,  always: false, center: true,  group: 'extended', defaultVisible: false, minWidth:  70 },
  { key: 'warnings',      label: 'Warnings',      sortable: true,  always: false, center: true,  group: 'extended', defaultVisible: true,  minWidth:  75 },
  { key: 'total_uploads', label: 'Uploads',       sortable: true,  always: false, center: true,  group: 'extended', defaultVisible: false, minWidth:  75 },
  { key: 'adoptions',     label: 'Adoptions',     sortable: true,  always: false, center: true,  group: 'extended', defaultVisible: false, minWidth:  85 },
  { key: 'reqs_filled',   label: 'Reqs Filled',   sortable: true,  always: false, center: true,  group: 'extended', defaultVisible: false, minWidth:  90 },
  { key: 'scrape_health', label: 'Scrape',        sortable: true,  always: false, center: true,  group: 'extended', defaultVisible: false, minWidth:  80 },
];

// ── Runtime state ─────────────────────────────────────────────────────────

export let trackers: Tracker[] = [];
/** ONE unified stats cache — merged API+scrape fields per tracker. */
export let statsCache: StatsMap = {};
export let historyData: HistoryPoint[] = [];
export let appSettings: AppSettings = {
  theme: '', tracker_name_mode: '', group_name_style: '', username_style: '',
  private_mode: false, show_favicons: false, show_stat_sources: false,
  show_pathway_etas: true, show_trend_estimates: true, show_target_etas: true,
  show_rate_hovers: true, show_unread_mail: true, show_unread_notifications: true,
  show_tracker_rules: true, highlight_hnr: true,
  update_check_auto: false, trust_proxy_headers: false, duration_format: 'ym',
  pathway_favorites: [], pathway_not_interested: [], pathways_include_disabled: false,
  profile_auto_sync: true, api_only_mode: false,
  scrape_interval_minutes: 120, max_scrapes_per_day: 0, auto_interval: false,
  refresh_interval_minutes: 30, qui_refresh_seconds: 10,
  qui_url: 'http://localhost:7476', qui_api_key: '',
  qui_enabled_instances: [], qui_bars_visible: true, qui_seedsize_mode: 'off',
  backup_enabled: false, backup_frequency: 'weekly', backup_keep: 5,
  prowlarr_url: '', prowlarr_api_key: '', jackett_url: '', jackett_admin_password: '',
};
export let groupDefs: TrackerGroupMap = {};
export let quiInstancesMeta: QUIInstanceMeta[] = [];
export let scrapeStatus: ScrapeStatusMap = {};

export let currentView: ViewMode = (localStorage.getItem('u3d-view') as ViewMode) || 'grid';
export let expandedRows = new Set<string>();
// Detail-table sort persists across reloads (same treatment as the view mode).
export let sortCol = localStorage.getItem('u3d-sort-col') ?? '';
export let sortDir: SortDir = (localStorage.getItem('u3d-sort-dir') as SortDir) || 'asc';
export let pendingDelete: string | null = null;
export let modalEnabled = true;

// Setters (keeps mutation explicit and searchable)
export function setTrackers(v: Tracker[]) { trackers = v; }
export function setStatsCache(v: StatsMap) { statsCache = v; }
export function setStatsCacheEntry(id: string, v: TrackerStatsResponse) { statsCache[id] = v; }
export function setHistoryData(v: HistoryPoint[]) { historyData = v; }
export function setAppSettings(v: AppSettings) { appSettings = v; }
export function setGroupDefs(v: TrackerGroupMap) { groupDefs = v; }
export function setQUIInstancesMeta(v: QUIInstanceMeta[]) { quiInstancesMeta = v; }
export function setScrapeStatus(v: ScrapeStatusMap) { scrapeStatus = v; }
export function setCurrentView(v: ViewMode) { currentView = v; }
export function toggleExpanded(id: string) {
  if (expandedRows.has(id)) expandedRows.delete(id); else expandedRows.add(id);
}
export function setSortCol(col: string) { sortCol = col; try { localStorage.setItem('u3d-sort-col', col); } catch { /* private mode */ } }
export function setSortDir(d: SortDir) { sortDir = d; try { localStorage.setItem('u3d-sort-dir', d); } catch { /* private mode */ } }
export function setPendingDelete(id: string | null) { pendingDelete = id; }
export function setModalEnabled(v: boolean) { modalEnabled = v; }

// ── Merged-field accessors ────────────────────────────────────────────────
// STALE DATA RULE: these read fields regardless of resp.ok — when a tracker
// is down or rate-limited, the backend keeps returning the last stored
// fields, and the UI must keep rendering them. Never gate on resp.ok here.

/** Full StatField (value + source + updated_at) for a tracker field. */
export function fieldOf(resp: TrackerStatsResponse | undefined, key: string): StatField | undefined {
  return resp?.fields?.[key];
}

/** Raw value of a merged field, or undefined. */
export function valOf(resp: TrackerStatsResponse | undefined, key: string): unknown {
  const f = resp?.fields?.[key];
  return f === undefined ? undefined : f.value;
}

/** String form of a merged field; '' when missing/empty. */
export function strOf(resp: TrackerStatsResponse | undefined, key: string): string {
  const v = valOf(resp, key);
  return v == null ? '' : String(v);
}

/** Numeric form of a merged field (commas stripped); null when missing/NaN. */
export function numOf(resp: TrackerStatsResponse | undefined, key: string): number | null {
  const v = valOf(resp, key);
  if (v == null || v === '') return null;
  const n = parseFloat(String(v).replace(/,/g, ''));
  return isNaN(n) ? null : n;
}

// ── Column preferences ────────────────────────────────────────────────────

function defaultColPrefs(): ColPref[] {
  return COL_DEFS.map(d => ({ key: d.key, visible: d.defaultVisible }));
}

export function loadColPrefs(): ColPref[] {
  try {
    const saved = JSON.parse(localStorage.getItem('u3d-cols') || 'null') as ColPref[] | null;
    if (!saved) return defaultColPrefs();
    const savedKeys = saved.map(c => c.key);
    const merged = saved.filter(c => COL_DEFS.some(d => d.key === c.key));
    COL_DEFS.forEach(d => {
      if (!savedKeys.includes(d.key)) merged.push({ key: d.key, visible: d.defaultVisible });
    });
    return merged;
  } catch { return defaultColPrefs(); }
}

export function saveColPrefs(prefs: ColPref[]) {
  localStorage.setItem('u3d-cols', JSON.stringify(prefs));
}

export function getVisibleCols(prefs: ColPref[]): ColDef[] {
  return prefs
    .filter(c => c.visible)
    .map(c => COL_DEFS.find(d => d.key === c.key))
    .filter(Boolean) as ColDef[];
}

export function resetColPrefs(): ColPref[] {
  const prefs = defaultColPrefs();
  saveColPrefs(prefs);
  return prefs;
}

// ── Tracker display order (local — backend has no reorder endpoint) ───────

const ORDER_KEY = 'u3d-order';

/** Apply the locally saved drag-reorder order to a tracker list. */
export function applySavedOrder(list: Tracker[]): Tracker[] {
  try {
    const order = JSON.parse(localStorage.getItem(ORDER_KEY) || 'null') as string[] | null;
    if (!order) return list;
    const pos = new Map(order.map((id, i) => [id, i]));
    return [...list].sort((a, b) =>
      (pos.get(a.id) ?? Number.MAX_SAFE_INTEGER) - (pos.get(b.id) ?? Number.MAX_SAFE_INTEGER));
  } catch { return list; }
}

export function saveOrder(ids: string[]) {
  localStorage.setItem(ORDER_KEY, JSON.stringify(ids));
}
