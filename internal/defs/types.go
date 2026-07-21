// Package defs loads tracker and tracker-type definitions from external JSON
// files. Definitions are pure data — the application contains no
// tracker-specific strings anywhere else (v1 lesson 10).
//
// Override chain (later layers win / are merged on top):
//
//	built-in defaults → tracker type def → tracker def → user config
//
// Rate limits combine differently: intervals take the MAX of the chain,
// daily caps take the MIN of non-zero values, disable flags OR together.
package defs

import "strings"

// TypeDef describes a class of tracker software (e.g. unit3d, gazelle).
// Loaded from defs/types/<key>.json.
type TypeDef struct {
	SchemaVersion int    `json:"schema_version"`
	Key           string `json:"key"`
	Label         string `json:"label"`
	// LastUpdated (YYYY-MM-DD) — bump on ANY content change; feeds the defs
	// version shown by the update check (max across tracker AND type defs).
	LastUpdated string `json:"last_updated,omitempty"`
	Description string `json:"description,omitempty"`

	// API describes how stats are fetched for this type.
	API TypeAPI `json:"api"`

	// APIFieldMap maps API JSON field names → canonical field names,
	// applied to every response before storage (e.g. "seedbonus" → "bonus_points").
	APIFieldMap map[string]string `json:"api_field_map,omitempty"`

	// Scrape holds type-level scrape behaviour and defaults.
	Scrape ScrapeSpec `json:"scrape"`
}

// TypeAPI selects the built-in fetcher used for a tracker type.
type TypeAPI struct {
	// Kind is one of: "unit3d", "gazelle", "gazelle_json", "gazelle_games", "custom", "demo", "none".
	//   unit3d  — GET {url}/api/user?api_token={key}
	//   gazelle — username/query-key API used by legacy Gazelle integrations
	//   gazelle_json — scoped ajax.php API with a raw Authorization header
	//   gazelle_games — scoped api.php API with an X-API-Key header
	//   custom  — fully described by the tracker def's "api" object
	//   demo    — local mock data, no HTTP
	//   none    — no API; scrape-only tracker type
	Kind string `json:"kind"`

	// RequiredFields lists tracker-config fields the user MUST fill at setup.
	// Valid values: "username" (gazelle needs it for the API call),
	// "session_cookie", "join_date" (API-only types like custom that report
	// no join date — needed for account-age tracking). Surfaced in the UI as
	// required inputs with explanatory hints.
	RequiredFields []string `json:"required_fields,omitempty"`
}

// ScrapeSpec holds scrape behaviour. Used at both type level and tracker
// level; tracker entries are merged over type entries.
type ScrapeSpec struct {
	// SkipHTMLScrape — architectural: this type/tracker cannot be scraped
	// (e.g. custom API-only types). Distinct from DisableScraping (policy).
	SkipHTMLScrape bool `json:"skip_html_scrape,omitempty"`

	// DisableScraping — policy: the tracker operator requests no scraping.
	// Cannot be overridden by users.
	DisableScraping bool `json:"disable_scraping,omitempty"`

	// ProfilePath is the profile URL path; "{username}" is substituted.
	// "" = inherit from type (tracker level) or no profile page (type level).
	ProfilePath string `json:"profile_path,omitempty"`

	// Labels maps lowercase on-page label text → canonical field name.
	// Type level: the base label map. Tracker level: merged on top ("extra labels").
	Labels map[string]string `json:"labels,omitempty"`

	// EventTitleClass extracts the event banner title from an element with
	// this CSS class instead of the default <strong> strategy.
	EventTitleClass string `json:"event_title_class,omitempty"`

	// StatCardClasses enables the value/label CSS-class pair strategy for
	// trackers with non-standard stat card layouts.
	StatCardClasses *StatCardClasses `json:"stat_card_classes,omitempty"`

	// PresenceFlags detect boolean page states by element presence, keyed by
	// canonical field name (e.g. "unread_mail"). The site header ships inside
	// the profile HTML we already fetch, so these cost zero extra requests.
	PresenceFlags map[string]PresenceFlag `json:"presence_flags,omitempty"`

	// Identify controls how Yata identifies itself in ALL requests to this
	// tracker (API fetches and scrapes) so staff can monitor the traffic:
	//   "ua" (default) — browser UA with a "Yata/<version>" suffix
	//   "header"       — plain browser UA + X-Yata-Version request header
	//   "none"         — plain browser UA (for UA-sensitive WAF/bot filters)
	// Lives in the scrape block for cascade purposes but governs API traffic
	// too. See internal/ident.
	Identify string `json:"identify,omitempty"`

	// MinIntervalMinutes is an operator-requested minimum gap between scrapes.
	// 0 = no opinion. Effective interval = max across the whole cascade.
	MinIntervalMinutes int `json:"min_interval_minutes,omitempty"`

	// MaxScrapesPerDay is an operator-requested daily cap (UTC day).
	// 0 = no opinion. Effective cap = min of non-zero values in the cascade.
	MaxScrapesPerDay int `json:"max_scrapes_per_day,omitempty"`
}

// StatCardClasses holds the CSS classes for label/value elements in a
// tracker's custom stat-card layout. The value element may appear before
// or after the label element.
type StatCardClasses struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// TrackerDef is the complete definition of one tracker site.
// Loaded from defs/trackers/<key>.json.
type TrackerDef struct {
	SchemaVersion int    `json:"schema_version"`
	Key           string `json:"key"`
	Name          string `json:"name"`
	Abbr          string `json:"abbr"`
	URL           string `json:"url"`
	// Aliases are alternate base URLs that should match this def.
	Aliases []string `json:"aliases,omitempty"`
	// Type references a TypeDef key.
	Type string `json:"type"`

	// LastUpdated is the date (YYYY-MM-DD) this def's data was last verified
	// against the tracker. Record-keeping only — never displayed in the app.
	LastUpdated string `json:"last_updated,omitempty"`
	// ApprovedBy records which tracker staff member approved Yata's
	// support for this tracker (name, their role, and the date). The derived
	// STATUS (see ApprovalStatus) is shown in the UI as a warning icon on
	// non-approved defs; the who/when details themselves are never displayed.
	ApprovedBy *DefApproval `json:"approved_by,omitempty"`

	APIFieldMap map[string]string `json:"api_field_map,omitempty"`
	Scrape      ScrapeSpec        `json:"scrape,omitempty"`

	// API configures the fetch for type kind "custom". Null otherwise.
	API *CustomAPI `json:"api,omitempty"`

	// ExtendedStats, when set on a unit3d tracker, adds a supplementary API
	// stats endpoint (e.g. the /api/user/stats that newer UNIT3D trackers add to
	// expose formerly scrape-only stats). Its fields are merged on top of the
	// core /api/user response — letting a tracker turn OFF scraping entirely
	// while Yata still shows seed size, seed times, unread flags, etc.
	ExtendedStats *ExtendedStatsSpec `json:"extended_stats,omitempty"`

	// Groups lists user ranks in ascending order (lowest first).
	Groups []GroupDef `json:"groups,omitempty"`

	// InviteRequirements captures site-wide rules for USING the invite system
	// when they are NOT tied to a user class (e.g. MAM: Power User class PLUS
	// 1 TB upload, 2.0 ratio, 6 months account age). They AUGMENT the
	// community pathway data — which for such trackers usually just says
	// "None" — on every route leaving this tracker. Nil = no extra rules.
	InviteRequirements *InviteReqs `json:"invite_requirements,omitempty"`

	// Rules holds site-wide account rules (distinct from per-group
	// requirements) used for display logic and warnings.
	Rules *TrackerRules `json:"rules,omitempty"`
}

// InviteReqs are a tracker's site-wide invite-system requirements (see
// TrackerDef.InviteRequirements). The embedded GroupRequirements fields
// (min_uploaded, min_ratio, min_age, …) carry the stat thresholds.
type InviteReqs struct {
	// MinClass is a user class that must ADDITIONALLY be held (must match a
	// Groups entry by name for live evaluation, e.g. "Power User").
	MinClass string `json:"min_class,omitempty"`
	GroupRequirements
	// Note is free text always shown alongside (e.g. "some invite threads
	// additionally require VIP"). Keep it short and cite-worthy.
	Note string `json:"note,omitempty"`
}

// PresenceFlag detects a boolean page state by element presence: find the
// <a> whose href (query/fragment stripped) ends with LinkSuffix; the flag is
// "true" when the anchor contains a Marker element, "false" when the anchor
// exists without one. Anchor absent → the field is NOT set at all — an
// unrecognised layout must never fake a "false" ("all read").
//
// Unit3D example: the header inbox link (…/conversations) contains a pulsing
// <svg> dot exactly when unread mail exists.
type PresenceFlag struct {
	LinkSuffix string `json:"link_suffix"`
	Marker     string `json:"marker"` // descendant element name, e.g. "svg"
}

// DefApproval records who from the tracker's staff approved support,
// their role, and when. The who/when details are record-keeping only (never
// shown in the app); the derived status drives the UI's approval warning.
type DefApproval struct {
	Name string `json:"name,omitempty"` // staff member's handle
	Role string `json:"role,omitempty"` // e.g. "SysOp", "Moderator"
	Date string `json:"date,omitempty"` // YYYY-MM-DD
	// Status overrides the derived state for the in-between cases:
	//   "informal" — staff gave a non-committal OK (record what was said in
	//                Note); shown with a softer warning than unknown.
	//   "pending"  — asked, awaiting a reply.
	// Empty = derive: name+date filled → approved, otherwise unknown.
	Status string `json:"status,omitempty"`
	// Note is free text for the informal case (what was said, by whom) —
	// shown in the UI tooltip alongside the informal warning.
	Note string `json:"note,omitempty"`
}

// Approval status values as derived by TrackerDef.ApprovalStatus.
const (
	ApprovalApproved = "approved" // staff signed off (name + date recorded)
	ApprovalInformal = "informal" // non-committal OK, not an official yes
	ApprovalPending  = "pending"  // asked, no answer yet
	ApprovalUnknown  = "unknown"  // never asked / unreachable / testing def
)

// ApprovalStatus derives the def's approval state. The default for absent or
// unfilled approved_by blocks is DELIBERATELY "unknown": any def someone
// hand-writes or receives for testing carries the use-at-your-own-risk
// warning with zero extra authoring effort. Official refusals don't get a
// status — they go in the opt-out list, which blocks rather than warns.
func (d *TrackerDef) ApprovalStatus() string {
	a := d.ApprovedBy
	if a == nil {
		return ApprovalUnknown
	}
	switch a.Status {
	case ApprovalInformal, ApprovalPending:
		return a.Status
	}
	if strings.TrimSpace(a.Name) != "" && strings.TrimSpace(a.Date) != "" {
		return ApprovalApproved
	}
	return ApprovalUnknown
}

// ApprovalNote returns the informal-approval note ("" otherwise).
func (d *TrackerDef) ApprovalNote() string {
	if a := d.ApprovedBy; a != nil {
		return a.Note
	}
	return ""
}

// TrackerRules are account-wide rules from the tracker's rules page.
type TrackerRules struct {
	// MinRatio is the ratio below which the account is at risk (warnings /
	// demotion / ban per the tracker's rules). The UI colors the ratio stat
	// red ONLY below this value when set (otherwise generic thresholds).
	MinRatio float64 `json:"min_ratio,omitempty"`
	// MinSeedDays is the tracker's minimum seed time per torrent, in days
	// (e.g. seedpool 10, InfinityHD 3). DISPLAY-ONLY reference — Yata does
	// no per-torrent tracking or calculations with it, and the fine print
	// (partial-download thresholds, exemptions, …) stays on the tracker.
	MinSeedDays int `json:"min_seed_days,omitempty"`
	// MinSeedHours represents exact minimums that are not a whole number of
	// days (e.g. GazelleGames requires 80 hours). It takes precedence over
	// MinSeedDays in the UI.
	MinSeedHours int `json:"min_seed_hours,omitempty"`
	// Category-specific minimums are used when episode and season torrents
	// have different requirements. They take precedence over MinSeedDays in
	// the UI when present.
	MinSeedDaysEpisode int `json:"min_seed_days_episode,omitempty"`
	MinSeedDaysSeason  int `json:"min_seed_days_season,omitempty"`
	// Note carries concise fine print that cannot be represented by the fixed
	// thresholds above, such as size-based seed-time formulas and H&R grace.
	Note string `json:"note,omitempty"`
}

// ExtendedStatsSpec declares a supplementary UNIT3D stats endpoint. Field names
// in the response are expected to already be canonical (UNIT3D/Yata names, e.g.
// seed_size, avg_seed_time, fl_tokens, real_ratio, unread_mail), so only the
// byte-count fields need conversion — everything else (seconds, counts, ratios,
// bools) passes through unchanged. Authenticated with the same api_token query
// param as /api/user; the endpoint's fields never overwrite core /api/user ones.
type ExtendedStatsSpec struct {
	// Path is appended to the tracker base URL, e.g. "/api/user/stats".
	Path string `json:"path"`
	// ByteFields lists response fields returned as raw byte counts that must be
	// formatted as human-readable sizes (e.g. seed_size, real_uploaded).
	ByteFields []string `json:"byte_fields,omitempty"`
}

// CustomAPI describes a non-standard tracker API entirely as data.
type CustomAPI struct {
	// Path is appended to the tracker base URL.
	Path string `json:"path"`
	// BaseURL overrides the tracker's canonical URL when its API uses a
	// dedicated host. Empty means use the tracker URL.
	BaseURL string `json:"base_url,omitempty"`
	// AuthMethod: "session_cookie" | "api_key_query" | "api_key_header" |
	// "api_key_json_rpc". JSON-RPC sends the key as the first positional param.
	AuthMethod string `json:"auth_method"`
	// JSONRPCMethod is required for auth_method "api_key_json_rpc".
	JSONRPCMethod string `json:"json_rpc_method,omitempty"`
	// CookieName for auth_method "session_cookie".
	CookieName string `json:"cookie_name,omitempty"`
	// APIKeyParam for auth_method "api_key_query".
	APIKeyParam string `json:"api_key_param,omitempty"`
	// SuccessField and SuccessValue optionally require a response-envelope
	// field to equal a specific value before mappings are processed.
	SuccessField string `json:"success_field,omitempty"`
	SuccessValue string `json:"success_value,omitempty"`

	// FieldMap maps JSON response paths (dot notation) → canonical field names.
	FieldMap map[string]string `json:"field_map,omitempty"`
	// SumFields maps canonical field → JSON paths summed as integers.
	SumFields map[string][]string `json:"sum_fields,omitempty"`
	// SumBytesFields maps canonical field → JSON paths (byte counts) summed
	// and formatted as a human-readable size.
	SumBytesFields map[string][]string `json:"sum_bytes_fields,omitempty"`
	// ByteFields maps JSON paths → canonical fields, converting raw bytes to sizes.
	ByteFields map[string]string `json:"byte_fields,omitempty"`
	// UnixFields maps Unix-second JSON paths → canonical YYYY-MM-DD fields.
	UnixFields map[string]string `json:"unix_fields,omitempty"`
	// BufferFromBytes computes buffer = uploaded_bytes − downloaded_bytes,
	// using the byte_fields entries mapped to "uploaded" and "downloaded".
	BufferFromBytes bool `json:"buffer_from_bytes,omitempty"`
	// RatioFromBytes computes ratio = uploaded_bytes / downloaded_bytes from
	// the same byte_fields entries, for APIs that return raw transfer counts
	// but no ratio (e.g. SpeedApp). A ratio mapped via field_map wins;
	// downloaded = 0 with uploads → "Infinity" (rendered ∞).
	RatioFromBytes bool `json:"ratio_from_bytes,omitempty"`

	// BoolFields map JSON paths → canonical fields, emitting "true"/"false"
	// from a truthy value (non-zero number, JSON true, or a non-empty/non-"0"
	// string). Turns e.g. an unread-message COUNT into the unread_mail flag.
	BoolFields map[string]string `json:"bool_fields,omitempty"`

	// ClassField is the JSON path to a numeric/string membership "class", and
	// ClassMap translates that value → a group NAME (matched to the def's
	// groups). For APIs that report the rank as an id rather than its name.
	ClassField string            `json:"class_field,omitempty"`
	ClassMap   map[string]string `json:"class_map,omitempty"`

	// APIKeyHint overrides the hint under the API key field in the UI.
	APIKeyHint string `json:"api_key_hint,omitempty"`
}

// GroupDef describes one user rank/class on a tracker.
type GroupDef struct {
	Name         string            `json:"name"`
	Style        GroupStyle        `json:"style"`
	Requirements GroupRequirements `json:"requirements"`
	Perks        []GroupPerk       `json:"perks,omitempty"`
}

// GroupStyle is the visual presentation of a group badge / username.
type GroupStyle struct {
	Color   string `json:"color,omitempty"`   // hex, "" = theme default
	Icon    string `json:"icon,omitempty"`    // Font Awesome class
	Sparkle bool   `json:"sparkle,omitempty"` // shimmer animation (top tiers)
}

// GroupRequirements are the thresholds to hold a rank. Zero/empty = none.
// When Description is set, the group is non-stat-based (invite-only etc.)
// and the UI shows the text instead of target bars.
type GroupRequirements struct {
	MinUploaded string `json:"min_uploaded,omitempty"`
	// MinDownloaded — some trackers promote on download volume instead
	// (e.g. TBDev-family sites where buying ratio proves participation).
	MinDownloaded string `json:"min_downloaded,omitempty"`
	// MinTotalTransfer is a combined upload + download threshold.
	MinTotalTransfer string  `json:"min_total_transfer,omitempty"`
	MinRatio         float64 `json:"min_ratio,omitempty"`
	MinSeedtime      string  `json:"min_seedtime,omitempty"`
	MinSeedSize      string  `json:"min_seed_size,omitempty"`
	MinUploads       int     `json:"min_uploads,omitempty"`
	// MinAdoptions — adopted-torrent count (e.g. ANT's adoption program,
	// where classes accept "N uploads and/or 2N adoptions").
	MinAdoptions   int    `json:"min_adoptions,omitempty"`
	MinBonusPoints int    `json:"min_bonus_points,omitempty"`
	MinAge         string `json:"min_age,omitempty"`
	// MinMonthlyUploads is uploads required per rolling month (e.g. RocketHD/
	// Aither uploader classes). No live stat exists for this yet — Yata will
	// eventually estimate it from upload history — so it is never evaluated
	// against live data (same treatment as MinCounts below); the field just
	// lets defs record the requirement today.
	MinMonthlyUploads int    `json:"min_monthly_uploads,omitempty"`
	Description       string `json:"description,omitempty"`
	// Note records non-numeric conditions that supplement trackable targets.
	Note string `json:"note,omitempty"`

	// AnyOf expresses alternative requirement sets: the fields above must
	// ALL be met, plus AT LEAST ONE complete AnyOf entry. Example (LST
	// Whale): uploads+ratio+age+seedtime above, any_of: [{min_seed_size:
	// "6 TiB"}, {min_uploaded: "25 TiB"}]. Entries must not nest further.
	AnyOf []GroupRequirements `json:"any_of,omitempty"`

	// MinCounts are minimum counts of arbitrary per-tracker stat fields —
	// e.g. HUNO promotes on "torrents seeding within a seed-time bracket"
	// (vanguard_seeds ≥ 1, champion_seeds ≥ 10, …) where each bracket count
	// arrives as its own API stat. An ordered slice (not a map) so the def
	// controls display order. Rendered live from the def like any_of — the
	// entries are never copied into a tracker's stored targets map.
	MinCounts []MinCountReq `json:"min_counts,omitempty"`
}

// MinCountReq is one "stat field ≥ count" group requirement (see
// GroupRequirements.MinCounts).
type MinCountReq struct {
	// Field is the canonical stat field holding the current count.
	Field string `json:"field"`
	// Count is the minimum required value.
	Count int `json:"count"`
	// Label overrides the generic field label in target rows, e.g.
	// "Vanguard (1–10d seed)" instead of "Vanguard Seeds".
	Label string `json:"label,omitempty"`
}

// GroupPerk is one benefit a group enjoys.
type GroupPerk struct {
	Icon  string `json:"icon"`
	Label string `json:"label"`
}
