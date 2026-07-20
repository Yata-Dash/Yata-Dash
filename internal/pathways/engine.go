package pathways

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
)

// ─────────────────────────────────────────────────────────────────────────────
// Inputs
// ─────────────────────────────────────────────────────────────────────────────

// Stats is a user's current numbers on one tracker. -1 = unknown.
type Stats struct {
	AgeDays       float64
	UploadedGiB   float64
	DownloadedGiB float64
	Ratio         float64
	SeedSizeGiB   float64
	AvgSeedSec    float64
	Uploads       float64
	Adoptions     float64
	BonusPoints   float64
}

// Rates are growth estimates from recent history (per day; 0 = unknown).
type Rates struct {
	UploadGiB   float64
	DownloadGiB float64
	SeedSizeGiB float64
	Bonus       float64
}

// UserTracker is one of the user's configured trackers mapped into the
// pathway dataset.
type UserTracker struct {
	TrackerID   string
	PathwayName string
	DefKey      string
	Stats       Stats
	Rates       Rates
}

// ─────────────────────────────────────────────────────────────────────────────
// Outputs (JSON-shaped for the API)
// ─────────────────────────────────────────────────────────────────────────────

// ReqProgress is the evaluation of one requirement for display.
// When Kind is set, Have/Need (+texts) carry quantitative progress so the UI
// can render a bar: Have -1 = unknown current value.
type ReqProgress struct {
	Label   string  `json:"label"`
	Met     bool    `json:"met"`
	ETADays float64 `json:"eta_days"` // 0 when met; -1 unknown
	Note    string  `json:"note,omitempty"`

	Kind     string  `json:"kind,omitempty"` // uploaded|seed_size|ratio|seedtime|uploads|bonus|age
	Have     float64 `json:"have,omitempty"`
	Need     float64 `json:"need,omitempty"`
	HaveText string  `json:"have_text,omitempty"`
	NeedText string  `json:"need_text,omitempty"`

	// HasUnknown marks a class row whose ETADays is a LOWER BOUND: some
	// component couldn't be projected but the known parts (account age in
	// particular — always exact, never compressible) still give a floor.
	// Rendered as "N+".
	HasUnknown bool `json:"has_unknown,omitempty"`

	// Classes is the per-class breakdown for "reach class X (or Y)" reqs:
	// every listed alternative with its full requirement bars.
	Classes []ClassEval `json:"classes,omitempty"`
}

// ClassEval is the evaluation of one group/class a route requires.
// With HasUnknown, ETADays is the floor from the known components.
type ClassEval struct {
	Name       string          `json:"name"`
	Met        bool            `json:"met"`
	ETADays    float64         `json:"eta_days"`
	HasUnknown bool            `json:"has_unknown,omitempty"`
	Fastest    bool            `json:"fastest,omitempty"`
	Reqs       []ReqProgress   `json:"reqs"`             // base requirements (ALL must be met)
	AnyOf      [][]ReqProgress `json:"any_of,omitempty"` // alternatives (ONE must be met)
}

// Step is one edge of a path with its evaluated requirements.
// ETADays is the maximum of the KNOWN requirement ETAs — when HasUnknown is
// set it is a LOWER BOUND (account age in particular is always known and
// non-compressible), displayed as "N+".
type Step struct {
	From       string        `json:"from"`
	To         string        `json:"to"`
	Days       int           `json:"days"`
	ReqsRaw    string        `json:"reqs_raw"`
	Updated    string        `json:"updated,omitempty"`
	Reqs       []ReqProgress `json:"reqs"`
	ETADays    float64       `json:"eta_days"`
	HasUnknown bool          `json:"has_unknown"` // some requirement not estimable → ETADays is a floor
	Estimated  bool          `json:"estimated"`   // true beyond the first hop (no live stats)
	// DefNote is extra context from the FROM tracker's def invite_requirements
	// (shown distinctly from the community reqs_raw text).
	DefNote string `json:"def_note,omitempty"`
}

// Path is a full chain from one of the user's trackers to the target.
// TotalETADays sums every step's known ETA — steps in a chain are strictly
// sequential (you must reach hop 1's class before hop 2's clock starts), so
// known components always accumulate. With HasUnknown it's a lower bound.
type Path struct {
	StartTrackerID string  `json:"start_tracker_id"`
	StartName      string  `json:"start_name"`
	Steps          []Step  `json:"steps"`
	TotalETADays   float64 `json:"total_eta_days"`
	HasUnknown     bool    `json:"has_unknown"` // some requirement couldn't be estimated → total is a floor
}

// Suggestion is an intermediate tracker that can reach the target, offered
// when the user has no direct route.
type Suggestion struct {
	Name    string `json:"name"`
	Days    int    `json:"days"`
	Reqs    string `json:"reqs"`
	Updated string `json:"updated,omitempty"`
}

// Result is the full answer for one target.
type Result struct {
	Target      string       `json:"target"`
	Source      SourceInfo   `json:"source"` // attribution for the disclosure
	Direct      bool         `json:"direct"` // user has at least one 1-hop path
	Paths       []Path       `json:"paths"`
	Suggestions []Suggestion `json:"suggestions,omitempty"`
}

const (
	maxDepth = 3
	maxPaths = 8
)

// FindPaths computes ranked paths from the user's trackers to target.
// groupsFor returns the def groups for a pathway tracker name ("" def key =
// no def); inviteReqsFor returns a tracker's def-level invite requirements
// (nil = none — the common case). The first hop is evaluated against live
// stats; later hops use the community estimates and are marked Estimated.
func FindPaths(d *Data, users []UserTracker, target string,
	groupsFor func(pathwayName string) []defs.GroupDef,
	inviteReqsFor func(pathwayName string) *defs.InviteReqs) Result {
	res := Result{Target: target, Source: d.Source}

	userByName := map[string]UserTracker{}
	for _, u := range users {
		userByName[u.PathwayName] = u
	}

	for _, u := range users {
		if u.PathwayName == target {
			continue // already there
		}
		var walk func(at string, visited map[string]bool, steps []Step)
		walk = func(at string, visited map[string]bool, steps []Step) {
			if len(steps) >= maxDepth {
				return
			}
			visited[at] = true
			defer delete(visited, at)
			for _, r := range d.From(at) {
				if !r.Active || visited[r.To] {
					continue
				}
				if _, isOwn := userByName[r.To]; isOwn && r.To != target {
					continue // routing through a tracker the user already has adds nothing
				}
				step := evalStep(r, u, len(steps) == 0, d, groupsFor, inviteReqsFor)
				next := append(append([]Step(nil), steps...), step)
				if r.To == target {
					res.Paths = append(res.Paths, buildPath(u, next))
					continue
				}
				walk(r.To, visited, next)
			}
		}
		walk(u.PathwayName, map[string]bool{}, nil)
	}

	for _, p := range res.Paths {
		if len(p.Steps) == 1 {
			res.Direct = true
			break
		}
	}

	sort.Slice(res.Paths, func(i, j int) bool {
		a, b := res.Paths[i], res.Paths[j]
		if a.HasUnknown != b.HasUnknown {
			return !a.HasUnknown
		}
		if a.TotalETADays != b.TotalETADays {
			return a.TotalETADays < b.TotalETADays
		}
		return len(a.Steps) < len(b.Steps)
	})
	if len(res.Paths) > maxPaths {
		res.Paths = res.Paths[:maxPaths]
	}

	// No path at all → suggest the best inbound sources for the target so
	// the user can see which trackers could eventually lead there.
	if len(res.Paths) == 0 {
		inbound := append([]Route(nil), d.To(target)...)
		sort.Slice(inbound, func(i, j int) bool {
			di, dj := inbound[i].Days, inbound[j].Days
			if di < 0 {
				di = math.MaxInt32
			}
			if dj < 0 {
				dj = math.MaxInt32
			}
			return di < dj
		})
		for _, r := range inbound {
			if !r.Active {
				continue
			}
			res.Suggestions = append(res.Suggestions, Suggestion{
				Name: r.From, Days: r.Days, Reqs: r.Reqs, Updated: r.Updated,
			})
			if len(res.Suggestions) >= 5 {
				break
			}
		}
	}
	return res
}

// ReadyTargets returns every target for which the user currently meets ALL
// listed requirements on at least one active DIRECT route (first hop, live
// stats — the same evaluation the paths view runs). Community data: meeting
// the listed requirements never guarantees an invite.
func ReadyTargets(d *Data, users []UserTracker,
	groupsFor func(pathwayName string) []defs.GroupDef,
	inviteReqsFor func(pathwayName string) *defs.InviteReqs) map[string]bool {
	out := map[string]bool{}
	for _, u := range users {
		for _, r := range d.From(u.PathwayName) {
			if !r.Active || out[r.To] || r.To == u.PathwayName {
				continue
			}
			step := evalStep(r, u, true, d, groupsFor, inviteReqsFor)
			// Zero known ETA with nothing unknown ⇒ every requirement met
			// (unmet controllable stats set HasUnknown; unmet age sets ETADays).
			if step.ETADays == 0 && !step.HasUnknown {
				out[r.To] = true
			}
		}
	}
	return out
}

// DirectRoutesFrom evaluates every ACTIVE direct invite route leaving one of
// the user's trackers against live stats — the Tracker Detail page's
// "pathways from here" list. Routes to targets the user already has are
// skipped. Results are sorted: fully-met routes first, then by known ETA.
func DirectRoutesFrom(d *Data, u UserTracker, owned map[string]bool,
	groupsFor func(pathwayName string) []defs.GroupDef,
	inviteReqsFor func(pathwayName string) *defs.InviteReqs) []Step {
	var out []Step
	for _, r := range d.From(u.PathwayName) {
		if !r.Active || owned[r.To] {
			continue
		}
		out = append(out, evalStep(r, u, true, d, groupsFor, inviteReqsFor))
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		aMet := a.ETADays == 0 && !a.HasUnknown
		bMet := b.ETADays == 0 && !b.HasUnknown
		if aMet != bMet {
			return aMet
		}
		if a.HasUnknown != b.HasUnknown {
			return !a.HasUnknown
		}
		if a.ETADays != b.ETADays {
			return a.ETADays < b.ETADays
		}
		return a.To < b.To
	})
	return out
}

func buildPath(u UserTracker, steps []Step) Path {
	p := Path{StartTrackerID: u.TrackerID, StartName: u.PathwayName, Steps: steps}
	for _, s := range steps {
		p.TotalETADays += s.ETADays // known component always accumulates
		if s.HasUnknown {
			p.HasUnknown = true
		}
	}
	return p
}

// ─────────────────────────────────────────────────────────────────────────────
// Step evaluation
// ─────────────────────────────────────────────────────────────────────────────

const plusNote = `"+" means this class or higher — official invites can carry extra requirements beyond the class itself`

func evalStep(r Route, u UserTracker, firstHop bool, d *Data,
	groupsFor func(string) []defs.GroupDef,
	inviteReqsFor func(string) *defs.InviteReqs) Step {
	step := Step{
		From: r.From, To: r.To, Days: r.Days, ReqsRaw: r.Reqs,
		Updated: r.Updated, Estimated: !firstHop,
	}
	reqs := ParseReqs(r.Reqs)

	// Def-level invite requirements of the FROM tracker (e.g. MAM's
	// class-independent "Power User + 1 TB + 2.0 ratio + 6 months") AUGMENT
	// the community data. A community "None" would contradict them — drop it.
	if ir := inviteReqsFor(r.From); ir != nil {
		if extra := inviteReqTokens(ir); len(extra) > 0 {
			kept := reqs[:0]
			for _, q := range reqs {
				if q.Kind != "none" {
					kept = append(kept, q)
				}
			}
			reqs = append(kept, extra...)
		}
		step.DefNote = ir.Note
	}

	if !firstHop {
		// Beyond the first hop there are no live stats: estimate from the
		// community data — account-age requirement plus, when a class is
		// required, the tracker's typical invite-unlock time.
		eta := float64(0)
		if r.Days > 0 {
			eta = float64(r.Days)
		}
		unknown := false
		for _, q := range reqs {
			switch q.Kind {
			case "age":
				eta = math.Max(eta, float64(q.Days))
				step.Reqs = append(step.Reqs, ReqProgress{Label: q.Raw, Kind: "age", ETADays: float64(q.Days)})
				continue
			case "class":
				// Class progress on a tracker the user isn't on yet can't be
				// measured — the typical invite-unlock time is a floor, and the
				// real class requirements may push it longer (the "+").
				if uc, ok := d.Unlocks[r.From]; ok && uc.Days > 0 {
					eta = math.Max(eta, float64(uc.Days))
				}
				unknown = true
				note := ""
				if q.Plus {
					note = plusNote
				}
				step.Reqs = append(step.Reqs, ReqProgress{Label: q.Raw, ETADays: -1, Note: note})
				continue
			case "none":
				step.Reqs = append(step.Reqs, ReqProgress{Label: q.Raw, Met: true})
				continue
			}
			// Any other requirement (upload, ratio, …) is controllable and
			// can't be checked on a tracker the user isn't on — floor only.
			unknown = true
			step.Reqs = append(step.Reqs, ReqProgress{Label: q.Raw, ETADays: -1})
		}
		step.ETADays = eta
		step.HasUnknown = unknown
		return step
	}

	// First hop — evaluate every requirement against live stats.
	// Classes are evaluated FIRST so route-level requirements that the class
	// itself already demands can be dropped as duplicates (e.g.
	// "Oceanus+, 1 year" where Oceanus requires 1 year of account age).
	var classRows, otherRows []ReqProgress
	ageNeed := 0.0
	if r.Days > 0 {
		ageNeed = float64(r.Days) // the route's own account-age floor
	}

	for _, q := range reqs {
		switch q.Kind {
		case "none":
			otherRows = append(otherRows, ReqProgress{Label: "No requirement", Met: true})
		case "age":
			ageNeed = math.Max(ageNeed, float64(q.Days))
		case "class":
			classRows = append(classRows, classProgress(q, u, groupsFor))
		case "ratio", "uploaded", "downloaded", "seed_size", "uploads", "adoptions", "bonus", "seedtime":
			if classCovers(classRows, q.Kind, q.Value) {
				continue // every required class alternative already demands this
			}
			otherRows = append(otherRows, statProgress(q, u))
		default: // unknown token — preserve verbatim
			otherRows = append(otherRows, ReqProgress{Label: q.Raw, ETADays: -1})
		}
	}
	// Note: stat tokens can precede class tokens in the text, so re-check
	// coverage after all classes are known.
	if len(classRows) > 0 {
		filtered := otherRows[:0]
		for _, row := range otherRows {
			if row.Kind != "" && row.Kind != "age" && classCovers(classRows, row.Kind, row.Need) {
				continue
			}
			filtered = append(filtered, row)
		}
		otherRows = filtered
	}
	if ageNeed > 0 && !classCovers(classRows, "age", ageNeed) {
		otherRows = append(otherRows, ageProgress(ageNeed, u.Stats.AgeDays))
	}

	step.Reqs = append(classRows, otherRows...)

	// Step ETA is the account-age minimum only. Class rows already carry an
	// age-only ETA (see evalGroupReqs); standalone age rows contribute their
	// exact remaining time. Every other unmet requirement is controllable —
	// it sets the "+" floor flag but never the numeric minimum.
	var eta float64
	unknown := false
	for _, p := range step.Reqs {
		if p.Met {
			continue
		}
		isAge := p.Kind == "age"
		isClass := len(p.Classes) > 0
		if isAge || isClass {
			if p.ETADays > 0 {
				eta = math.Max(eta, p.ETADays)
			}
			if p.ETADays < 0 || p.HasUnknown {
				unknown = true
			}
		} else {
			unknown = true // controllable / unprojectable stat → floor only
		}
	}
	step.ETADays = eta
	step.HasUnknown = unknown
	return step
}

// fmtStat formats a stat value for the progress bar labels.
func fmtStat(kind string, v float64) string {
	if v < 0 {
		return "?"
	}
	switch kind {
	case "uploaded", "downloaded", "seed_size":
		return parse.BytesToSize(int64(v * 1024 * 1024 * 1024))
	case "ratio":
		return strconv.FormatFloat(v, 'f', 2, 64)
	case "seedtime":
		return fmtDays(v / 86400)
	case "age":
		return fmtDays(v)
	default: // uploads, bonus
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
}

func ageProgress(needDays, haveDays float64) ReqProgress {
	p := ReqProgress{
		Label: "Account age " + fmtDays(needDays),
		Kind:  "age", Have: haveDays, Need: needDays,
		HaveText: fmtStat("age", haveDays), NeedText: fmtStat("age", needDays),
	}
	if haveDays < 0 {
		p.ETADays = -1
		p.Note = "join date unknown"
		return p
	}
	if haveDays >= needDays {
		p.Met = true
		return p
	}
	p.ETADays = needDays - haveDays
	return p
}

func statProgress(q Req, u UserTracker) ReqProgress {
	have, rate := -1.0, 0.0
	switch q.Kind {
	case "ratio":
		have = u.Stats.Ratio
	case "uploaded":
		have, rate = u.Stats.UploadedGiB, u.Rates.UploadGiB
	case "downloaded":
		have, rate = u.Stats.DownloadedGiB, u.Rates.DownloadGiB
	case "seed_size":
		have, rate = u.Stats.SeedSizeGiB, u.Rates.SeedSizeGiB
	case "uploads":
		have = u.Stats.Uploads
	case "adoptions":
		have = u.Stats.Adoptions
	case "bonus":
		have, rate = u.Stats.BonusPoints, u.Rates.Bonus
	case "seedtime":
		have = u.Stats.AvgSeedSec
	}
	p := ReqProgress{
		Label: q.Raw,
		Kind:  q.Kind, Have: have, Need: q.Value,
		HaveText: fmtStat(q.Kind, have), NeedText: fmtStat(q.Kind, q.Value),
	}
	if have < 0 {
		p.ETADays = -1
		p.Note = "no data for this stat yet"
		return p
	}
	if have >= q.Value {
		p.Met = true
		return p
	}
	switch q.Kind {
	case "seedtime":
		// Average seedtime grows at most ~1 day per day if everything keeps
		// seeding — a rough optimistic floor.
		p.ETADays = (q.Value - have) / 86400
		p.Note = "approximate — assumes everything keeps seeding"
	case "ratio", "uploads", "adoptions":
		p.ETADays = -1 // progress is visible on the bar; no rate to project a date
	default:
		if rate > 0.01 {
			p.ETADays = (q.Value - have) / rate
			p.Note = "at your recent daily rate"
		} else {
			p.ETADays = -1
			p.Note = "no recent growth to project from"
		}
	}
	return p
}

// classProgress evaluates "reach class X (or Y…)" against the source
// tracker's def groups. Every listed alternative gets a full requirement
// breakdown (Classes) so the UI can render progress bars; the fastest
// estimable alternative drives the ETA.
func classProgress(q Req, u UserTracker, groupsFor func(string) []defs.GroupDef) ReqProgress {
	p := ReqProgress{Label: q.Raw}
	if q.Plus {
		p.Note = plusNote
	}
	groups := groupsFor(u.PathwayName)
	if len(groups) == 0 {
		p.ETADays = -1
		return p
	}
	for _, want := range q.Classes {
		for _, g := range groups {
			if strings.EqualFold(g.Name, want) {
				p.Classes = append(p.Classes, evalClass(g, u))
			}
		}
	}
	if len(p.Classes) == 0 {
		p.ETADays = -1 // class names not in this tracker's def — truly unknown
		return p
	}
	// Fastest fully-estimable alternative wins; if none, the cheapest FLOOR
	// across alternatives is still a meaningful lower bound (e.g. "Titan in
	// 2 years, 1.9y of account age left" → 1.9y+ even when other stats
	// can't be projected).
	best, floor := math.Inf(1), math.Inf(1)
	bestIdx := -1
	for i, ce := range p.Classes {
		if !ce.HasUnknown && ce.ETADays < best {
			best = ce.ETADays
			bestIdx = i
		}
		if ce.ETADays < floor {
			floor = ce.ETADays
		}
	}
	if bestIdx >= 0 {
		if len(p.Classes) > 1 {
			p.Classes[bestIdx].Fastest = true
		}
		if best <= 0 {
			p.Met = true
			return p
		}
		p.ETADays = best
		return p
	}
	p.ETADays = floor
	p.HasUnknown = true
	return p
}

// evalClass builds the full requirement breakdown for one group:
// base requirements (all must be met) plus any_of alternatives (one must).
func evalClass(g defs.GroupDef, u UserTracker) ClassEval {
	rows, eta, unknown := evalGroupReqs(g.Requirements, u)
	ce := ClassEval{Name: g.Name, Reqs: rows}
	if len(g.Requirements.AnyOf) > 0 {
		best, floorMin := math.Inf(1), math.Inf(1)
		altKnown := false
		for _, alt := range g.Requirements.AnyOf {
			aRows, aEta, aUnk := evalGroupReqs(alt, u)
			ce.AnyOf = append(ce.AnyOf, aRows)
			if !aUnk && aEta < best {
				best = aEta
				altKnown = true
			}
			if aEta < floorMin {
				floorMin = aEta // aEta is already the known floor of that alt
			}
		}
		if altKnown {
			eta = math.Max(eta, best)
		} else {
			eta = math.Max(eta, floorMin)
			unknown = true
		}
	}
	ce.ETADays = eta
	ce.HasUnknown = unknown
	ce.Met = !unknown && eta == 0
	return ce
}

// evalGroupReqs evaluates the flat (non-any_of) fields of a requirement set,
// returning the display rows plus the combined ETA / unknown flag.
func evalGroupReqs(req defs.GroupRequirements, u UserTracker) ([]ReqProgress, float64, bool) {
	if req.Description != "" {
		return []ReqProgress{{Label: req.Description, ETADays: -1, Note: "not stat-based"}}, 0, true
	}
	var rows []ReqProgress
	var eta float64
	unknown := false
	// The numeric ETA is driven ONLY by account age — the one stat a user
	// cannot speed up by working harder. Every OTHER unmet requirement
	// (upload, seed size, ratio, …) is controllable: it never extends the
	// numeric minimum, it only flags that the real time may be longer (the
	// "+"). Each requirement still carries its own per-stat ETADays for the
	// progress-bar chip inside; that is display-only and never aggregated.
	add := func(p ReqProgress) {
		rows = append(rows, p)
		if p.Met {
			return
		}
		if p.Kind == "age" && p.ETADays >= 0 {
			if p.ETADays > eta {
				eta = p.ETADays
			}
		} else {
			unknown = true // controllable or unprojectable → floor only
		}
	}
	if req.MinUploaded != "" {
		add(statProgress(Req{Kind: "uploaded", Value: parseSizeGiB(req.MinUploaded), Raw: "Upload " + req.MinUploaded}, u))
	}
	if req.MinDownloaded != "" {
		add(statProgress(Req{Kind: "downloaded", Value: parseSizeGiB(req.MinDownloaded), Raw: "Download " + req.MinDownloaded}, u))
	}
	if req.MinSeedSize != "" {
		add(statProgress(Req{Kind: "seed_size", Value: parseSizeGiB(req.MinSeedSize), Raw: "Seed size " + req.MinSeedSize}, u))
	}
	if req.MinRatio > 0 {
		add(statProgress(Req{Kind: "ratio", Value: req.MinRatio, Raw: "Ratio ≥ " + strconv.FormatFloat(req.MinRatio, 'f', -1, 64)}, u))
	}
	if req.MinSeedtime != "" {
		if days, k := ParseDurationDays(req.MinSeedtime); k {
			add(statProgress(Req{Kind: "seedtime", Value: days * 86400, Raw: "Avg seedtime " + req.MinSeedtime}, u))
		}
	}
	if req.MinUploads > 0 {
		add(statProgress(Req{Kind: "uploads", Value: float64(req.MinUploads), Raw: "Uploads: " + strconv.Itoa(req.MinUploads)}, u))
	}
	if req.MinAdoptions > 0 {
		add(statProgress(Req{Kind: "adoptions", Value: float64(req.MinAdoptions), Raw: "Adoptions: " + strconv.Itoa(req.MinAdoptions)}, u))
	}
	if req.MinBonusPoints > 0 {
		add(statProgress(Req{Kind: "bonus", Value: float64(req.MinBonusPoints), Raw: "Bonus points: " + strconv.Itoa(req.MinBonusPoints)}, u))
	}
	if req.MinAge != "" {
		if days, k := ParseDurationDays(req.MinAge); k {
			add(ageProgress(days, u.Stats.AgeDays))
		}
	}
	return rows, eta, unknown
}

// inviteReqTokens converts a def's invite requirements into the same Req
// tokens the community-text parser produces, so both evaluate through one
// code path (live stats on the first hop, estimate floors on later hops).
func inviteReqTokens(ir *defs.InviteReqs) []Req {
	var out []Req
	if ir.MinClass != "" {
		out = append(out, Req{Kind: "class", Classes: []string{ir.MinClass}, Raw: ir.MinClass})
	}
	if ir.MinUploaded != "" {
		out = append(out, Req{Kind: "uploaded", Value: parseSizeGiB(ir.MinUploaded), Raw: "Upload " + ir.MinUploaded})
	}
	if ir.MinDownloaded != "" {
		out = append(out, Req{Kind: "downloaded", Value: parseSizeGiB(ir.MinDownloaded), Raw: "Download " + ir.MinDownloaded})
	}
	if ir.MinRatio > 0 {
		out = append(out, Req{Kind: "ratio", Value: ir.MinRatio, Raw: "Ratio ≥ " + strconv.FormatFloat(ir.MinRatio, 'f', -1, 64)})
	}
	if ir.MinSeedSize != "" {
		out = append(out, Req{Kind: "seed_size", Value: parseSizeGiB(ir.MinSeedSize), Raw: "Seed size " + ir.MinSeedSize})
	}
	if ir.MinSeedtime != "" {
		if days, ok := ParseDurationDays(ir.MinSeedtime); ok {
			out = append(out, Req{Kind: "seedtime", Value: days * 86400, Raw: "Avg seedtime " + ir.MinSeedtime})
		}
	}
	if ir.MinUploads > 0 {
		out = append(out, Req{Kind: "uploads", Value: float64(ir.MinUploads), Raw: "Uploads: " + strconv.Itoa(ir.MinUploads)})
	}
	if ir.MinAdoptions > 0 {
		out = append(out, Req{Kind: "adoptions", Value: float64(ir.MinAdoptions), Raw: "Adoptions: " + strconv.Itoa(ir.MinAdoptions)})
	}
	if ir.MinBonusPoints > 0 {
		out = append(out, Req{Kind: "bonus", Value: float64(ir.MinBonusPoints), Raw: "Bonus points: " + strconv.Itoa(ir.MinBonusPoints)})
	}
	if ir.MinAge != "" {
		if days, ok := ParseDurationDays(ir.MinAge); ok {
			out = append(out, Req{Kind: "age", Days: int(days), Raw: "Account age " + ir.MinAge})
		}
	}
	return out
}

// classCovers reports whether EVERY evaluated class alternative already
// demands at least `need` of the given stat kind — in that case a separate
// route-level requirement of the same kind is redundant and can be dropped
// (e.g. "Oceanus+, 1 year" where Oceanus itself requires 1 year of account
// age). One alternative lacking the requirement means the standalone row
// still carries information, so it stays.
func classCovers(classRows []ReqProgress, kind string, need float64) bool {
	any := false
	for _, c := range classRows {
		for _, ce := range c.Classes {
			any = true
			found := false
			for _, r := range ce.Reqs {
				if r.Kind == kind && r.Need >= need {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return any
}

// parseSizeGiB converts "6 TiB" / "500 GiB" style strings to GiB.
func parseSizeGiB(s string) float64 {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return math.Inf(1) // unparseable → never met → unknown via rate check
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	return sizeGiB(n, m[2])
}

// fmtDays renders a day count as a compact duration. Over a year it includes
// the residual months so 18 months reads "1y 6mo" — NOT "2y" (the old
// whole-year rounding turned 1.5 years into 2).
func fmtDays(d float64) string {
	days := int(d + 0.5)
	if days >= 365 {
		y := days / 365
		mo := int(float64(days-y*365)/30.44 + 0.5)
		if mo >= 12 {
			return strconv.Itoa(y+1) + "y"
		}
		if mo > 0 {
			return strconv.Itoa(y) + "y " + strconv.Itoa(mo) + "mo"
		}
		return strconv.Itoa(y) + "y"
	}
	if days >= 30 {
		return strconv.Itoa(int(float64(days)/30.44+0.5)) + "mo"
	}
	return strconv.Itoa(days) + "d"
}
