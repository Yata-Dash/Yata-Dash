// highlights.go — GET /api/highlights: the "what needs my attention" endpoint
// for dashboard cards that have one card's worth of room (Dashbrr, Homepage,
// Homarr). Deliberately NOT part of /api/summary: summary is the stable
// documented totals feed and integrations already depend on its shape, so the
// ranked lists live beside it rather than inside it. A dashboard fetches both
// and degrades to totals-only against a Yata that predates this endpoint.
//
// Like /api/summary, this serves ONLY stored data — polling it never contacts
// a tracker. Mounted on the token-or-session group (server.go).
package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/pathways"
)

// highlightDefaultLimit is one card's worth of rows. highlightMaxLimit exists
// so a caller can ask for a fuller list without being able to turn this into
// an unbounded dump of every tracker.
const (
	highlightDefaultLimit = 5
	highlightMaxLimit     = 25
)

// highlightTarget is one tracker's target progress, collapsed to the m/T the
// dashboard shows. Same evaluation the alert engine and the weekly digest use
// (targeteval.go), so the three never disagree about what "met" means.
type highlightTarget struct {
	TrackerID string `json:"tracker_id"`
	Name      string `json:"name"`
	Abbr      string `json:"abbr,omitempty"`
	Met       int    `json:"met"`
	Total     int    `json:"total"`
	// Remaining is Total-Met, precomputed so a template can sort or threshold
	// on it without arithmetic.
	Remaining int `json:"remaining"`
}

// GET /api/highlights?limit=5
func getHighlights(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := highlightDefaultLimit
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				jsonError(w, "limit must be a positive integer", http.StatusBadRequest)
				return
			}
			limit = min(n, highlightMaxLimit)
		}

		targets := highlightTargets(d)
		paths := highlightPathways(d)

		jsonOK(w, map[string]any{
			"generated_at": time.Now().Unix(),
			"limit":        limit,
			"targets":      capSlice(targets, limit),
			// target_count / pathway_count are the FULL lengths before
			// truncation, so a card can honestly say "5 of 12".
			"target_count":       len(targets),
			"pathways":           capSlice(paths, limit),
			"pathway_count":      len(paths),
			"pathways_available": d.Paths != nil,
		})
	}
}

// highlightTargets ranks the user's trackers by how close they are to
// finishing their tracked targets. Disabled trackers are excluded: their
// stats are frozen, so a "4/6" from one is a claim about the past.
//
// Fully-met trackers are kept and rank FIRST (Remaining 0) — a completed
// target set is the most actionable row on the list, not the least: it is the
// moment to go ask for the promotion.
func highlightTargets(d *Deps) []highlightTarget {
	var out []highlightTarget
	for _, t := range d.Cfg.Trackers() {
		if !t.Enabled {
			continue
		}
		if len(t.Targets) == 0 && t.TargetGroup == "" {
			continue
		}
		merged, err := d.Stats.Merged(t.ID)
		if err != nil {
			continue
		}
		var groups []defs.GroupDef
		name, abbr := t.Name, ""
		if td, ok := d.Reg.TrackerByURL(t.URL); ok {
			groups, abbr = td.Groups, td.Abbr
			if name == "" {
				name = td.Name
			}
		}
		_, met, total := evaluateTargetRows(t, merged, groups)
		if total == 0 {
			continue // nothing evaluable — a group with no live-backed reqs
		}
		out = append(out, highlightTarget{
			TrackerID: t.ID, Name: name, Abbr: abbr,
			Met: met, Total: total, Remaining: total - met,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Remaining != b.Remaining {
			return a.Remaining < b.Remaining
		}
		// Among equally-close trackers, more met means further along overall.
		if a.Met != b.Met {
			return a.Met > b.Met
		}
		return a.Name < b.Name
	})
	return out
}

// highlightPathways ranks pathway targets the user is closest to qualifying
// for, one row per target (see pathways.TargetSummaries).
//
// Targets marked "not interested" in the Pathways view are dropped entirely.
// That list is the user having said, explicitly, that they don't want to be
// told about this tracker — a dashboard card is exactly the surface that
// would otherwise nag about it forever.
func highlightPathways(d *Deps) []pathways.TargetSummary {
	if d.Paths == nil {
		return nil
	}
	users := mapUserTrackers(d)
	owned := make(map[string]bool, len(users))
	for _, u := range users {
		owned[u.PathwayName] = true
	}
	groupsFor, inviteReqsFor := defLookups(d)
	all := pathways.TargetSummaries(d.Paths, users, owned, groupsFor, inviteReqsFor)

	skip := notInterestedSet(d)
	if len(skip) == 0 {
		return all
	}
	out := make([]pathways.TargetSummary, 0, len(all))
	for _, s := range all {
		if !skip[strings.ToLower(s.Name)] {
			out = append(out, s)
		}
	}
	return out
}

// notInterestedSet lowercases the user's not-interested pathway targets for
// comparison. The list is stored as dataset tracker names, which is what
// TargetSummary.Name carries, but casing is whatever the UI wrote.
func notInterestedSet(d *Deps) map[string]bool {
	names := d.Cfg.Settings().PathwayNotInterested
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			out[strings.ToLower(n)] = true
		}
	}
	return out
}

// capSlice truncates to n, always returning a non-nil slice so the JSON is an
// empty array rather than null — a template doing .targets[0] on null is a
// harder error to read than one on [].
func capSlice[T any](in []T, n int) []T {
	if len(in) > n {
		in = in[:n]
	}
	if in == nil {
		return []T{}
	}
	return in
}
