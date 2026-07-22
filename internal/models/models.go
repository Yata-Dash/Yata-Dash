// Package models defines the shared data structures used across Yata.
// Tracker-specific metadata does NOT live here — it lives in external JSON
// definition files loaded by internal/defs.
package models

// Tracker is a user-configured tracker account, stored in config.json.
type Tracker struct {
	ID            string `json:"id"`
	Name          string `json:"name"` // display name; defaults to def name
	URL           string `json:"url"`  // base URL, e.g. https://seedpool.org
	Type          string `json:"type"` // tracker type key, e.g. "unit3d"
	APIKey        string `json:"api_key"`
	SessionCookie string `json:"session_cookie"`
	Username      string `json:"username"`
	Enabled       bool   `json:"enabled"`

	// MinScrapeIntervalMinutes is the user's per-tracker scrape interval
	// override. 0 = unset. The effective interval is the maximum across the
	// whole cascade (global floor, global setting, type def, tracker def, this).
	MinScrapeIntervalMinutes int `json:"min_scrape_interval_minutes,omitempty"`
	// MaxScrapesPerDay is the user's per-tracker UTC-day cap. 0 = unset. The
	// effective cap is the most restrictive non-zero value across the cascade.
	MaxScrapesPerDay int `json:"max_scrapes_per_day,omitempty"`
	// AutoInterval derives the per-tracker interval from MaxScrapesPerDay
	// (1440/cap) when both are set, mirroring the global option.
	AutoInterval bool `json:"auto_interval,omitempty"`
	// APIOnly disables HTML profile scraping for THIS tracker only (the global
	// api_only_mode forces it for all). Cannot re-enable scraping a def forbids.
	APIOnly bool `json:"api_only,omitempty"`

	// Targets maps canonical stat field names to target values entered by the
	// user (or loaded from a group definition), e.g. {"uploaded": "10 TiB",
	// "ratio": "1.05"}. Values are human-readable strings parsed by the UI.
	Targets map[string]string `json:"targets,omitempty"`

	// TargetGroup is the group name whose requirements were loaded as targets.
	// "" = targets entered manually.
	TargetGroup string `json:"target_group,omitempty"`

	// TargetDeadlines maps a target field key to an optional "reach it by"
	// date (YYYY-MM-DD) — goal pacing (see internal/api/pacing.go). Account
	// age ("days") can never carry one: hitting an age by a date isn't
	// something the user controls. Sanitized on save (internal/api/trackers.go):
	// an entry survives only while its field still has a target value.
	TargetDeadlines map[string]string `json:"target_deadlines,omitempty"`

	// MockScenario selects the demo dataset for trackers of a "demo" kind type.
	MockScenario string `json:"mock_scenario,omitempty"`

	// JoinDate is a user-entered account creation date (YYYY-MM-DD). It is a
	// last-resort source for the join_date stat — used only when neither the
	// API nor a profile scrape reports one (e.g. MyAnonamouse, which exposes
	// no join date). Entered once at setup; a join date never changes.
	JoinDate string `json:"join_date,omitempty"`
}

// TrackerView is the safe public representation of a Tracker sent to the
// frontend. Credentials are masked/boolean-ised.
type TrackerView struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Abbr            string            `json:"abbr"`    // from def; "" for manual trackers
	DefKey          string            `json:"def_key"` // matched def key; "" for manual
	URL             string            `json:"url"`
	Type            string            `json:"type"`
	Enabled         bool              `json:"enabled"`
	HasKey          bool              `json:"has_key"`
	APIKeyMasked    string            `json:"api_key_masked"`
	HasSession      bool              `json:"has_session"`
	Username        string            `json:"username"`
	Targets         map[string]string `json:"targets"`
	TargetGroup     string            `json:"target_group"`
	TargetDeadlines map[string]string `json:"target_deadlines"`
	JoinDate        string            `json:"join_date"` // user-entered fallback (YYYY-MM-DD)

	MinScrapeIntervalMinutes int    `json:"min_scrape_interval_minutes"`
	MaxScrapesPerDay         int    `json:"max_scrapes_per_day"`
	AutoInterval             bool   `json:"auto_interval"`
	APIOnly                  bool   `json:"api_only"`
	MockScenario             string `json:"mock_scenario,omitempty"`

	// TrackerMinInterval / TrackerMaxPerDay are the def-level operator requests
	// (0 = none) so the form can show them and enforce the floor.
	TrackerMinInterval int `json:"tracker_min_interval"`
	TrackerMaxPerDay   int `json:"tracker_max_per_day"`

	// SupportsHTMLScrape is false when the type architecturally cannot scrape
	// (skip_html_scrape) OR the tracker operator forbids it (disable_scraping).
	SupportsHTMLScrape bool `json:"supports_html_scrape"`
	// ScrapeDisabledByTracker is true when the tracker def itself disables
	// scraping (operator request) — shown distinctly in the UI.
	ScrapeDisabledByTracker bool `json:"scrape_disabled_by_tracker"`
	// APIKeyHint is custom hint text for where to find the API key/token.
	APIKeyHint string `json:"api_key_hint,omitempty"`
	// ProfileURL is the user's profile page on the tracker ("" when unknown).
	ProfileURL string `json:"profile_url,omitempty"`
	// RequiredFields lists extra config fields this tracker's type needs
	// (e.g. gazelle requires "username"), minus any the tracker def's API
	// already provides. No omitempty: an empty list must reach the UI as []
	// so it doesn't fall back to the type-level default.
	RequiredFields []string `json:"required_fields"`
	// MinRatio is the tracker's account-wide required ratio (0 = unknown).
	// The UI colors the ratio red only below this when set.
	MinRatio float64 `json:"min_ratio,omitempty"`
	// MinSeedDays is the tracker's minimum per-torrent seed time in days
	// (0 = unknown). Display-only reference from the def — no calculations.
	MinSeedDays int `json:"min_seed_days,omitempty"`
	// DefApproval is the def's staff-approval status (approved | informal |
	// pending | unknown). Manual trackers (no def) report "unknown" — the UI
	// warns for anything but "approved". Who/when details are never exposed.
	DefApproval     string `json:"def_approval"`
	DefApprovalNote string `json:"def_approval_note,omitempty"` // informal-OK note

	// OptedOut is true when this already-configured tracker's host is now on
	// defs/optout.json — the operator has asked not to be supported. Yata
	// stops all API + scrape traffic to it; the UI flags the row so the user
	// knows why it went quiet. OptOutNote carries the public note, if any.
	OptedOut   bool   `json:"opted_out,omitempty"`
	OptOutNote string `json:"opted_out_note,omitempty"`
}

// Settings holds application-level configuration.
type Settings struct {
	Theme           string `json:"theme"`             // theme id; "" = default
	TrackerNameMode string `json:"tracker_name_mode"` // "name" | "both" | "abbr"
	GroupNameStyle  string `json:"group_name_style"`  // "plain" | "styled"
	UsernameStyle   string `json:"username_style"`    // "plain" | "group"
	PrivateMode     bool   `json:"private_mode"`      // blur usernames
	ShowFavicons    bool   `json:"show_favicons"`
	ShowStatSources bool   `json:"show_stat_sources"` // per-stat api/scrape origin dot
	ProfileAutoSync bool   `json:"profile_auto_sync"` // auto-scrape on refresh when allowed

	// ShowPathwayEtas toggles "estimated time to reach" chips in the
	// Pathways view (path/class headers + exact account-age countdowns).
	// nil = true. Progress bars always show.
	ShowPathwayEtas *bool `json:"show_pathway_etas"`
	// ShowTrendEstimates toggles the per-stat trend projections (upload/
	// seed size/bonus "≈ N at your recent rate" chips), independently of
	// ShowPathwayEtas. nil = true.
	ShowTrendEstimates *bool `json:"show_trend_estimates"`
	// ShowTargetEtas toggles the dashboard TARGETS time estimates (per-target
	// "≈ N" / account-age "in N" chips + the "Next group ≈ N" promotion
	// headline), independently of the Pathways toggles. nil = true.
	ShowTargetEtas *bool `json:"show_target_etas"`
	// ShowRateHovers toggles the per-day trend tooltips shown on hover over
	// stat values (uploaded/downloaded/buffer/bonus/uploads — e.g.
	// "≈ 245.3 GiB per day"), like the ratio hover's tracker minimum. nil = true.
	ShowRateHovers *bool `json:"show_rate_hovers"`
	// ShowUnreadMail / ShowUnreadNotifications toggle the unread envelope/bell
	// icons on dashboard cards and in the detail table's expanded info
	// (scraped header presence flags — Unit3D inbox/bell dots). Separate
	// toggles: many users care about mail but not notifications. nil = true.
	ShowUnreadMail          *bool `json:"show_unread_mail"`
	ShowUnreadNotifications *bool `json:"show_unread_notifications"`
	// ShowTrackerRules toggles the compact rules line at the bottom of grid
	// cards (min ratio / min seed time from the def — display-only). nil =
	// true. The Detail view's Rules section always shows.
	ShowTrackerRules *bool `json:"show_tracker_rules"`
	// HighlightHnR toggles red colouring for a nonzero hit-and-run count
	// (cards, table, expanded stat rows). nil = true. Some trackers' H&Rs
	// never clear (permanent record) — off shows a neutral colour instead so
	// it doesn't read as an ongoing alarm.
	HighlightHnR *bool `json:"highlight_hnr"`
	// HideLoginWarning suppresses the persistent "Login protection is off"
	// dashboard banner for users who deliberately run without login (trusted
	// LAN, etc.). Default false — the warning shows, since login is the safer
	// posture; this is the explicit opt-out for those who've decided otherwise.
	HideLoginWarning bool `json:"hide_login_warning"`
	// ShowGoalPacing toggles the full pacing line ("needs X/day · doing
	// Y/day · verdict") under each dated target row in the Tracker Detail
	// Targets section. nil = true. Independent of ShowGoalChips.
	ShowGoalPacing *bool `json:"show_goal_pacing"`
	// ShowGoalChips toggles the compact on-track/behind/overdue chip after a
	// dated target row's ETA chip on grid cards and the table's expanded
	// targets. nil = true. Independent of ShowGoalPacing.
	ShowGoalChips *bool `json:"show_goal_chips"`
	// PathwayFavorites / PathwayNotInterested are pathway-target lists (by
	// dataset tracker name). Favourites sort first in the Pathways picker;
	// not-interested entries sort last and are excluded from the
	// requirements-met filter. Stored server-side so they survive browsers
	// and ride along in config export/import.
	PathwayFavorites     []string `json:"pathway_favorites,omitempty"`
	PathwayNotInterested []string `json:"pathway_not_interested,omitempty"`
	// PathwaysIncludeDisabled lets DISABLED trackers act as pathway starting
	// points — imported/def-less trackers a user keeps purely as a "I'm a
	// member here" record. Their stats are ALWAYS treated as unknown (frozen
	// numbers must never claim a requirement is met), so those paths carry no
	// time estimate. Opt-in: default false.
	PathwaysIncludeDisabled bool `json:"pathways_include_disabled"`

	// TrustProxyHeaders makes Yata honor X-Forwarded-For (login rate
	// limiting — otherwise every proxied client shares the proxy's lockout
	// bucket) and X-Forwarded-Proto (Secure session cookie behind TLS-
	// terminating proxies). Enable ONLY behind a reverse proxy you control:
	// directly exposed, these headers are client-spoofable.
	TrustProxyHeaders bool `json:"trust_proxy_headers"`
	// UpdateCheckAuto opts in to a DAILY check of versions.json on the repo
	// (contacts raw.githubusercontent.com). Default OFF — privacy stance: the
	// app contacts nothing the user didn't ask for. Manual checks always work.
	UpdateCheckAuto bool `json:"update_check_auto"`
	// DurationFormat controls duration rendering: "ym" (1Y 9M, default) or
	// "days" (694 days).
	DurationFormat string `json:"duration_format"`

	// ── Automatic config backups (opt-in) ──────────────────────────────────
	BackupEnabled   bool   `json:"backup_enabled"`   // off by default
	BackupFrequency string `json:"backup_frequency"` // daily|weekly|monthly
	BackupKeep      int    `json:"backup_keep"`      // retain last N (default 5, max 99)

	// ── Scrape rate limiting (global layer of the cascade) ──────────────────
	APIOnlyMode           bool `json:"api_only_mode"`           // disable ALL scraping
	ScrapeIntervalMinutes int  `json:"scrape_interval_minutes"` // floor 60
	MaxScrapesPerDay      int  `json:"max_scrapes_per_day"`     // 0 = unlimited
	AutoInterval          bool `json:"auto_interval"`           // derive interval from daily max

	// ── Automatic refresh cadence (API polling, distinct from scraping) ─────
	// RefreshIntervalMinutes is how often stats are auto-refreshed from tracker
	// APIs while idle (the background loop + any open dashboards). Floor 15;
	// 0 = unset → treated as the 30-min default. Manual refresh (the button /
	// Tracker Test) always bypasses this — it's purely to cut idle load.
	RefreshIntervalMinutes int `json:"refresh_interval_minutes"`
	// QUIRefreshSeconds is how often qui (local qBittorrent) stat bars refresh
	// in an open dashboard. This data is local + time-sensitive, so it stays
	// fast. Floor 1; 0 = unset → 10-sec default. The qui toggle turns it off.
	QUIRefreshSeconds int `json:"qui_refresh_seconds"`

	// ── History retention ────────────────────────────────────────────────────
	// HistoryDailyRetentionDays is how long daily history rollups are kept —
	// the data behind long-range growth charts and trend rates. 0/unset →
	// 730-day default (~150 KB per tracker per year, so "years" is cheap).
	// Fine-grained history (sparklines) stays at 14 days regardless.
	HistoryDailyRetentionDays int `json:"history_daily_retention_days"`

	// ── QUI (qBittorrent UI) integration ────────────────────────────────────
	QUIURL              string `json:"qui_url"`
	QUIAPIKey           string `json:"qui_api_key"`
	QUIEnabledInstances []int  `json:"qui_enabled_instances"`
	QUIBarsVisible      *bool  `json:"qui_bars_visible"` // nil = true
	// QUISeedsizeMode controls whether qui's per-tracker seeding totals feed
	// the seed_size stat. qui's number is a client-side calculation over the
	// torrents it can see — the tracker's own figure is the truth for
	// progression — so the strongest mode still loses to a tracker API:
	//   "off"     (default) — never used
	//   "missing" — fills in only when neither the API nor a scrape has it
	//   "prefer"  — beats scrapes, still loses to the tracker's API
	QUISeedsizeMode string `json:"qui_seedsize_mode"`

	// ── Indexer-manager imports (saved on first successful fetch so the
	//    import sections come prefilled; secrets are masked like QUIAPIKey) ──
	ProwlarrURL          string `json:"prowlarr_url"`
	ProwlarrAPIKey       string `json:"prowlarr_api_key"`
	JackettURL           string `json:"jackett_url"`
	JackettAdminPassword string `json:"jackett_admin_password"`
}

// DefaultSettings returns the defaults for a fresh install.
func DefaultSettings() Settings {
	return Settings{
		Theme:           "",
		TrackerNameMode: "name",
		GroupNameStyle:  "styled",
		UsernameStyle:   "plain",
		ProfileAutoSync: true,
		// New-install default: a conservative 120 min. The HARD FLOOR is still
		// 60 (see scrape.HardFloorMinutes + the < 60 clamps) — users may lower
		// it to 60 but not below; unchanged, it stays at 120.
		ScrapeIntervalMinutes: 120,
		MaxScrapesPerDay:      0,
		// Idle API polling: 30 min by default (floor 15). The manual refresh
		// button and Tracker Test are unaffected — trackers' own API rate
		// limits still apply, this just lowers unattended background load.
		RefreshIntervalMinutes: 30,
		// qui is a local API with time-sensitive data (speed/free space) — keep
		// it snappy at 10 s (floor 1; the integration toggle turns it off).
		QUIRefreshSeconds: 10,
		// ~2 years of daily history — enough for the History view's long
		// ranges while keeping the database tiny.
		HistoryDailyRetentionDays: 730,
		QUIURL:                    "http://localhost:7476",
		QUIEnabledInstances:       []int{},
	}
}

// ServerConfig controls the listen address.
type ServerConfig struct {
	Host string `json:"host"` // default "0.0.0.0"
	Port int    `json:"port"` // default 8420
}

// Config is the top-level config.json structure.
type Config struct {
	Server        ServerConfig       `json:"server"`
	Trackers      []Tracker          `json:"trackers"`
	Settings      Settings           `json:"settings"`
	Notifications NotificationConfig `json:"notifications"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Alerts & notifications (webhooks)
// ─────────────────────────────────────────────────────────────────────────────

// NotificationConfig holds the webhook destinations and the alert rules that
// target them. Stored in config.json (so it's covered by export/backup).
type NotificationConfig struct {
	Destinations []NotifyDestination `json:"destinations"`
	Rules        []AlertRule         `json:"rules"`
	// SeededDefaultRules marks that the one-time default-rules seeding (see
	// config.seedDefaultAlertRules) has already run for this install, so a
	// user who deletes the starter rules never gets them re-injected.
	SeededDefaultRules bool `json:"seeded_default_rules,omitempty"`
	// Digest schedules the weekly summary notification (internal/api/digest.go).
	Digest DigestConfig `json:"digest"`
}

// DigestConfig schedules the weekly summary notification: per-tracker
// deltas, target/goal progress, this week's promotions/demotions, and newly
// requirements-met pathway targets, pushed through the existing webhook
// destinations.
type DigestConfig struct {
	Enabled      bool     `json:"enabled"`
	Weekday      int      `json:"weekday"`      // 0=Sunday … 6=Saturday; default 1 (Monday)
	Hour         int      `json:"hour"`         // 0-23 server-local; default 9
	Destinations []string `json:"destinations"` // destination IDs; empty = all enabled

	// Server-maintained state — the client never sends these; PUT
	// /api/notifications must carry the stored values forward (same rule as
	// SeededDefaultRules).
	LastSentAt       int64    `json:"last_sent_at,omitempty"`       // unix seconds
	LastReadyTargets []string `json:"last_ready_targets,omitempty"` // pathway targets ready at last digest
}

// NotifyDestination is one webhook target. Type selects the payload format.
type NotifyDestination struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`    // discord | telegram | gotify | generic
	URL     string `json:"url"`     // webhook URL (discord/generic) or base URL (gotify)
	Token   string `json:"token"`   // telegram bot token / gotify app token
	ChatID  string `json:"chat_id"` // telegram chat id
	Enabled bool   `json:"enabled"`
}

// AlertRule fires a notification when its conditions become true for a tracker.
type AlertRule struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Enabled      bool        `json:"enabled"`
	TrackerIDs   []string    `json:"tracker_ids"`          // trackers this rule includes/excludes
	TrackerMode  string      `json:"tracker_mode"`         // "include" (default) | "exclude"
	TrackerID    string      `json:"tracker_id,omitempty"` // legacy single-tracker field (migrated by Scope)
	Match        string      `json:"match"`                // "all" (AND) | "any" (OR)
	Conditions   []Condition `json:"conditions"`
	Destinations []string    `json:"destinations"` // destination IDs; empty = all enabled
	CooldownMins int         `json:"cooldown_minutes"`
}

// Matches reports whether the rule applies to the given tracker. Include mode
// with no trackers selected = all trackers; exclude mode = all but the listed.
// The legacy single TrackerID is honoured when TrackerIDs is empty.
func (r AlertRule) Matches(trackerID string) bool {
	ids := r.TrackerIDs
	if len(ids) == 0 && r.TrackerID != "" {
		ids = []string{r.TrackerID}
	}
	in := false
	for _, id := range ids {
		if id == trackerID {
			in = true
			break
		}
	}
	if r.TrackerMode == "exclude" {
		return !in
	}
	return len(ids) == 0 || in
}

// Condition is one field/operator/value test within a rule.
type Condition struct {
	Field string `json:"field"` // ratio|buffer|warnings|hit_and_runs|freeleech_active|reachable|group|…
	Op    string `json:"op"`    // lt|lte|eq|ne|gt|gte|changed|is_true|is_false
	Value string `json:"value"` // numeric / size string; ignored for bool & changed ops
}

// ─────────────────────────────────────────────────────────────────────────────
// Stats
// ─────────────────────────────────────────────────────────────────────────────

// Source identifies where a stat value came from.
type Source string

const (
	SourceAPI    Source = "api"
	SourceScrape Source = "scrape"
	// SourceManual is user-entered data (e.g. a join date the tracker's API
	// doesn't provide). Lowest merge priority — only fills gaps API and
	// scrape both leave empty.
	SourceManual Source = "manual"
	// SourceQUI is client-side data computed by a linked qui instance
	// (currently seed_size only, from its per-tracker torrent totals). It's a
	// calculation over the torrents qui can see — not the tracker's own
	// number — so its merge position is a setting (see
	// Settings.QUISeedsizeMode) and it NEVER beats the tracker's API.
	SourceQUI Source = "qui"
)

// StatField is one merged stat value with provenance.
type StatField struct {
	Value     any    `json:"value"`
	Source    Source `json:"source"`
	UpdatedAt int64  `json:"updated_at"` // unix seconds
}

// MergedStats is the unified per-tracker stats view returned by /api/stats.
// Keys are canonical field names (see internal/stats/fields.go).
type MergedStats map[string]StatField

// TrackerStatsResponse is the per-tracker entry in the /api/stats response.
type TrackerStatsResponse struct {
	TrackerID string      `json:"tracker_id"`
	OK        bool        `json:"ok"`
	Error     string      `json:"error,omitempty"`
	ErrorKind string      `json:"error_kind,omitempty"` // auth_error | connection_error | parse_error | disabled
	Fields    MergedStats `json:"fields"`
	FetchedAt int64       `json:"fetched_at"`
	// Rates is per-day growth for projectable fields (uploaded/downloaded/
	// seed_size in GiB; bonus_points raw), from the stable daily-rollup
	// average. The frontend uses it for target/promotion ETAs. A field with
	// no measurable growth is omitted.
	Rates map[string]float64 `json:"rates,omitempty"`
}
