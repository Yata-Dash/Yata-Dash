package defs

import "sort"

// Working out how much of a tracker's own promotion ladder Yata can actually
// follow. The number that matters is relative to the tracker's own
// requirements — "4 of 6" says more than a raw stat count, because a tracker
// that promotes on two things needs to report two things.

// requirementStat maps each group requirement to the canonical stat field that
// backs it. Mirrors groupRequirementsToTargets in web/src/utils/group.ts; the
// two must agree or the summary will disagree with the target rows the user
// can see on the same page.
func requirementStats(req GroupRequirements, set map[string]bool) {
	if req.MinUploaded != "" {
		set["uploaded"] = true
	}
	if req.MinDownloaded != "" {
		set["downloaded"] = true
	}
	if req.MinTotalTransfer != "" {
		// A combined threshold needs both halves to be measurable.
		set["uploaded"], set["downloaded"] = true, true
	}
	if req.MinRatio != 0 {
		set["ratio"] = true
	}
	if req.MinSeedtime != "" {
		set["avg_seed_time"] = true
	}
	if req.MinSeedSize != "" {
		set["seed_size"] = true
	}
	if req.MinUploads != 0 {
		set["uploads_approved"] = true
	}
	if req.MinAdoptions != 0 {
		set["adoptions"] = true
	}
	if req.MinBonusPoints != 0 {
		set["bonus_points"] = true
	}
	if req.MinAge != "" {
		set["join_date"] = true
	}
	// Backed by the monthly_uploads stat, which trackers can now report
	// (Aither's torrent_uploads_month was the first). Counting it is what
	// makes the ladder figure honest for the uploader classes it gates: a
	// tracker that reports it can be followed all the way, and one that
	// doesn't is short by a stat its own requirements need — which is exactly
	// what the number is there to say. Until a tracker reported it, counting
	// it would have marked every def using it permanently short for a gap in
	// Yata rather than in the tracker.
	if req.MinMonthlyUploads != 0 {
		set["monthly_uploads"] = true
	}
	for _, mc := range req.MinCounts {
		if mc.Field != "" {
			set[mc.Field] = true
		}
	}
	// Alternatives count too: a rank reachable by uploads OR adoptions needs
	// both measurable before Yata can tell the user which route is closer.
	for _, alt := range req.AnyOf {
		requirementStats(alt, set)
	}
}

// LadderStats returns every canonical stat field a def's group ladder needs in
// order to be followed — the denominator of the "N of M" figure. Ranks that
// are appointed rather than earned (a Description and no thresholds) contribute
// nothing, so staff classes don't inflate it.
func LadderStats(td TrackerDef) []string {
	set := map[string]bool{}
	for _, g := range td.Groups {
		requirementStats(g.Requirements, set)
	}
	return sortedKeys(set)
}

// CapabilitySummary answers "what can Yata see on this tracker?" for one def.
type CapabilitySummary struct {
	// Required is the tracker's own ladder requirements (the "of M").
	Required []string `json:"required"`
	// MetAPI and MetScrape are the requirements covered by the API alone, and
	// by the API plus scraping. Reported separately because scraping is off by
	// default on unapproved trackers and needs a session cookie the user may
	// not have — one number would flatter or understate depending which you
	// picked.
	MetAPI    []string `json:"met_api"`
	MetScrape []string `json:"met_scrape"`
	// Missing is what neither route can reach.
	Missing []string `json:"missing"`
	// ScrapePossible is false when the operator forbids scraping or the type
	// cannot scrape — the reason MetScrape may equal MetAPI.
	ScrapePossible bool `json:"scrape_possible"`
	// APIStats is the full API field set, for the detailed breakdown.
	APIStats []string `json:"api_stats"`
	// Declared is false when the set was derived from a def that describes its
	// own API, so nothing had to be declared by hand.
	Declared bool `json:"declared"`
}

// notableFields are the capabilities worth their own icon, beyond the ladder
// coverage figure. Each entry lists every canonical field that satisfies it:
// events arrive as the structured `active_events` list from newer UNIT3D APIs
// but as the flat `active_event` banner string from a profile scrape, and a
// tracker showing the banner plainly does report events — checking only the
// structured form made every scraped tracker read "Not reported" while its
// freeleech banner sat at the top of the page.
var notableFields = map[string][]string{
	"unread_mail":          {"unread_mail"},
	"unread_notifications": {"unread_notifications"},
	"active_events":        {"active_events", "active_event"},
}

// FieldSource says how a field is obtainable: "api", "scrape" or "" for not
// at all. Drives the tri-state capability icons.
func (rc ResolvedCapabilities) FieldSource(field string) string {
	switch {
	case rc.HasAPI(field):
		return "api"
	case sortedContains(rc.ScrapeStats, field):
		return "scrape"
	default:
		return ""
	}
}

// Notables reports the source of each notable capability. Where several fields
// satisfy one capability the best answer wins — an API that reports events
// outranks a scraped banner saying the same thing.
func (rc ResolvedCapabilities) Notables() map[string]string {
	out := make(map[string]string, len(notableFields))
	for key, fields := range notableFields {
		best := ""
		for _, f := range fields {
			switch rc.FieldSource(f) {
			case "api":
				best = "api"
			case "scrape":
				if best == "" {
					best = "scrape"
				}
			}
		}
		out[key] = best
	}
	return out
}

// userSuppliable are fields the user can always enter at setup, so they are
// never a gap in what a tracker provides. A tracker whose API omits the join
// date declares it in api.required_fields, which makes it a required input on
// the add form — so account age is trackable either way, and counting it as
// missing would mark the tracker short for something it can't affect and the
// user has already handled.
var userSuppliable = map[string]bool{"join_date": true}

// Summarise combines a def's ladder requirements with what the tracker can
// report.
func (rc ResolvedCapabilities) Summarise(td TrackerDef) CapabilitySummary {
	required := LadderStats(td)
	sum := CapabilitySummary{
		Required:       required,
		ScrapePossible: rc.ScrapePossible,
		APIStats:       rc.APIStats,
		Declared:       !rc.Derived,
	}
	for _, f := range required {
		switch {
		case rc.HasAPI(f) || userSuppliable[f]:
			sum.MetAPI = append(sum.MetAPI, f)
			sum.MetScrape = append(sum.MetScrape, f)
		case sortedContains(rc.ScrapeStats, f):
			sum.MetScrape = append(sum.MetScrape, f)
		default:
			sum.Missing = append(sum.Missing, f)
		}
	}
	return sum
}

func sortedContains(list []string, v string) bool {
	i := sort.SearchStrings(list, v)
	return i < len(list) && list[i] == v
}
