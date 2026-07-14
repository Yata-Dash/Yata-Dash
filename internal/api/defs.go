package api

import (
	"net/http"

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
}

// requiredFieldsFor resolves a type's required config fields for one tracker:
// any field the tracker def's custom API already provides is dropped — e.g. a
// field_map entry mapping member_since → join_date means the user never has
// to enter a join date (HUNO), while MAM's API reports none so the type-level
// requirement stands. Always returns a non-nil slice.
func requiredFieldsFor(base []string, api *defs.CustomAPI) []string {
	out := make([]string, 0, len(base))
	if api == nil || len(api.FieldMap) == 0 {
		return append(out, base...)
	}
	provided := make(map[string]bool, len(api.FieldMap))
	for _, canonical := range api.FieldMap {
		provided[canonical] = true
	}
	for _, f := range base {
		if !provided[f] {
			out = append(out, f)
		}
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
			if td.API != nil {
				info.APIKeyHint = td.API.APIKeyHint
				info.NeedsSessionCookie = td.API.AuthMethod == "session_cookie"
			}
			if tt, ok := d.Reg.Type(td.Type); ok {
				info.RequiredFields = requiredFieldsFor(tt.API.RequiredFields, td.API)
			} else {
				info.RequiredFields = []string{}
			}
			tout = append(tout, info)
		}
		types := d.Reg.Types()
		tyout := make([]typeInfo, 0, len(types))
		for _, tt := range types {
			tyout = append(tyout, typeInfo{Key: tt.Key, Label: tt.Label, APIKind: tt.API.Kind, RequiredFields: tt.API.RequiredFields})
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
