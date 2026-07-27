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
