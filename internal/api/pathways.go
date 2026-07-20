package api

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
	"github.com/Yata-Dash/Yata-Dash/internal/pathways"
)

func registerPathways(r chi.Router, d *Deps) {
	r.Get("/pathways/targets", pathwayTargets(d))
	r.Get("/pathways/paths", pathwayPaths(d))
	r.Get("/pathways/from", pathwayFrom(d))
}

// GET /api/pathways/from?tracker=<id> — active direct routes leaving one of
// the user's trackers, evaluated against live stats (the Tracker Detail
// page's "pathways from here"). 404 when pathways data is absent; an empty
// list when the tracker isn't in the dataset.
func pathwayFrom(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Paths == nil {
			jsonError(w, "pathways_data_missing", http.StatusNotFound)
			return
		}
		id := strings.TrimSpace(r.URL.Query().Get("tracker"))
		if id == "" {
			jsonError(w, "tracker is required", http.StatusBadRequest)
			return
		}
		users := mapUserTrackers(d)
		owned := map[string]bool{}
		for _, u := range users {
			owned[u.PathwayName] = true
		}
		routes := []pathways.Step{}
		for _, u := range users {
			if u.TrackerID != id {
				continue
			}
			groupsFor, inviteReqsFor := defLookups(d)
			routes = pathways.DirectRoutesFrom(d.Paths, u, owned, groupsFor, inviteReqsFor)
			break
		}
		jsonOK(w, map[string]any{"source": d.Paths.Source, "routes": routes})
	}
}

// targetEntry is one selectable target tracker.
type targetEntry struct {
	Name    string `json:"name"`
	Abbr    string `json:"abbr,omitempty"`
	DefKey  string `json:"def_key,omitempty"` // matched Yata def
	IsMine  bool   `json:"is_mine"`           // user already has it
	Inbound int    `json:"inbound"`           // number of active routes in
	// ReqsMet: the user meets ALL listed requirements on at least one active
	// direct route in (live stats vs community data — never a guarantee).
	ReqsMet bool `json:"reqs_met,omitempty"`
}

// defLookups builds the def-resolution callbacks the pathways engine needs.
func defLookups(d *Deps) (func(string) []defs.GroupDef, func(string) *defs.InviteReqs) {
	groupsFor := func(name string) []defs.GroupDef {
		if td, ok := matchDef(d.Reg, d.Paths, name); ok {
			return td.Groups
		}
		return nil
	}
	inviteReqsFor := func(name string) *defs.InviteReqs {
		if td, ok := matchDef(d.Reg, d.Paths, name); ok {
			return td.InviteRequirements
		}
		return nil
	}
	return groupsFor, inviteReqsFor
}

// pathwayReadiness bundles the pathway evaluation both pathwayTargets and the
// weekly digest (digest.go's readyPathwayTargetNames) need: which pathway
// target names are already the user's own trackers, and which are
// requirements-met on at least one active direct route (live stats, first
// hop only — see pathways.ReadyTargets). Factored out so the digest doesn't
// re-derive mapUserTrackers/defLookups/ReadyTargets from scratch.
func pathwayReadiness(d *Deps) (mine, ready map[string]bool) {
	users := mapUserTrackers(d)
	mine = map[string]bool{}
	for _, u := range users {
		mine[u.PathwayName] = true
	}
	groupsFor, inviteReqsFor := defLookups(d)
	ready = pathways.ReadyTargets(d.Paths, users, groupsFor, inviteReqsFor)
	return mine, ready
}

// GET /api/pathways/targets — every tracker in the dataset (the UI's target
// dropdown), plus the dataset attribution for the disclosure banner.
func pathwayTargets(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Paths == nil {
			jsonError(w, "pathways_data_missing", http.StatusNotFound)
			return
		}
		mine, ready := pathwayReadiness(d)
		out := make([]targetEntry, 0, len(d.Paths.Names()))
		for _, name := range d.Paths.Names() {
			e := targetEntry{
				Name: name, Abbr: d.Paths.Abbr[name], IsMine: mine[name],
				ReqsMet: ready[name] && !mine[name],
			}
			if td, ok := matchDef(d.Reg, d.Paths, name); ok {
				e.DefKey = td.Key
			}
			for _, rt := range d.Paths.To(name) {
				if rt.Active {
					e.Inbound++
				}
			}
			out = append(out, e)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		jsonOK(w, map[string]any{
			"source":  d.Paths.Source,
			"targets": out,
		})
	}
}

// GET /api/pathways/paths?target=Aither — ranked paths from the user's
// trackers to the target, with live-stat requirement evaluation on the
// first hop.
func pathwayPaths(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Paths == nil {
			jsonError(w, "pathways_data_missing", http.StatusNotFound)
			return
		}
		target := strings.TrimSpace(r.URL.Query().Get("target"))
		if target == "" {
			jsonError(w, "target is required", http.StatusBadRequest)
			return
		}
		users := mapUserTrackers(d)
		groupsFor, inviteReqsFor := defLookups(d)
		jsonOK(w, pathways.FindPaths(d.Paths, users, target, groupsFor, inviteReqsFor))
	}
}

// matchDef maps a pathway tracker name to a Yata def: by def name, def
// key, or the dataset's abbreviation, case-insensitively.
func matchDef(reg *defs.Registry, p *pathways.Data, name string) (defs.TrackerDef, bool) {
	abbr := p.Abbr[name]
	for _, td := range reg.Trackers() {
		if strings.EqualFold(td.Name, name) || strings.EqualFold(td.Key, name) ||
			(abbr != "" && strings.EqualFold(td.Abbr, abbr)) {
			return td, true
		}
	}
	return defs.TrackerDef{}, false
}

// mapUserTrackers converts the user's enabled trackers into pathway inputs:
// pathway-name mapping + current stats + growth rates from 7-day history.
func mapUserTrackers(d *Deps) []pathways.UserTracker {
	var out []pathways.UserTracker
	nameIndex := map[string]string{} // lowercase def name/key/abbr → pathway name
	for _, n := range d.Paths.Names() {
		nameIndex[strings.ToLower(n)] = n
		if a := d.Paths.Abbr[n]; a != "" {
			nameIndex[strings.ToLower(a)] = n
		}
	}

	for _, t := range d.Cfg.Trackers() {
		if !t.Enabled {
			continue
		}
		// Resolve this tracker's pathway name via its def.
		var pname string
		if td, ok := d.Reg.TrackerByURL(t.URL); ok {
			for _, candidate := range []string{td.Name, td.Key, td.Abbr} {
				if n, hit := nameIndex[strings.ToLower(candidate)]; hit && candidate != "" {
					pname = n
					break
				}
			}
		}
		if pname == "" {
			continue // tracker not in the pathway dataset
		}

		merged, err := d.Stats.Merged(t.ID)
		if err != nil {
			continue
		}
		gr := d.Stats.GrowthRates(t.ID)
		u := pathways.UserTracker{
			TrackerID:   t.ID,
			PathwayName: pname,
			Stats:       statsFromMerged(merged),
			Rates: pathways.Rates{
				UploadGiB:   gr["uploaded"],
				DownloadGiB: gr["downloaded"],
				SeedSizeGiB: gr["seed_size"],
				Bonus:       gr["bonus_points"],
			},
		}
		out = append(out, u)
	}
	return out
}

// statsFromMerged extracts the numbers the engine needs (-1 = unknown).
func statsFromMerged(m models.MergedStats) pathways.Stats {
	s := pathways.Stats{AgeDays: -1, UploadedGiB: -1, DownloadedGiB: -1, Ratio: -1, SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, Adoptions: -1, BonusPoints: -1}
	str := func(f string) string {
		if v, ok := m[f]; ok {
			if sv, ok := v.Value.(string); ok {
				return sv
			}
		}
		return ""
	}
	num := func(f string) float64 {
		v, ok := m[f]
		if !ok {
			return -1
		}
		switch n := v.Value.(type) {
		case float64:
			return n
		case string:
			if n == "" {
				return -1
			}
			return parse.AnyFloat(n)
		}
		return -1
	}
	if g := parse.SizeToGiB(str("uploaded")); g != nil {
		s.UploadedGiB = *g
	}
	if g := parse.SizeToGiB(str("downloaded")); g != nil {
		s.DownloadedGiB = *g
	}
	if g := parse.SizeToGiB(str("seed_size")); g != nil {
		s.SeedSizeGiB = *g
	}
	if v := num("ratio"); v >= 0 {
		s.Ratio = v
	}
	if v := num("uploads_approved"); v >= 0 {
		s.Uploads = v
	}
	if v := num("adoptions"); v >= 0 {
		s.Adoptions = v
	}
	if v := num("bonus_points"); v >= 0 {
		s.BonusPoints = v
	}
	if sec := parse.SeedTimeToSeconds(str("avg_seed_time")); sec != nil {
		s.AvgSeedSec = *sec
	}
	if jd := str("join_date"); jd != "" {
		if t, err := time.Parse("2006-01-02", jd); err == nil {
			s.AgeDays = time.Since(t).Hours() / 24
		}
	}
	return s
}

