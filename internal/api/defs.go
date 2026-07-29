package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
)

func registerDefs(r chi.Router, d *Deps) {
	r.Get("/defs", listDefs(d))
	r.Post("/defs/reload", reloadDefs(d))
	r.Get("/tracker-groups", trackerGroups(d))
}

// defInfo is the trimmed tracker def DTO for UI dropdowns/add-modal.
type defInfo struct {
	Key                string `json:"key"`
	Name               string `json:"name"`
	Abbr               string `json:"abbr"`
	URL                string `json:"url"`
	Type               string `json:"type"`
	HasGroups          bool   `json:"has_groups"`
	ScrapeDisabled     bool   `json:"scrape_disabled"`
	MinIntervalMinutes int    `json:"min_interval_minutes,omitempty"`
	MaxScrapesPerDay   int    `json:"max_scrapes_per_day,omitempty"`
	APIKeyHint         string `json:"api_key_hint,omitempty"`
	// NeedsSessionCookie: the def's custom API authenticates WITH the session
	// cookie — the cookie field must stay visible even though scraping is off.
	NeedsSessionCookie bool   `json:"needs_session_cookie,omitempty"`
	ApprovalStatus     string `json:"approval_status"` // approved|informal|pending|unknown
	ApprovalNote       string `json:"approval_note,omitempty"`
	// RequiredFields is the def-level resolution of the type's required
	// config fields (see requiredFieldsFor). No omitempty: an empty list
	// must reach the UI as [] so it doesn't fall back to the type default.
	RequiredFields []string `json:"required_fields"`
	// Capabilities is what this tracker can actually report, so the picker can
	// show it BEFORE the tracker is added — which is the whole point: a user
	// choosing between two trackers can't consult stats they don't have yet.
	Capabilities *capabilityView `json:"capabilities,omitempty"`
}

// capabilityView is the UI-facing shape of a tracker's capabilities: the
// ladder-coverage counts, and a per-field source map for the icons.
type capabilityView struct {
	// LadderTotal, MetAPI and MetScrape are the "N of M" figures. MetScrape
	// INCLUDES everything MetAPI covers — it is "with scraping as well", not a
	// separate tally, so the UI can render "4 via API · 6 with scraping".
	LadderTotal int `json:"ladder_total"`
	MetAPI      int `json:"met_api"`
	MetScrape   int `json:"met_scrape"`
	// Missing names the ladder requirements neither route can reach, so the
	// tooltip can say WHICH stats are unavailable rather than just how many.
	Missing []string `json:"missing,omitempty"`
	// ScrapePossible is false when the operator forbids scraping or the type
	// can't scrape — the reason MetScrape may equal MetAPI.
	ScrapePossible bool `json:"scrape_possible"`
	// Notables maps the capabilities worth their own icon to "api", "scrape"
	// or "" (not available).
	Notables map[string]string `json:"notables"`
	// APIStats is the full field set, for the detailed breakdown.
	APIStats []string `json:"api_stats,omitempty"`
	// Known is false when nothing has been declared or derived for this
	// tracker — the UI must then say "not recorded" rather than "reports
	// nothing", which are very different claims.
	Known bool `json:"known"`
}

// buildCapabilityView resolves one def's capabilities into the UI shape.
func buildCapabilityView(d *Deps, td defs.TrackerDef) *capabilityView {
	caps := d.Reg.ResolveCapabilities(td.URL, td.Type)
	sum := caps.Summarise(td)
	return &capabilityView{
		LadderTotal:    len(sum.Required),
		MetAPI:         len(sum.MetAPI),
		MetScrape:      len(sum.MetScrape),
		Missing:        sum.Missing,
		ScrapePossible: sum.ScrapePossible,
		Notables:       caps.Notables(),
		APIStats:       sum.APIStats,
		Known:          len(sum.APIStats) > 0 || len(caps.ScrapeStats) > 0,
	}
}

// requiredFieldsFor resolves the required config fields for one tracker.
//
// Two sources are unioned: the TYPE's list (what every tracker of this kind
// needs) and the DEF's own api.required_fields (what this tracker needs on top,
// because it differs from its type's norm — a UNIT3D tracker whose API omits
// the join date and which isn't scraped). Then any field the def's API already
// provides is dropped — a field_map entry mapping member_since → join_date
// means the user never has to enter one (HUNO), while MAM's API reports none so
// the requirement stands. Always returns a non-nil slice.
func requiredFieldsFor(base []string, api *defs.CustomAPI) []string {
	out := make([]string, 0, len(base)+1)
	provided := make(map[string]bool)
	if api != nil {
		for _, canonical := range api.FieldMap {
			provided[canonical] = true
		}
	}
	seen := make(map[string]bool, len(base))
	add := func(fields []string) {
		for _, f := range fields {
			if provided[f] || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	add(base)
	if api != nil {
		add(api.RequiredFields)
	}
	if api != nil && strings.Contains(api.Path, "{username}") {
		hasUsername := false
		for _, field := range out {
			hasUsername = hasUsername || field == "username"
		}
		if !hasUsername {
			out = append(out, "username")
		}
	}
	if api != nil && api.AuthMethod == "session_cookie" && !slices.Contains(out, "session_cookie") {
		out = append(out, "session_cookie")
	}
	return out
}

type typeInfo struct {
	Key            string   `json:"key"`
	Label          string   `json:"label"`
	APIKind        string   `json:"api_kind"`
	RequiredFields []string `json:"required_fields,omitempty"`
}

// GET /api/defs — registry contents + load issues.
func listDefs(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		trackers := d.Reg.Trackers()
		tout := make([]defInfo, 0, len(trackers))
		for _, td := range trackers {
			rs := d.Reg.ResolveScrape(td.URL, td.Type)
			info := defInfo{
				Key:                td.Key,
				Name:               td.Name,
				Abbr:               td.Abbr,
				URL:                td.URL,
				Type:               td.Type,
				HasGroups:          len(td.Groups) > 0,
				ScrapeDisabled:     rs.DisableScraping || rs.SkipHTMLScrape,
				MinIntervalMinutes: rs.MinIntervalMinutes,
				MaxScrapesPerDay:   rs.MaxScrapesPerDay,
				ApprovalStatus:     td.ApprovalStatus(),
				ApprovalNote:       td.ApprovalNote(),
			}
			// Resolve through the type so a tracker inheriting its family's
			// endpoint reports the same hint and required fields it will
			// actually be fetched with.
			customAPI := d.Reg.ResolveCustomAPI(td.URL, td.Type)
			if tt, ok := d.Reg.Type(td.Type); ok {
				info.RequiredFields = requiredFieldsFor(tt.API.RequiredFields, customAPI)
				info.APIKeyHint = tt.API.APIKeyHint // type default…
			} else {
				info.RequiredFields = []string{}
			}
			if customAPI != nil && customAPI.APIKeyHint != "" {
				info.APIKeyHint = customAPI.APIKeyHint // …overridden per tracker
			}
			// The cookie field must stay visible even when scraping is off
			// whenever the API itself needs it — a custom def with auth_method
			// "session_cookie" resolves "session_cookie" into RequiredFields.
			info.NeedsSessionCookie = slices.Contains(info.RequiredFields, "session_cookie")
			info.Capabilities = buildCapabilityView(d, td)
			tout = append(tout, info)
		}
		types := d.Reg.Types()
		tyout := make([]typeInfo, 0, len(types))
		for _, tt := range types {
			tyout = append(tyout, typeInfo{
				Key: tt.Key, Label: tt.Label, APIKind: tt.API.Kind,
				RequiredFields: tt.API.RequiredFields,
			})
		}
		jsonOK(w, map[string]any{
			"trackers": tout,
			"types":    tyout,
			"issues":   d.Reg.Issues(),
			"opt_outs": d.Reg.OptOuts(),
		})
	}
}

// POST /api/defs/reload — re-read the defs directory at runtime.
func reloadDefs(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := d.Reg.Reload(); err != nil {
			d.logErrorf("defs: reload failed — %v", err)
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// A corrected declaration should stop warning on the next fetch, and a
		// newly-broken one should warn again rather than staying quiet because
		// the previous version already did.
		resetCapDriftMemory()
		d.logInfof("defs: reloaded — %d trackers, %d types, %d issues",
			len(d.Reg.Trackers()), len(d.Reg.Types()), len(d.Reg.Issues()))
		jsonOK(w, map[string]any{
			"ok":       true,
			"trackers": len(d.Reg.Trackers()),
			"types":    len(d.Reg.Types()),
			"issues":   d.Reg.Issues(),
		})
	}
}

// GET /api/tracker-groups — group definitions for every tracker def, keyed by
// def key. Used for styled badges, perks, and "Load from Group" targets.
func trackerGroups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := map[string][]defs.GroupDef{}
		for _, td := range d.Reg.Trackers() {
			if len(td.Groups) > 0 {
				out[td.Key] = td.Groups
			}
		}
		jsonOK(w, out)
	}
}
