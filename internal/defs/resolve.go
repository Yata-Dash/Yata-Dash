package defs

import (
	"slices"
	"sort"
)

// ResolvedScrape is the fully-merged scrape behaviour for one tracker,
// combining type-level and tracker-level ScrapeSpecs. The user/global layer
// of the rate-limit cascade is applied later by internal/scrape (policy),
// because it needs live settings.
type ResolvedScrape struct {
	SkipHTMLScrape  bool
	DisableScraping bool
	// OptedOut is true when the tracker's host is on defs/optout.json — the
	// operator has asked NOT to be supported at all. Unlike DisableScraping
	// (which only blocks profile scraping), an opt-out blocks BOTH the API
	// fetch and scraping. OptOut carries the matched entry (name/date/note)
	// for the UI. This can go true AFTER a tracker was added, so it must be
	// enforced at runtime — not just at add-time.
	OptedOut        bool
	OptOut          OptOutEntry
	// Retired is true when the tracker's def records that the site has SHUT
	// DOWN. It also forces DisableScraping, so a caller that doesn't know
	// about retirement still refuses to fetch the page; this flag exists so
	// the ones that do can say why, instead of blaming the operator.
	Retired bool
	ProfilePath     string
	Labels          map[string]string
	EventTitleClass string
	StatCardClasses *StatCardClasses
	PresenceFlags   map[string]PresenceFlag
	// Identify is how Yata identifies itself to this tracker ("ua" default,
	// "header", or "none") — applies to API and scrape traffic alike.
	Identify string

	// MinIntervalMinutes is the def-level requested minimum (max of type and
	// tracker values; 0 = no opinion).
	MinIntervalMinutes int
	// MaxScrapesPerDay is the def-level daily cap (min of non-zero type and
	// tracker values; 0 = no cap requested).
	MaxScrapesPerDay int
}

// ResolveScrape merges the scrape chain for a tracker identified by config
// values (URL + type key). Works for manual trackers with no def: the type
// layer still applies, the tracker layer is empty.
func (r *Registry) ResolveScrape(trackerURL, typeKey string) ResolvedScrape {
	out := ResolvedScrape{Labels: map[string]string{}}

	td, hasDef := r.TrackerByURL(trackerURL)
	if hasDef && td.Type != "" {
		typeKey = td.Type
	}

	// Layer 1 — tracker type
	if tt, ok := r.Type(typeKey); ok {
		applySpec(&out, tt.Scrape)
	}
	// Layer 2 — tracker def
	if hasDef {
		applySpec(&out, td.Scrape)
	}
	// Opt-out is host-based (not part of the type/tracker scrape chain) and
	// trumps everything: it blocks API + scrape alike. Resolved here so every
	// caller (scrape policy, UI status, refresh loop) sees it consistently.
	if entry, opted := r.OptOut(trackerURL); opted {
		out.OptedOut = true
		out.OptOut = entry
	}
	// A retired tracker is not scraped either — same reason, different fact.
	// DisableScraping rather than a flag of its own: every caller already
	// treats it as "this page is not to be fetched", and the UI reads the
	// retirement itself for the wording.
	if hasDef && td.Retired != nil {
		out.Retired = true
		out.DisableScraping = true
	}
	return out
}

// applySpec merges one ScrapeSpec layer into the resolution.
// Booleans OR (a layer can disable, never re-enable); strings override when
// set; labels merge with later layers winning; intervals take MAX; daily
// caps take MIN of non-zero values.
func applySpec(out *ResolvedScrape, s ScrapeSpec) {
	if s.SkipHTMLScrape {
		out.SkipHTMLScrape = true
	}
	if s.DisableScraping {
		out.DisableScraping = true
	}
	if s.ProfilePath != "" {
		out.ProfilePath = s.ProfilePath
	}
	for k, v := range s.Labels {
		out.Labels[k] = v
	}
	if s.EventTitleClass != "" {
		out.EventTitleClass = s.EventTitleClass
	}
	if s.StatCardClasses != nil {
		out.StatCardClasses = s.StatCardClasses
	}
	if s.Identify != "" {
		out.Identify = s.Identify
	}
	for k, v := range s.PresenceFlags {
		if out.PresenceFlags == nil {
			out.PresenceFlags = map[string]PresenceFlag{}
		}
		out.PresenceFlags[k] = v // later layers (tracker def) win per field
	}
	if s.MinIntervalMinutes > out.MinIntervalMinutes {
		out.MinIntervalMinutes = s.MinIntervalMinutes
	}
	if s.MaxScrapesPerDay > 0 && (out.MaxScrapesPerDay == 0 || s.MaxScrapesPerDay < out.MaxScrapesPerDay) {
		out.MaxScrapesPerDay = s.MaxScrapesPerDay
	}
}

// ResolveAPIFieldMap merges type-level and tracker-level API field maps;
// tracker entries win on collision.
func (r *Registry) ResolveAPIFieldMap(trackerURL, typeKey string) map[string]string {
	td, hasDef := r.TrackerByURL(trackerURL)
	if hasDef && td.Type != "" {
		typeKey = td.Type
	}
	merged := map[string]string{}
	if tt, ok := r.Type(typeKey); ok {
		for k, v := range tt.APIFieldMap {
			merged[k] = v
		}
	}
	if hasDef {
		for k, v := range td.APIFieldMap {
			merged[k] = v
		}
	}
	return merged
}

// TypeKeyFor resolves which type a tracker actually is: its def's type when it
// has a def, otherwise the type stored on the tracker itself. Callers that need
// to key behaviour on a specific tracker FAMILY should use this rather than
// APIKind — several families now share the "custom" fetcher, so the kind no
// longer identifies them.
func (r *Registry) TypeKeyFor(trackerURL, typeKey string) string {
	if td, ok := r.TrackerByURL(trackerURL); ok && td.Type != "" {
		return td.Type
	}
	return typeKey
}

// ResolveCustomAPI returns the effective custom-API description for a tracker:
// the TYPE's shared block with the tracker def's own block merged over it.
// Returns nil when neither supplies one.
//
// The two levels exist because tracker families share an API. Anthelion and
// Nebulance run the same software from the same developers, so the endpoint,
// the auth style and the field names belong to the family, while the API-key
// hint (a path through each site's own settings UI) belongs to the tracker.
func (r *Registry) ResolveCustomAPI(trackerURL, typeKey string) *CustomAPI {
	td, hasDef := r.TrackerByURL(trackerURL)
	if hasDef && td.Type != "" {
		typeKey = td.Type
	}
	var base *CustomAPI
	if tt, ok := r.Type(typeKey); ok && tt.CustomAPI != nil {
		clone := *tt.CustomAPI
		base = &clone
	}
	if !hasDef || td.API == nil {
		return base
	}
	if base == nil {
		return td.API
	}
	merged := *base
	mergeCustomAPI(&merged, td.API)
	return &merged
}

// mergeCustomAPI overlays the non-empty parts of over onto dst. Scalars are
// replaced when set; maps are merged key by key so a def can add one mapping
// without restating its family's whole field map.
func mergeCustomAPI(dst *CustomAPI, over *CustomAPI) {
	setStr := func(d *string, o string) {
		if o != "" {
			*d = o
		}
	}
	setStr(&dst.Path, over.Path)
	setStr(&dst.BaseURL, over.BaseURL)
	setStr(&dst.AuthMethod, over.AuthMethod)
	setStr(&dst.JSONRPCMethod, over.JSONRPCMethod)
	setStr(&dst.CookieName, over.CookieName)
	setStr(&dst.APIKeyParam, over.APIKeyParam)
	setStr(&dst.SuccessField, over.SuccessField)
	setStr(&dst.SuccessValue, over.SuccessValue)
	setStr(&dst.ClassField, over.ClassField)
	setStr(&dst.APIKeyHint, over.APIKeyHint)
	// Booleans only ever turn ON here: a family that computes ratio from bytes
	// does so for every member, and there is no "false" to distinguish from
	// "unset" in JSON. A member that genuinely differs needs its own type.
	dst.BufferFromBytes = dst.BufferFromBytes || over.BufferFromBytes
	dst.RatioFromBytes = dst.RatioFromBytes || over.RatioFromBytes

	dst.FieldMap = mergeStrMap(dst.FieldMap, over.FieldMap)
	dst.ByteFields = mergeStrMap(dst.ByteFields, over.ByteFields)
	dst.UnixFields = mergeStrMap(dst.UnixFields, over.UnixFields)
	dst.BoolFields = mergeStrMap(dst.BoolFields, over.BoolFields)
	dst.ClassMap = mergeStrMap(dst.ClassMap, over.ClassMap)
	dst.SumFields = mergeSliceMap(dst.SumFields, over.SumFields)
	dst.SumBytesFields = mergeSliceMap(dst.SumBytesFields, over.SumBytesFields)
	if len(over.RequiredFields) > 0 {
		dst.RequiredFields = append(append([]string{}, dst.RequiredFields...), over.RequiredFields...)
	}
}

func mergeStrMap(base, over map[string]string) map[string]string {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

func mergeSliceMap(base, over map[string][]string) map[string][]string {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	out := make(map[string][]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// ── Capabilities ─────────────────────────────────────────────────────────────

// ResolvedCapabilities is what a tracker can actually report, split by how it
// gets there. Callers compare these sets against a group ladder's requirements
// to answer "how much of my promotion progress can Yata even see here?".
type ResolvedCapabilities struct {
	// APIStats are canonical fields the tracker's API returns.
	APIStats []string
	// ScrapeStats are canonical fields its profile page yields — empty when
	// scraping is architecturally impossible or forbidden by the operator, so
	// callers never have to re-check the policy themselves.
	ScrapeStats []string
	// ScrapePossible records why ScrapeStats may be empty: false means the
	// operator forbids it or the type can't scrape, rather than the page
	// simply offering nothing.
	ScrapePossible bool
	// Derived is true when APIStats came from a def that describes its own API
	// (a custom field_map) rather than from a declaration. Consumers use it to
	// decide whether a missing declaration is worth warning about.
	Derived bool
}

// Has reports whether a canonical field is obtainable at all, by either route.
func (rc ResolvedCapabilities) Has(field string) bool {
	return slices.Contains(rc.APIStats, field) || slices.Contains(rc.ScrapeStats, field)
}

// HasAPI reports whether a canonical field comes from the API specifically.
func (rc ResolvedCapabilities) HasAPI(field string) bool {
	return slices.Contains(rc.APIStats, field)
}

// ResolveCapabilities works out what a tracker can report.
//
// The API set comes from whichever source can be trusted: a def describing its
// own API through a field_map is derived from directly, otherwise the type's
// declared baseline with the tracker's add/omit deltas applied. The scrape set
// is derived from the resolved label map and presence flags, and is empty
// whenever scraping is impossible or forbidden.
func (r *Registry) ResolveCapabilities(trackerURL, typeKey string) ResolvedCapabilities {
	td, hasDef := r.TrackerByURL(trackerURL)
	if hasDef && td.Type != "" {
		typeKey = td.Type
	}
	out := ResolvedCapabilities{}

	// A custom API states its own output, so believe it over any declaration.
	if api := r.ResolveCustomAPI(trackerURL, typeKey); api != nil && api.Path != "" {
		out.APIStats = canonicalFieldsOf(api)
		out.Derived = true
	} else {
		set := map[string]bool{}
		if tt, ok := r.Type(typeKey); ok && tt.Capabilities != nil {
			for _, f := range tt.Capabilities.APIStats {
				set[f] = true
			}
		}
		if hasDef && td.Capabilities != nil {
			// A tracker restating the whole list replaces the baseline; the
			// usual case is a delta on top of it.
			if len(td.Capabilities.APIStats) > 0 {
				set = map[string]bool{}
				for _, f := range td.Capabilities.APIStats {
					set[f] = true
				}
			}
			for _, f := range td.Capabilities.APIStatsAdd {
				set[f] = true
			}
			for _, f := range td.Capabilities.APIStatsOmit {
				delete(set, f)
			}
		}
		// A field the user is REQUIRED to enter is, by definition, one this
		// tracker's API doesn't supply — that is what api.required_fields
		// means. Subtracting it here keeps the two from being maintained
		// separately and disagreeing: three UNIT3D forks already declare
		// join_date that way, and none of them should count it as an API stat.
		if hasDef && td.API != nil {
			for _, f := range td.API.RequiredFields {
				delete(set, f)
			}
		}
		out.APIStats = sortedKeys(set)
	}

	// Scraping: policy first, then what the page can actually yield.
	rs := r.ResolveScrape(trackerURL, typeKey)
	out.ScrapePossible = !rs.SkipHTMLScrape && !rs.DisableScraping && !rs.OptedOut
	if out.ScrapePossible {
		if hasDef && td.Capabilities != nil && len(td.Capabilities.ScrapeStats) > 0 {
			out.ScrapeStats = append([]string(nil), td.Capabilities.ScrapeStats...)
			sort.Strings(out.ScrapeStats)
		} else {
			out.ScrapeStats = scrapeFieldsOf(rs)
		}
	}
	return out
}

// canonicalFieldsOf lists every canonical field a custom API description can
// produce, across all the ways it can produce one.
func canonicalFieldsOf(api *CustomAPI) []string {
	set := map[string]bool{}
	for _, m := range []map[string]string{api.FieldMap, api.ByteFields, api.UnixFields, api.BoolFields} {
		for _, canonical := range m {
			set[canonical] = true
		}
	}
	for canonical := range api.SumFields {
		set[canonical] = true
	}
	for canonical := range api.SumBytesFields {
		set[canonical] = true
	}
	if api.ClassField != "" {
		set["group"] = true
	}
	// Derived values: computed from the byte fields rather than mapped, so
	// they appear in no map but do reach the user.
	if api.BufferFromBytes {
		set["buffer"] = true
	}
	if api.RatioFromBytes {
		set["ratio"] = true
	}
	return sortedKeys(set)
}

// scrapeFieldsOf lists the canonical fields a profile page can yield: whatever
// the resolved label vocabulary maps to, plus the presence flags. The scraper's
// own base label map is not consulted here — it is generic English label text
// shared by every tracker, so counting it would claim every tracker can scrape
// every stat regardless of what its page actually shows.
func scrapeFieldsOf(rs ResolvedScrape) []string {
	set := map[string]bool{}
	for _, canonical := range rs.Labels {
		set[canonical] = true
	}
	for field := range rs.PresenceFlags {
		set[field] = true
	}
	// Event banners come from the scraper's own extraction pass, not from the
	// label map, so no def mentions them — but every scraped page goes through
	// it, and a tracker showing a freeleech banner does report events. Without
	// this, trackers whose banner Yata displays perfectly well were reporting
	// "events: not available" on the same screen.
	set["active_event"] = true
	return sortedKeys(set)
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// NormalizeAPIFields renames tracker-specific API field names to canonical
// names in-place. When both the alias and the canonical name exist in the
// response, the alias source wins — it is authoritative (v1: Unit3D returns
// both "seedbonus": "593626.75" and a bogus "bonus_points": 1).
func NormalizeAPIFields(fieldMap map[string]string, data map[string]any) map[string]any {
	for apiName, canonical := range fieldMap {
		if v, ok := data[apiName]; ok {
			data[canonical] = v
			delete(data, apiName)
		}
	}
	return data
}

// APIKind returns the fetcher kind for a tracker (by URL + type key).
func (r *Registry) APIKind(trackerURL, typeKey string) string {
	td, hasDef := r.TrackerByURL(trackerURL)
	if hasDef {
		// A shut-down tracker is never contacted again. Checked HERE rather
		// than left to the caller because the api.kind lives on the TYPE,
		// which is stored on the user's tracker — so simply deleting a dead
		// tracker's def does not stop the requests, it only strips the name
		// and ladder its stored history still refers to.
		if td.Retired != nil {
			return "none"
		}
		// A def that declares its own full API block (path + mappings) always
		// fetches through the custom fetcher, whatever its base type. The type
		// keeps driving everything else (display, credential fields, scrape
		// conventions) — e.g. HUNO is a UNIT3D tracker whose stats come from a
		// bespoke /api/profile endpoint.
		if td.API != nil && td.API.Path != "" {
			return "custom"
		}
		if td.Type != "" {
			typeKey = td.Type
		}
	}
	// A type can supply the path for a whole family of trackers, in which case
	// its members fetch through the custom fetcher without restating it.
	if tt, ok := r.Type(typeKey); ok && tt.CustomAPI != nil && tt.CustomAPI.Path != "" {
		return "custom"
	}
	if tt, ok := r.Type(typeKey); ok {
		return tt.API.Kind
	}
	// An unresolvable type means we do not know what software the site runs.
	// This used to fall back to UNIT3D, which turned every unrecognised
	// tracker into a stream of requests to endpoints it does not have — and
	// presented the result to the user as a UNIT3D tracker that was merely
	// failing. Collecting nothing is the honest answer; the UI prompts for a
	// type instead.
	return "none"
}
