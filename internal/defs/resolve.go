package defs

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
	if tt, ok := r.Type(typeKey); ok {
		return tt.API.Kind
	}
	return "unit3d" // sensible default for manual trackers with unknown type
}
