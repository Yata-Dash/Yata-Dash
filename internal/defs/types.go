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

	// Notes is documentation for whoever edits the def next — never shown in
	// the UI. A real field rather than a stray key because readJSON falls back
	// to a tolerant decode when it meets an unknown one, which would quietly
	// disable unknown-field checking for the whole file and let a typo in a
	// field map pass unnoticed.
	Notes string `json:"notes,omitempty"`

	// API describes how stats are fetched for this type.
	API TypeAPI `json:"api"`

	// CustomAPI lets a TYPE carry the whole custom-API description, for a
	// family of trackers running the same software with the same endpoint —
	// Anthelion and Nebulance share their developers, their api.php grammar
	// and (increasingly) their field names. Each tracker def then only needs
	// what actually differs from its siblings, usually just the key hint.
	//
	// Tracker-level "api" blocks are merged OVER this one, so a def can still
	// override any single part without restating the rest. A type that sets
	// this needs api.kind "custom".
	CustomAPI *CustomAPI `json:"custom_api,omitempty"`

	// Capabilities declares what a tracker of this type can report. Set on the
	// TYPE it is the baseline for that software; tracker defs then state only
	// how they differ. See Capabilities.
	Capabilities *Capabilities `json:"capabilities,omitempty"`

	// APIFieldMap maps API JSON field names → canonical field names,
	// applied to every response before storage (e.g. "seedbonus" → "bonus_points").
	APIFieldMap map[string]string `json:"api_field_map,omitempty"`

	// Scrape holds type-level scrape behaviour and defaults.
	Scrape ScrapeSpec `json:"scrape"`
}

// TypeAPI selects the built-in fetcher used for a tracker type.
type TypeAPI struct {
	// Kind is one of: "unit3d", "gazelle", "gazelle_json", "gazelle_games",
	// "custom", "demo", "none".
	//   unit3d  — GET {url}/api/user?api_token={key}
	//   gazelle_json — ajax.php, the STANDARD Gazelle API endpoint (upstream,
	//                  present on essentially every fork). Raw Authorization
	//                  header. Reach for this first for a new Gazelle site.
	//   gazelle — a fork's own api.php (?action=user&method=getuserinfo),
	//             key + username. NOT upstream and not a shared "legacy"
	//             standard: forks that ship an api.php each invented their
	//             own grammar, so this fits only sites matching Anthelion's.
	//   gazelle_games — GazelleGames' own api.php (?request=…), X-API-Key
	//                   header, three chained calls. Shares nothing with the
	//                   above but the filename and the response envelope.
	//   custom  — fully described by the tracker def's "api" object
	//   demo    — local mock data, no HTTP
	//   none    — no API; scrape-only tracker type
	//
	// Every API kind authenticates with an API KEY. Session cookies are a
	// scraping credential only: a key is something a tracker's operators chose
	// to expose for external tools, whereas a session token is the user's own
	// login and its use for API access needs the operators' explicit blessing.
	// A Gazelle fork with no API-token feature therefore stays unsupported
	// until its staff open one (or approve the alternative directly).
	Kind string `json:"kind"`

	// APIKeyHint is the default "where do I find my key" hint for this type,
	// shown under the API key field. A tracker def's own api.api_key_hint
	// overrides it. (This field was set in defs/types/unit3d.json long before
	// it existed here, so the hint was silently dropped — the unknown-field
	// warning is what surfaced it.)
	APIKeyHint string `json:"api_key_hint,omitempty"`

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
	// Notes is documentation for whoever edits this def next — never shown in
	// the UI. Several defs already carried one before the field existed, so
	// the text was being silently discarded on load.
	Notes string `json:"notes,omitempty"`
	// Capabilities states how this tracker differs from its type's baseline —
	// usually a short api_stats_add or api_stats_omit rather than a full list.
	Capabilities *Capabilities `json:"capabilities,omitempty"`
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

	// Events, when set on a unit3d tracker, adds a site-events endpoint —
	// UNIT3D's /api/events/global-free-leech and its kin. It is what lets a
	// tracker report a running promotion through the API instead of leaving
	// the freeleech banner as the last reason to keep scraping it.
	//
	// Opt-in per tracker rather than declared on the type: not every UNIT3D
	// install exposes it, and a 404 on every refresh is a request spent
	// learning nothing.
	Events *EventsSpec `json:"events,omitempty"`

	// Retired marks a tracker that has SHUT DOWN. Yata never contacts it again
	// — no API call, no scrape — but everything already stored stays: the
	// history, the charts, the group timeline.
	//
	// Deliberately not the opt-out list, which answers a different question.
	// Opting out is a live operator's decision that Yata enforces on the user's
	// behalf and must never let them override; a shutdown is just a fact, and
	// if the site ever came back refusing to re-enable it would be Yata being
	// wrong and stubborn. Opt-out also has to work with NO def at all (it
	// matches a bare hostname, so an unknown URL can be refused at add time),
	// where a retirement only ever applies to a tracker we already defined.
	//
	// A retired def keeps ONLY what identifies the tracker: the ladder, field
	// maps, scrape config and rules all describe how to collect data that will
	// never arrive again. Names still render from the stored history; badges
	// fall back to the theme's default styling.
	Retired *RetiredSpec `json:"retired,omitempty"`

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

// LadderIndex returns a group's position in a def's ascending ladder (Groups
// is lowest-first — see TrackerDef.Groups), matched case-insensitively, or -1
// if the name isn't one of that def's ranks. Only positions of two names in
// the SAME def's ladder are comparable, and only meaningfully so for earned
// ranks: the upper end of many ladders holds staff/uploader classes that are
// appointed rather than climbed to.
func LadderIndex(groups []GroupDef, name string) int {
	name = strings.TrimSpace(name)
	for i, g := range groups {
		if strings.EqualFold(g.Name, name) {
			return i
		}
	}
	return -1
}

// InviteReqs are a tracker's site-wide invite-system requirements (see
// TrackerDef.InviteRequirements). The embedded GroupRequirements fields
// (min_uploaded, min_ratio, min_age, …) carry the stat thresholds.
type InviteReqs struct {
	// MinClass is a user class that must ADDITIONALLY be held (must match a
	// Groups entry by name for live evaluation, e.g. "Power User").
	MinClass string `json:"min_class,omitempty"`
	// MinClassAnyOf lists classes where holding ANY ONE of them opens the
	// invite forum — for trackers whose ladder forks into parallel tracks that
	// unlock it at different rungs (LST: "Sailboat OR Whale", the standard and
	// upload-heavy branches). Names must match Groups entries. Set alongside
	// MinClass only when BOTH rules apply; use one or the other normally.
	MinClassAnyOf []string `json:"min_class_any_of,omitempty"`
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
	// MaxLoginGapDays is how long an account may go without a login before
	// the tracker acts on it — disabling, parking or pruning. Unlike the seed
	// minimums above this is NOT display-only: it is the denominator for
	// login_days_remaining, so one alert rule ("warn me a week out") means the
	// same thing on a 30-day tracker and a 90-day one.
	//
	// Where a tracker disables first and deletes later, record the FIRST
	// consequence. That is the deadline the user can still act before; by the
	// second one the account is already gone. Stock UNIT3D does exactly this
	// at 90 days and 120, which is why so many defs read 90 — but forks
	// change it (several here run 60), so it is recorded per def and NOT
	// inherited from the type: a type-level default would turn "nobody has
	// checked this tracker" into a confident 90 for every UNIT3D install,
	// including any that has the behaviour switched off.
	//
	// Absent means Yata does not know of a policy — which is the common case
	// and must stay silent. It does NOT mean the tracker has none, so nothing
	// may present an absent value as "no deadline".
	//
	// Known limitation: several trackers (AnimeBytes, BTN, Redacted,
	// GazelleGames, Anthelion) grant inactivity immunity as a RANK PERK, so a
	// user high enough on the ladder is exempt from a policy this field states
	// site-wide. Yata has no way to express that yet, so those defs are left
	// unset rather than warning people who cannot be pruned.
	MaxLoginGapDays int `json:"max_login_gap_days,omitempty"`
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

// EventsSpec declares an endpoint reporting one site-wide event — a flag
// saying whether it is running and, usually, when it ends.
//
// Deliberately a single on/off event rather than UNIT3D's richer structured
// list: the endpoint that exists in the wild answers exactly that
// ({"is_global_free_leech": true, "global_free_leech_until": "…"}), and a
// tracker with the richer list already reports it through /api/user, where
// normalizeActiveEvents reads it.
//
// The event has no name in the response, so Label supplies one. It defaults to
// the wording these banners have carried for years, which is also what the
// profile scrape has been reading — so a tracker switching from scraping to
// this endpoint shows the same sentence it always did.
type EventsSpec struct {
	// Path is appended to the tracker base URL.
	Path string `json:"path"`
	// ActiveField is the boolean (or truthy) response field saying whether the
	// event is running right now.
	ActiveField string `json:"active_field"`
	// UntilField is the response field carrying the end time. Optional: an
	// event with no end still shows, just without a countdown.
	UntilField string `json:"until_field,omitempty"`
	// Label overrides the displayed event name.
	Label string `json:"label,omitempty"`
}

// RetiredSpec records that a tracker has shut down, and when.
type RetiredSpec struct {
	// Date the tracker shut down (YYYY-MM-DD). Shown to the user, so an
	// abandoned entry reads as a fact rather than as Yata having given up.
	Date string `json:"date,omitempty"`
	// Note is optional extra context ("merged into X", "domain expired").
	Note string `json:"note,omitempty"`
}

// DefaultEventLabel is the name shown for an EventsSpec event that declares no
// Label — matching the banner text these trackers render themselves.
const DefaultEventLabel = "Global freeleech mode activated"

// EventLabel is the name to display for this event.
func (e EventsSpec) EventLabel() string {
	if l := strings.TrimSpace(e.Label); l != "" {
		return l
	}
	return DefaultEventLabel
}

// Capabilities declares which stats a tracker can actually report, so the UI
// can tell "Yata is broken" apart from "this tracker's API doesn't expose that"
// and "the operator doesn't allow scraping" — three situations that otherwise
// look identical to anyone who hasn't read the defs.
//
// The vocabulary is just CANONICAL STAT FIELD NAMES, deliberately: unread_mail,
// unread_notifications and active_events are already stat fields, so "does this
// tracker report unread mail?" is "is unread_mail in its set?" with no second
// vocabulary to keep in step.
//
// Declared rather than inferred, because for most trackers it cannot be
// inferred: a plain UNIT3D def carries no field information at all, and what
// /api/user returns varies by fork. So the TYPE declares its software's
// baseline and each tracker states only its delta. Where a def CAN be trusted
// to describe itself — a custom API with a field_map — the set is derived
// instead and needs no declaration at all.
type Capabilities struct {
	// APIStats is the full set of canonical fields this tracker's API returns.
	// Normally set on the type (the stock response for that software); a
	// tracker setting it replaces the baseline outright.
	APIStats []string `json:"api_stats,omitempty"`
	// APIStatsAdd and APIStatsOmit are the usual tracker-level form: this fork
	// returns its software's stock set plus/minus a few. Deltas rather than a
	// restated list, so each def reads as "how I differ from my siblings" and a
	// change to the baseline reaches everyone who didn't opt out of it.
	APIStatsAdd  []string `json:"api_stats_add,omitempty"`
	APIStatsOmit []string `json:"api_stats_omit,omitempty"`

	// ScrapeStats overrides the set derived from scrape.labels and
	// presence_flags. Rarely needed — the label map already describes what a
	// profile page yields — but a tracker whose page carries a stat the
	// generic vocabulary misses can say so here.
	ScrapeStats []string `json:"scrape_stats,omitempty"`
}

// CustomAPI describes a non-standard tracker API entirely as data.
//
// A def of ANY type may carry a partial "api" block for per-tracker API
// metadata — APIKeyHint and RequiredFields are read for unit3d and gazelle defs
// too, not just custom ones.
type CustomAPI struct {
	// Path is appended to the tracker base URL.
	Path string `json:"path"`

	// RequiredFields adds to the TYPE's required fields for this tracker alone
	// (same vocabulary as TypeAPI.RequiredFields). For a tracker that differs
	// from its type's norm: most UNIT3D trackers report a join date, but one
	// whose API omits it and whose operator has asked not to be scraped can
	// only get it from the user, so it must be demanded at setup or account-age
	// tracking silently never works. Fields the def's own field_map provides
	// are still subtracted, so declaring one that is actually mapped is a
	// harmless no-op rather than a contradiction.
	RequiredFields []string `json:"required_fields,omitempty"`
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

	// EventList is the JSON path to a LIST of running site events, each an
	// object with a name (or a "type" slug) and optionally an end time —
	// site-wide freeleech, an upload contest, a themed week.
	//
	// A path rather than a field_map entry because field_map carries scalars
	// only: an array reaching it is silently dropped, which is exactly what
	// happened to the one API that reports events this way. The list is handed
	// to the same normaliser the UNIT3D path uses, so a custom def's events
	// render identically to every other tracker's.
	EventList string `json:"event_list,omitempty"`

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
