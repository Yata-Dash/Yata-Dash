package pathways

import "github.com/Yata-Dash/Yata-Dash/internal/defs"

// Pin states — see PINNED_PATHWAYS_PLAN.md §1. A pin is never dropped by
// the app; each state is reported and the user decides.
const (
	PinOK       = "ok"       // every hop resolves to an active route
	PinInactive = "inactive" // a hop's route exists but is marked inactive
	PinMissing  = "missing"  // a hop's route is no longer in the dataset
	PinNoStart  = "no_start" // none of the hops is a tracker the user has
	PinReached  = "reached"  // the user now has the destination
)

// PinResult is one pinned chain measured against the user's trackers.
//
// Path is embedded so the UI renders it with the same chips and step details
// as a searched path. StartIndex says which hop the evaluation started from —
// the furthest along the chain the user already holds — so hops before it
// render as done. Met/Total are the leaf requirement counts of the first open
// hop, the only hop with live stats and therefore the only honest source for
// a progress bar.
type PinResult struct {
	Hops        []string `json:"hops"`
	Destination string   `json:"destination"`
	State       string   `json:"state"`
	// BrokenHop is the index into Hops of the hop whose route is missing or
	// inactive (its "to" end). -1 when State is not missing/inactive.
	BrokenHop  int `json:"broken_hop"`
	StartIndex int `json:"start_index"`
	Met        int `json:"met"`
	Total      int `json:"total"`
	Path
}

// EvalPin measures one pinned chain. No search, no ranking, no path cap — a
// pinned path is exact, so it is looked up, not found.
//
// Evaluation starts from the furthest hop the user already has in Yata, so a
// pin advances as the user does: Aither → Anthelion → BTN measures Anthelion →
// BTN once Anthelion is added. The first open hop is evaluated against live
// stats; later hops stay estimates, exactly as in FindPaths.
func EvalPin(d *Data, hops []string, users []UserTracker,
	groupsFor func(string) []defs.GroupDef,
	inviteReqsFor func(string) *defs.InviteReqs) PinResult {
	res := PinResult{Hops: hops, BrokenHop: -1, StartIndex: -1}
	if len(hops) > 0 {
		res.Destination = hops[len(hops)-1]
	}
	if len(hops) < 2 {
		res.State = PinMissing // nothing to walk; still returned so it can be unpinned
		return res
	}

	userByName := map[string]UserTracker{}
	for _, u := range users {
		userByName[u.PathwayName] = u
	}
	// Furthest owned hop. Scanning from the end so a user who holds both the
	// start and an intermediate tracker is measured from the intermediate.
	for i := len(hops) - 1; i >= 0; i-- {
		if _, ok := userByName[hops[i]]; ok {
			res.StartIndex = i
			break
		}
	}
	if res.StartIndex < 0 {
		res.State = PinNoStart
		return res
	}
	if res.StartIndex == len(hops)-1 {
		res.State = PinReached
		return res
	}

	u := userByName[hops[res.StartIndex]]
	res.State = PinOK
	var steps []Step
	// Deliberately from StartIndex, not 0. Hops behind the user are history:
	// whether Aither → HDBits still exists says nothing about someone who
	// already holds HDBits, and failing their pin over it would be reporting a
	// problem they cannot have. The chain ahead is what still has to work.
	for i := res.StartIndex; i < len(hops)-1; i++ {
		r, ok := findRoute(d, hops[i], hops[i+1])
		if !ok {
			res.State, res.BrokenHop = PinMissing, i+1
			break
		}
		if !r.Active {
			res.State, res.BrokenHop = PinInactive, i+1
			break
		}
		steps = append(steps, evalStep(r, u, len(steps) == 0, d, groupsFor, inviteReqsFor))
	}
	res.Path = buildPath(u, steps)
	if len(steps) > 0 {
		res.Met, res.Total = countLeafReqs(steps[0].Reqs)
	}
	return res
}

// findRoute looks up the exact edge from → to.
func findRoute(d *Data, from, to string) (Route, bool) {
	for _, r := range d.From(from) {
		if r.To == to {
			return r, true
		}
	}
	return Route{}, false
}

// countLeafReqs counts what the progress bar measures: one unit per
// requirement the user has to satisfy. An any_of is one unit (met when any
// alternative is), a class requirement is one unit (the class, not each of
// its component thresholds), and a row that cannot be measured still counts —
// unavailable is not met.
func countLeafReqs(rows []ReqProgress) (met, total int) {
	for _, q := range rows {
		total++
		if q.Met {
			met++
		}
	}
	return met, total
}
