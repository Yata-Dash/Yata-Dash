package pathways

import (
	"sort"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
)

// TargetSummary rolls every route the user has INTO one pathway target up into
// a single row. One entry per TARGET, never per route: someone on three
// trackers that all lead to Upload.cx gets one "Upload.cx" with Routes=3, not
// three near-identical lines competing for space on a dashboard card.
//
// This is deliberately lossier than FindPaths/DirectRoutesFrom — it answers
// "which targets should I look at, and how close am I" for a caller with room
// for five lines. Anything wanting the actual route detail still uses the
// existing endpoints.
type TargetSummary struct {
	Name string `json:"name"`
	Abbr string `json:"abbr,omitempty"`
	// Routes is how many of the user's OWN trackers have an active direct
	// route in — the "(x2)" on a dashboard card. Counted per SOURCE TRACKER,
	// so two dataset routes from the same tracker still count once; the number
	// means "how many of my memberships can get me there", which is what a
	// reader assumes it means.
	Routes int `json:"routes"`
	// ReadyFrom is how many of those sources meet every listed requirement.
	ReadyFrom int  `json:"ready_from"`
	Ready     bool `json:"ready"`
	// ETADays is the BEST (lowest) estimate across the routes in, and From
	// names the source tracker it belongs to — the one route worth working on.
	// Meaningful only when HasUnknown is false; otherwise it is a floor, since
	// some requirement could not be estimated at all.
	ETADays    float64 `json:"eta_days"`
	HasUnknown bool    `json:"has_unknown"`
	From       string  `json:"from,omitempty"`
}

// TargetSummaries evaluates every active direct route out of the user's
// trackers and groups the results by destination. Targets the user already
// owns are skipped, matching DirectRoutesFrom.
//
// Ordering is "what should I look at first": ready targets, then targets whose
// ETA is a real number, then the shortest ETA, then name for stability. The
// caller truncates — it must NOT re-sort, or the truncation stops meaning
// anything.
//
// Same caveat as the rest of this package: the dataset is community-driven,
// and meeting the listed requirements never guarantees an invite.
func TargetSummaries(d *Data, users []UserTracker, owned map[string]bool,
	groupsFor func(pathwayName string) []defs.GroupDef,
	inviteReqsFor func(pathwayName string) *defs.InviteReqs) []TargetSummary {
	if d == nil {
		return nil
	}

	type acc struct {
		sum TargetSummary
		// Sets, not counters: a tracker with two dataset routes to the same
		// target must not inflate Routes to 2.
		sources  map[string]bool
		readySrc map[string]bool
		haveBest bool
	}
	byTarget := map[string]*acc{}

	for _, u := range users {
		for _, r := range d.From(u.PathwayName) {
			if !r.Active || r.To == u.PathwayName || owned[r.To] {
				continue
			}
			step := evalStep(r, u, true, d, groupsFor, inviteReqsFor)

			a := byTarget[r.To]
			if a == nil {
				a = &acc{
					sum:      TargetSummary{Name: r.To, Abbr: d.Abbr[r.To]},
					sources:  map[string]bool{},
					readySrc: map[string]bool{},
				}
				byTarget[r.To] = a
			}
			a.sources[u.PathwayName] = true
			// Same test ReadyTargets uses: a zero known ETA with nothing
			// unknown means every listed requirement is met. A disabled
			// tracker always carries HasUnknown (evalStep forces it), so
			// frozen stats can never claim readiness here either.
			if step.ETADays == 0 && !step.HasUnknown {
				a.readySrc[u.PathwayName] = true
			}
			if betterStep(step, a.sum, a.haveBest) {
				a.sum.ETADays = step.ETADays
				a.sum.HasUnknown = step.HasUnknown
				a.sum.From = r.From
				a.haveBest = true
			}
		}
	}

	out := make([]TargetSummary, 0, len(byTarget))
	for _, a := range byTarget {
		a.sum.Routes = len(a.sources)
		a.sum.ReadyFrom = len(a.readySrc)
		a.sum.Ready = a.sum.ReadyFrom > 0
		out = append(out, a.sum)
	}
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.Ready != y.Ready {
			return x.Ready
		}
		if x.HasUnknown != y.HasUnknown {
			return !x.HasUnknown
		}
		if x.ETADays != y.ETADays {
			return x.ETADays < y.ETADays
		}
		return x.Name < y.Name
	})
	return out
}

// betterStep reports whether a candidate route beats the best recorded so far.
// A route with everything estimable always wins over one with unknowns — its
// ETA is a real number rather than a lower bound — and among equals the
// shorter estimate wins.
func betterStep(cand Step, best TargetSummary, have bool) bool {
	if !have {
		return true
	}
	if cand.HasUnknown != best.HasUnknown {
		return !cand.HasUnknown
	}
	return cand.ETADays < best.ETADays
}
