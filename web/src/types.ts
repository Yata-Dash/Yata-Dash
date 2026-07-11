// types.ts — all shared TypeScript interfaces (v2 unified-stats API)
// To add a new field to a tracker or stat: update the interface here first.

// ── Trackers ──────────────────────────────────────────────────────────────

/** TrackerView — the safe public representation returned by GET /api/trackers */
export interface Tracker {
  id: string;
  name: string;
  abbr: string;        // from def registry; "" for manual trackers
  def_key: string;     // trackerdef registry key e.g. "fearnopeer"; "" for manual
  url: string;
  type: string;        // "unit3d" | "gazelle" | "custom" | "test" | ...
  enabled: boolean;
  has_key: boolean;
  api_key_masked: string;  // "••••••••" or ""
  has_session: boolean;
  username: string;
  /** Canonical field -> target value, e.g. {"uploaded": "10 TiB", "ratio": "1.05"} */
  targets: Record<string, string>;
  target_group: string;    // group name whose requirements are used as targets; "" = manual
  /** User-entered account creation date (YYYY-MM-DD); fallback when the
   *  tracker reports none. "" = unset. */
  join_date?: string;
  /** Per-tracker user override; 0 = unset (use global). */
  min_scrape_interval_minutes: number;
  /** Per-tracker daily cap; 0 = unset. */
  max_scrapes_per_day: number;
  /** Derive the per-tracker interval from max_scrapes_per_day. */
  auto_interval: boolean;
  /** Per-tracker API-only (no profile scraping for this tracker). */
  api_only: boolean;
  /** Def operator's requested minimum interval / daily cap (0 = none). */
  tracker_min_interval: number;
  tracker_max_per_day: number;
  mock_scenario?: string;
  /** False when the type architecturally cannot scrape OR the operator forbids it. */
  supports_html_scrape: boolean;
  /** True when the tracker def itself disables scraping (operator request). */
  scrape_disabled_by_tracker: boolean;
  api_key_hint?: string;
  /** Direct link to the user's profile page ("" / absent when unknown). */
  profile_url?: string;
  /** Extra config fields this tracker's type needs (e.g. gazelle: ["username"]). */
  required_fields?: string[];
  /** Tracker's account-wide required ratio (0/absent = unknown). */
  min_ratio?: number;
  /** Def staff-approval status: approved | informal | pending | unknown.
   *  Manual trackers report "unknown"; the UI warns unless "approved". */
  def_approval?: string;
  def_approval_note?: string; // informal-OK note, shown in the tooltip
  /** True when this already-added tracker's host is now on the opt-out list:
   *  its operator has asked not to be supported, so Yata has STOPPED all API
   *  + scrape traffic to it. The row flags this so the user knows why it went
   *  quiet. opted_out_note carries the public note, if any. */
  opted_out?: boolean;
  opted_out_note?: string;
}

/** Sentinel value meaning "credential unchanged" in PUT/POST payloads. */
export const MASKED_KEY = '••••••••';

/** Create/update payload for POST /api/trackers and PUT /api/trackers/{id}. */
export interface TrackerPayload {
  name?: string;
  url?: string;
  type?: string;
  api_key?: string;        // MASKED_KEY = unchanged; "" = clear
  session_cookie?: string; // MASKED_KEY = unchanged; "" = clear
  username?: string;
  enabled?: boolean;
  min_scrape_interval_minutes?: number;
  max_scrapes_per_day?: number;
  auto_interval?: boolean;
  api_only?: boolean;
  targets?: Record<string, string>;
  target_group?: string;
  join_date?: string;
  mock_scenario?: string;
}

// ── Unified stats ─────────────────────────────────────────────────────────

export type StatSource = 'api' | 'scrape' | 'manual';

/** One merged stat value with provenance. */
export interface StatField {
  value: unknown;
  source: StatSource;
  updated_at: number; // unix seconds
}

/**
 * Per-tracker entry of GET /api/stats. STALE DATA RULE: when ok=false the
 * backend still returns the last stored fields — render them like fresh data
 * plus an offline indicator. Never blank previously displayed stats.
 */
export interface TrackerStatsResponse {
  tracker_id: string;
  ok: boolean;
  error?: string;
  error_kind?: string; // disabled | no_key | timeout | connection_error | http_NNN | parse_error | api_error
  fields: Record<string, StatField>;
  fetched_at: number;  // unix seconds
  rates?: Record<string, number>; // per-day growth (uploaded/downloaded/seed_size in GiB/day, bonus_points raw/day); omitted when flat
}

export type StatsMap = Record<string, TrackerStatsResponse>;

// ── Tracker connectivity test (POST /api/trackers/{id}/test) ───────────────

/** Outcome of one connectivity check (API or scrape). */
export interface CheckResult {
  /** blocked = a scrape now would exceed the rate limits (nothing was sent). */
  status: 'ok' | 'fail' | 'not_configured' | 'not_applicable' | 'blocked' | string;
  /** Error kind (on fail) or reason (not_configured / not_applicable). */
  detail?: string;
  /** Number of fields returned on success. */
  fields?: number;
}

/** Combined API + scrape test for one tracker. */
export interface TrackerTestResult {
  api: CheckResult;
  scrape: CheckResult;
  tested_at: number; // unix seconds
}

export type TestStatusMap = Record<string, TrackerTestResult>;

// ── Scrape status ─────────────────────────────────────────────────────────

export interface ScrapeStatus {
  allowed: boolean;
  reason?: 'opted_out' | 'api_only' | 'no_scrape_support' | 'scrape_disabled' | 'no_username' | 'no_cookie' | 'daily_limit' | 'cooldown';
  next_allowed_at?: number;          // unix sec, set for "cooldown"
  effective_interval_minutes: number;
  effective_max_per_day: number;     // 0 = unlimited
  scrapes_today: number;
  last_scrape_at?: number;           // unix sec, 0/absent = never
  tracker_min_interval?: number;     // operator request, for UI explanation
  supports_html_scrape: boolean;
}

export type ScrapeStatusMap = Record<string, ScrapeStatus>;

/** Body of a 429 response from /api/scrape/{id}. */
export interface ScrapeBlocked {
  error: string;
  policy: ScrapeStatus;
}

// ── History ───────────────────────────────────────────────────────────────

/** Long-format numeric history point from GET /api/history. */
export interface HistoryPoint {
  tracker_id: string;
  recorded_at: number; // unix seconds
  field: string;       // uploaded | downloaded | buffer | seed_size | ratio | seeding | leeching | hit_and_runs | bonus_points | avg_seed_time
  value: number;       // sizes in GiB, durations in seconds
}

// ── API tokens (read-only integration tokens) ─────────────────────────────

export interface ApiTokenInfo {
  id: string;
  name: string;
  prefix: string;       // display hint (yata_xxxxxxxx…) — never the full token
  created_at: number;   // unix seconds
  last_used_at: number; // unix seconds; 0 = never used
}

/** One tracker/field line from GET /api/history/series. */
export interface HistorySeries {
  tracker_id: string;
  field: string;
  unit: 'GiB' | 'count' | 'ratio' | 'seconds';
  points: [number, number][]; // [unixSec, value], oldest first
}

/** A point-in-time timeline marker (group change) from GET /api/history/series. */
export interface HistoryEvent {
  tracker_id: string;
  at: number;     // unix sec
  kind: string;   // "group_change"
  detail: string; // e.g. "Seeker→PowerPool"
}

export interface HistorySeriesResponse {
  range: { from: number; to: number; granularity: 'fine' | 'daily' };
  series: HistorySeries[];
  events: HistoryEvent[];
}

// ── Settings ──────────────────────────────────────────────────────────────

export interface AppSettings {
  theme: string;                    // theme id; "" or "default" = Colorful default
  tracker_name_mode: 'name' | 'both' | 'abbr' | string;
  group_name_style: 'plain' | 'styled' | string;
  username_style: 'plain' | 'group' | string;
  private_mode: boolean;            // blur usernames
  show_favicons: boolean;
  show_stat_sources: boolean;       // per-stat api/scrape origin dot
  show_pathway_etas: boolean | null; // pathway time estimates; null = true
  show_trend_estimates: boolean | null; // per-stat 7-day projection chips; null = true
  show_target_etas: boolean | null; // dashboard target time estimates; null = true
  show_rate_hovers: boolean | null; // per-day trend tooltips on stat hover; null = true
  duration_format: 'ym' | 'days' | string; // "" = "ym" (1Y 9M style)
  profile_auto_sync: boolean;
  api_only_mode: boolean;           // disable all HTML scraping globally
  scrape_interval_minutes: number;  // min 60 — backend enforces
  max_scrapes_per_day: number;      // 0 = unlimited
  auto_interval: boolean;           // derive interval from max_scrapes_per_day
  refresh_interval_minutes: number; // idle API auto-refresh cadence; floor 15 (default 30)
  qui_refresh_seconds: number;      // qui bar refresh cadence; floor 1 (default 10)
  show_unread_mail?: boolean | null;          // null = true — unread envelope icons
  show_unread_notifications?: boolean | null; // null = true — unread bell icons
  update_check_auto?: boolean;                 // opt-in daily update check (default false)
  qui_url: string;
  qui_api_key: string;
  qui_enabled_instances: number[];
  qui_bars_visible: boolean | null; // null = true
  backup_enabled: boolean;          // automatic config backups (opt-in)
  backup_frequency: 'daily' | 'weekly' | 'monthly' | string;
  backup_keep: number;              // retain last N backups (1–99)
  // Indexer-manager imports — saved server-side on first successful fetch;
  // secrets round-trip as the mask sentinel like qui_api_key.
  prowlarr_url: string;
  prowlarr_api_key: string;
  jackett_url: string;
  jackett_admin_password: string;
}

// ── Alerts & notifications (webhooks) ──────────────────────────────────────
export interface NotifyDestination {
  id: string;
  name: string;
  type: 'discord' | 'telegram' | 'gotify' | 'generic' | string;
  url: string;
  token: string;
  chat_id: string;
  enabled: boolean;
}
export interface AlertCondition {
  field: string;
  op: string;
  value: string;
}
export interface AlertRule {
  id: string;
  name: string;
  enabled: boolean;
  tracker_ids: string[];           // trackers included/excluded
  tracker_mode: 'include' | 'exclude' | string;
  tracker_id?: string;             // legacy single-tracker field
  match: 'all' | 'any' | string;
  conditions: AlertCondition[];
  destinations: string[];          // destination ids; empty = all enabled
  cooldown_minutes: number;
}
export interface NotificationConfig {
  destinations: NotifyDestination[];
  rules: AlertRule[];
}

// ── Update check (GET /api/updates, POST /api/updates/check) ────────────────
export interface UpdateComponent {
  current: string;
  latest?: string;          // "" / absent until a check has run
  update_available: boolean;
}
export interface UpdateStatus {
  app: UpdateComponent;
  defs: UpdateComponent;
  pathways: UpdateComponent;
  checked_at?: number;      // unix seconds; absent = never checked
  error?: string;
}
export interface DryRunResult {
  tracker_id: string;
  tracker_name: string;
  matched: boolean;
  detail: string;
}

// ── Config backups ─────────────────────────────────────────────────────────
export interface BackupInfo {
  name: string;
  size: number;
  mod_time: number; // unix seconds
}
export interface BackupsResponse {
  backups: BackupInfo[];
  dir: string;
}

// ── Logs (rolling logger / Logs settings tab) ─────────────────────────────
export interface LogEntry {
  time: number;   // unix milliseconds
  level: 'trace' | 'debug' | 'info' | 'warn' | 'error' | string;
  msg: string;
}
export interface LogsResponse {
  entries: LogEntry[];
  level: string;  // active log level
  file: string;   // log file path on disk
}

// ── Auth (single-user basic auth) ─────────────────────────────────────────
export interface AuthStatus {
  configured: boolean;     // an account exists → login protection is on
  authenticated: boolean;  // the current request has a valid session
  username?: string;
}

// ── Group definition types (from /api/tracker-groups) ─────────────────────

export interface GroupStyle {
  color?: string;
  icon?: string;     // Font Awesome class, e.g. "fas fa-chess-rook"
  sparkle?: boolean; // top-tier groups get a shimmering animation
}

export interface GroupPerk {
  icon: string;
  label: string; // e.g. "DL Slots: 15" or "Send Invite"
}

export interface GroupRequirements {
  min_uploaded?: string;
  /** Some trackers promote on download volume instead (TBDev family). */
  min_downloaded?: string;
  min_ratio?: number;
  min_seedtime?: string;
  min_seed_size?: string;
  min_uploads?: number;
  /** Adopted-torrent count (ANT adoption program). */
  min_adoptions?: number;
  min_bonus_points?: number;
  min_age?: string;
  description?: string;  // non-empty = text-only / special group
  /**
   * Alternative requirement sets: the base fields above must ALL be met,
   * PLUS at least ONE complete any_of entry. Entries never nest further.
   */
  any_of?: GroupRequirements[];
  /**
   * Minimum counts of per-tracker stat fields (e.g. HUNO's seed-time
   * brackets: vanguard_seeds ≥ 1). Ordered — rendered live from the def,
   * never copied into the stored targets map.
   */
  min_counts?: MinCountReq[];
}

/** One "stat field ≥ count" group requirement (GroupRequirements.min_counts). */
export interface MinCountReq {
  field: string;  // canonical stat field holding the current count
  count: number;  // minimum required value
  label?: string; // display override, e.g. "Vanguard (1–10d seed)"
}

export interface GroupDef {
  name: string;
  style: GroupStyle;
  requirements: GroupRequirements;
  perks?: GroupPerk[];
}

/** Keyed by tracker def key (e.g. "seedpool") → list of groups for that tracker */
export type TrackerGroupMap = Record<string, GroupDef[]>;

// ── Tracker definition registry (from /api/defs) ──────────────────────────

export interface DefInfo {
  key: string;
  name: string;
  abbr: string;
  url: string;
  type: string;
  has_groups: boolean;
  scrape_disabled: boolean;
  min_interval_minutes?: number;
  max_scrapes_per_day?: number;
  api_key_hint?: string;
  /** The def's custom API authenticates with the session cookie — keep the
   *  cookie field visible even when scraping is off. */
  needs_session_cookie?: boolean;
  approval_status?: string; // approved | informal | pending | unknown
  approval_note?: string;
  /** Type's required config fields minus any the def's API provides. */
  required_fields?: string[];
}

export interface TypeInfo {
  key: string;
  label: string;
  api_kind: string; // "unit3d" | "gazelle" | "custom" | "demo"
  /** Extra config fields this type needs (e.g. gazelle: ["username"]). */
  required_fields?: string[];
}

export interface DefIssue {
  file: string;
  error: string;
}

/** A tracker that has asked NOT to be supported by Yata (defs/optout.json). */
export interface OptOutEntry {
  name: string;
  /** Bare hostname matched against tracker URLs (also matches www./any scheme). */
  host: string;
  date?: string;
  note?: string;
}

export interface DefsPayload {
  trackers: DefInfo[];
  types: TypeInfo[];
  issues: DefIssue[];
  opt_outs?: OptOutEntry[];
}

export interface DefsReloadResult {
  ok: boolean;
  trackers: number;
  types: number;
  issues: DefIssue[];
}

// ── Prowlarr import (POST /api/prowlarr/indexers) ─────────────────────────

export interface ProwlarrIndexer {
  name: string;
  privacy: string;        // private | semiPrivate | public (Jackett: semi-private)
  base_url: string;
  has_api_key: boolean;
  api_key?: string;
  session_cookie?: string; // Jackett only — stored cookie for cookie-auth indexers
  def_key: string;        // matched Yata def ("" = manual)
  def_approval?: string;  // approval status of the matched def
  already_added: boolean; // URL matches an existing tracker
  enabled: boolean;       // enabled in Prowlarr (Jackett: configured)
}

// ── Pathways (GET /api/pathways/*) ────────────────────────────────────────

/** Dataset attribution — must be shown in the disclosure footer. */
export interface PathwaySource {
  name: string;
  url: string;
  license: string;
  fetched: string;
}

/** One selectable target tracker from GET /api/pathways/targets. */
export interface PathwayTarget {
  name: string;
  abbr?: string;
  def_key?: string;   // matched Yata def ("" / absent = none)
  is_mine: boolean;   // user already has this tracker
  inbound: number;    // active routes into it (0 = unreachable by invite)
}

export interface PathwayTargetsResponse {
  source: PathwaySource;
  targets: PathwayTarget[];
}

/** Evaluation of one requirement for display.
 *  When kind is set, have/need (+texts) carry quantitative progress for a
 *  bar (have -1 = unknown current value). */
export interface PathwayReqProgress {
  label: string;
  met: boolean;
  eta_days: number;   // 0 when met; -1 unknown
  note?: string;
  kind?: string;      // uploaded|seed_size|ratio|seedtime|uploads|bonus|age
  have?: number;
  need?: number;
  have_text?: string;
  need_text?: string;
  /** eta_days is a LOWER BOUND (known components only) — render with "+". */
  has_unknown?: boolean;
  /** Per-class breakdown for "reach class X (or Y)" requirements. */
  classes?: PathwayClassEval[];
}

/** Full requirement breakdown for one group/class a route requires. */
export interface PathwayClassEval {
  name: string;
  met: boolean;
  eta_days: number;             // floor when has_unknown; -1 truly unknown
  has_unknown?: boolean;
  fastest?: boolean;            // marked when multiple alternatives listed
  reqs: PathwayReqProgress[];   // base requirements (ALL must be met)
  any_of?: PathwayReqProgress[][]; // alternatives (ONE must be met)
}

/** One edge of a path with its evaluated requirements. */
export interface PathwayStep {
  from: string;
  to: string;
  days: number;        // route's min account age in days (-1 unknown)
  reqs_raw: string;    // community free text — ALWAYS shown verbatim
  updated?: string;    // e.g. "Dec 2025" — data freshness per route
  reqs: PathwayReqProgress[] | null;
  eta_days: number;     // max of KNOWN req ETAs (a floor when has_unknown)
  has_unknown: boolean; // some requirement not estimable
  estimated: boolean;   // true beyond hop 1 (no live stats)
  /** Extra context from the FROM tracker's def invite_requirements —
   *  shown distinctly from the community reqs_raw text. */
  def_note?: string;
}

/** A full chain from one of the user's trackers to the target. */
export interface PathwayPath {
  start_tracker_id: string; // user's tracker id (favicon/name lookup)
  start_name: string;       // pathway name of the starting tracker
  steps: PathwayStep[];
  total_eta_days: number;   // sum of step ETAs (a floor when has_unknown)
  has_unknown: boolean;     // render total with a "+" suffix
}

/** Offered when the user has no path to the target. */
export interface PathwaySuggestion {
  name: string;
  days: number;
  reqs: string;
  updated?: string;
}

export interface PathwayPathsResponse {
  target: string;
  source: PathwaySource;
  direct: boolean;          // at least one 1-hop path exists
  paths: PathwayPath[] | null;
  suggestions?: PathwaySuggestion[];
}

// ── Misc ──────────────────────────────────────────────────────────────────

export interface ThemeInfo {
  id: string;
  name: string;
  swatches?: string[]; // [bg, surface, accent, highlight] — declared in CSS via /* swatches: */
}

export interface QUIInstanceMeta {
  id: number;
  name: string;
  connected: boolean;
  host: string;
}

export interface ColDef {
  key: string;
  label: string;
  sortable: boolean;
  always: boolean;
  center: boolean;
  /** "core" columns are visible by default; "extended" come from profile scrapes / extra API fields. */
  group: 'core' | 'extended';
  defaultVisible: boolean;
  minWidth: number; // px — minimum column width; prevents squishing when many cols visible
}

export interface ColPref {
  key: string;
  visible: boolean;
}

export type SortDir = 'asc' | 'desc';
export type ViewMode = 'grid' | 'table' | 'pathways' | 'history';
