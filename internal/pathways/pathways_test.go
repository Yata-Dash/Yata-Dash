package pathways

import (
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
)

func TestParseDurationDays(t *testing.T) {
	cases := []struct {
		in   string
		want float64 // days; 0 means ok==false expected
	}{
		// Unit3D single-letter form (tracker defs) — uppercase M = month.
		{"8M", 8 * 30.44},
		{"1M 2W 1D", 30.44 + 14 + 1},
		{"1Y 3M", 365 + 3*30.44},
		{"1W 3D", 7 + 3},
		{"2Y", 2 * 365},
		// Community word form (trackerpathways data).
		{"8 months", 8 * 30.44},
		{"1 year", 365},
		{"1 month 2 weeks 1 day", 30.44 + 14 + 1},
		{"6 months", 6 * 30.44},
		// Plain day count.
		{"45", 45},
		// Garbage.
		{"forever", 0},
	}
	for _, tc := range cases {
		got, ok := ParseDurationDays(tc.in)
		if tc.want == 0 {
			if ok {
				t.Errorf("%q: expected ok=false, got %v", tc.in, got)
			}
			continue
		}
		if !ok {
			t.Errorf("%q: expected ok=true", tc.in)
			continue
		}
		if got < tc.want-0.5 || got > tc.want+0.5 {
			t.Errorf("%q: got %.2f days, want %.2f", tc.in, got, tc.want)
		}
	}
}

func TestFmtDays(t *testing.T) {
	cases := []struct {
		days float64
		want string
	}{
		{548, "1y 6mo"}, // 18 months — must NOT round to "2y"
		{540, "1y 6mo"},
		{365, "1y"},
		{730, "2y"},
		{183, "6mo"},
		{91, "3mo"},
		{15, "15d"},
		{700, "1y 11mo"},
	}
	for _, tc := range cases {
		if got := fmtDays(tc.days); got != tc.want {
			t.Errorf("fmtDays(%v) = %q, want %q", tc.days, got, tc.want)
		}
	}
}

func TestParseReqs(t *testing.T) {
	cases := []struct {
		in   string
		kind []string
	}{
		{"No requirement", []string{"none"}},
		{"Unknown", []string{"unknown"}},
		{"6 months", []string{"age"}},
		{"Prometheus+", []string{"class"}},
		{"Leviathan or Ship, 12 months", []string{"class", "age"}},
		{"Prometheus+, 1 year, ratio>=1", []string{"class", "age", "ratio"}},
		{"Superfan+, 6 months", []string{"class", "age"}},
	}
	for _, tc := range cases {
		got := ParseReqs(tc.in)
		if len(got) != len(tc.kind) {
			t.Errorf("%q: got %d tokens, want %d (%+v)", tc.in, len(got), len(tc.kind), got)
			continue
		}
		for i, k := range tc.kind {
			if got[i].Kind != k {
				t.Errorf("%q token %d: kind %s, want %s", tc.in, i, got[i].Kind, k)
			}
		}
	}

	// Class alternatives + plus handling ("Titan+" → class "Titan", or-higher note).
	q := ParseReqs("Titan+")[0]
	if q.Kind != "class" || len(q.Classes) != 1 || q.Classes[0] != "Titan" || !q.Plus {
		t.Errorf("Titan+: %+v", q)
	}
	alts := ParseReqs("Leviathan or Ship, 12 months")[0]
	if len(alts.Classes) != 2 || alts.Classes[0] != "Leviathan" || alts.Classes[1] != "Ship" {
		t.Errorf("alternatives: %+v", alts)
	}
	age := ParseReqs("Leviathan or Ship, 12 months")[1]
	if age.Days < 360 || age.Days > 370 {
		t.Errorf("12 months → %d days", age.Days)
	}
}

// testData builds a small synthetic dataset:
//
//	Home → Target            (direct, 180d + class "Power")
//	Home → Mid → Target      (two hops)
//	Island → Target          (route exists, but the user isn't on Island)
func testData() *Data {
	d := &Data{
		Source: SourceInfo{Name: "test"},
		Routes: []Route{
			{From: "Home", To: "Target", Days: 180, Reqs: "Power+, 6 months", Active: true},
			{From: "Home", To: "Mid", Days: 0, Reqs: "No requirement", Active: true},
			{From: "Mid", To: "Target", Days: 90, Reqs: "3 months", Active: true},
			{From: "Island", To: "Target", Days: 30, Reqs: "1 month", Active: true},
			{From: "Home", To: "Dead", Days: 0, Reqs: "", Active: false},
		},
		Unlocks: map[string]UnlockClass{
			"Mid": {Days: 60, Text: "Elite: 2 months"},
		},
	}
	d.index()
	return d
}

func testGroups(name string) []defs.GroupDef {
	if name != "Home" {
		return nil
	}
	return []defs.GroupDef{{
		Name: "Power",
		Requirements: defs.GroupRequirements{
			MinUploaded: "1 TiB",
			MinRatio:    1.0,
		},
	}}
}

func TestFindPathsDirectAndRanked(t *testing.T) {
	d := testData()
	user := UserTracker{
		TrackerID: "t1", PathwayName: "Home",
		Stats: Stats{AgeDays: 200, UploadedGiB: 2048, Ratio: 2.0, SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, BonusPoints: -1},
		Rates: Rates{UploadGiB: 10},
	}
	res := FindPaths(d, []UserTracker{user}, "Target", testGroups, noInviteReqs)

	if !res.Direct {
		t.Fatal("expected a direct path")
	}
	if len(res.Paths) < 2 {
		t.Fatalf("expected direct + via-Mid paths, got %d", len(res.Paths))
	}
	// Direct path: age 200 ≥ 180, class Power met (2 TiB > 1 TiB, ratio 2 ≥ 1)
	// → ETA 0, ranks first.
	first := res.Paths[0]
	if len(first.Steps) != 1 || first.Steps[0].To != "Target" {
		t.Fatalf("first path should be direct: %+v", first)
	}
	if first.TotalETADays != 0 || first.HasUnknown {
		t.Errorf("direct path should be fully met: eta=%v unknown=%v reqs=%+v",
			first.TotalETADays, first.HasUnknown, first.Steps[0].Reqs)
	}
	// Multi-hop path exists and is marked estimated beyond hop 1.
	var multi *Path
	for i := range res.Paths {
		if len(res.Paths[i].Steps) == 2 {
			multi = &res.Paths[i]
		}
	}
	if multi == nil {
		t.Fatal("expected a 2-hop path via Mid")
	}
	if !multi.Steps[1].Estimated {
		t.Error("second hop should be marked estimated")
	}
	if multi.Steps[1].ETADays < 90 {
		t.Errorf("second hop ETA should respect the 90-day route floor, got %v", multi.Steps[1].ETADays)
	}
}

// TestReadyTargets: only targets whose direct-route requirements are ALL met
// against live stats are flagged; inactive routes and multi-hop-only targets
// never are.
func TestReadyTargets(t *testing.T) {
	d := testData()
	veteran := UserTracker{
		TrackerID: "t1", PathwayName: "Home",
		Stats: Stats{AgeDays: 200, UploadedGiB: 2048, Ratio: 2.0, SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, BonusPoints: -1},
	}
	ready := ReadyTargets(d, []UserTracker{veteran}, testGroups, noInviteReqs)
	if !ready["Target"] {
		t.Error("veteran meets 180d + Power on the direct route — Target should be ready")
	}
	if !ready["Mid"] {
		t.Error("Home → Mid has no requirements — Mid should be ready")
	}
	if ready["Dead"] {
		t.Error("inactive routes must never mark a target ready")
	}

	// A young account meets nothing time-gated: Target drops out, the
	// no-requirement route stays.
	young := veteran
	young.Stats.AgeDays = 30
	young.Stats.UploadedGiB = 100
	ready = ReadyTargets(d, []UserTracker{young}, testGroups, noInviteReqs)
	if ready["Target"] {
		t.Error("young account (30d < 180d, upload unmet) must not be ready for Target")
	}
	if !ready["Mid"] {
		t.Error("no-requirement route should stay ready regardless of stats")
	}
}

// TestDirectRoutesFrom: only active routes leave the list, owned targets are
// skipped, met routes sort first.
func TestDirectRoutesFrom(t *testing.T) {
	d := testData()
	u := UserTracker{
		TrackerID: "t1", PathwayName: "Home",
		Stats: Stats{AgeDays: 200, UploadedGiB: 2048, Ratio: 2.0, SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, BonusPoints: -1},
	}
	routes := DirectRoutesFrom(d, u, map[string]bool{"Home": true}, testGroups, noInviteReqs)
	// Home → Target (met), Home → Mid (no reqs, met); Dead is inactive.
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2 (Dead is inactive): %+v", len(routes), routes)
	}
	for _, s := range routes {
		if s.To == "Dead" {
			t.Error("inactive route must be excluded")
		}
		if !(s.ETADays == 0 && !s.HasUnknown) {
			t.Errorf("veteran should meet route to %s: eta=%v unknown=%v", s.To, s.ETADays, s.HasUnknown)
		}
	}
	// Owned targets are skipped.
	routes = DirectRoutesFrom(d, u, map[string]bool{"Home": true, "Mid": true}, testGroups, noInviteReqs)
	if len(routes) != 1 || routes[0].To != "Target" {
		t.Fatalf("owned Mid should be skipped: %+v", routes)
	}
}

func TestFindPathsClassETA(t *testing.T) {
	d := testData()
	// Young account, upload not yet met. The ETA is driven ONLY by account
	// age ("6 months" = 183d − 30d = 153d). The unmet upload is controllable,
	// so it does NOT extend the number — it only sets the "+" floor flag.
	user := UserTracker{
		TrackerID: "t1", PathwayName: "Home",
		Stats: Stats{AgeDays: 30, UploadedGiB: 100, Ratio: 1.5, SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, BonusPoints: -1},
		Rates: Rates{UploadGiB: 10},
	}
	res := FindPaths(d, []UserTracker{user}, "Target", testGroups, noInviteReqs)
	var direct *Path
	for i := range res.Paths {
		if len(res.Paths[i].Steps) == 1 {
			direct = &res.Paths[i]
		}
	}
	if direct == nil {
		t.Fatal("no direct path")
	}
	// Upload is unmet → controllable → floor flag set, but the number stays
	// the age minimum.
	if !direct.HasUnknown {
		t.Errorf("unmet upload should set the floor flag (+): %+v", direct.Steps[0].Reqs)
	}
	if direct.TotalETADays < 152 || direct.TotalETADays > 154 {
		t.Errorf("expected ~153d age-only ETA (upload must NOT inflate it), got %v", direct.TotalETADays)
	}
}

// TestETAIgnoresTrendProjection is the explicit regression for the user's
// "18Y" bug: a wildly slow upload projection must NOT drive the step ETA —
// only the account-age requirement does, with "+" signalling the rest.
func TestETAIgnoresTrendProjection(t *testing.T) {
	d := &Data{
		Source: SourceInfo{Name: "test"},
		Routes: []Route{{From: "Home", To: "Target", Days: 0, Reqs: "Oceanus+", Active: true}},
	}
	d.index()
	groups := func(name string) []defs.GroupDef {
		if name != "Home" {
			return nil
		}
		return []defs.GroupDef{{
			Name: "Oceanus",
			Requirements: defs.GroupRequirements{
				MinUploaded: "20 TiB", // huge; at the slow rate this projects to ~17 years
				MinRatio:    1.5,
				MinAge:      "1Y",
			},
		}}
	}
	user := UserTracker{
		TrackerID: "t1", PathwayName: "Home",
		Stats: Stats{AgeDays: 24, UploadedGiB: 148, Ratio: 0.74, SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, BonusPoints: -1},
		Rates: Rates{UploadGiB: 3.3}, // ~17y to reach 20 TiB — must be ignored for the ETA
	}
	res := FindPaths(d, []UserTracker{user}, "Target", groups, noInviteReqs)
	if len(res.Paths) != 1 {
		t.Fatalf("paths: %d", len(res.Paths))
	}
	p := res.Paths[0]
	// 1Y − 24d ≈ 341d, NOT ~17 years. "+" because upload/ratio remain.
	if p.TotalETADays < 335 || p.TotalETADays > 345 {
		t.Errorf("ETA must be the ~341d age minimum, not the 17y upload projection — got %v", p.TotalETADays)
	}
	if !p.HasUnknown {
		t.Error("unmet upload/ratio should set the + floor flag")
	}
	// The upload row itself still carries its trend projection for the bar.
	cls := p.Steps[0].Reqs[0]
	var upRow *ReqProgress
	for i := range cls.Classes[0].Reqs {
		if cls.Classes[0].Reqs[i].Kind == "uploaded" {
			upRow = &cls.Classes[0].Reqs[i]
		}
	}
	if upRow == nil || upRow.ETADays < 365*10 {
		t.Errorf("the upload row should keep its own (large) trend ETA for display: %+v", upRow)
	}
}

// TestClassBreakdownAndDedupe: the first hop returns full per-class
// requirement breakdowns with have/need progress data, and route-level
// requirements duplicated by the class itself are dropped (the
// "Oceanus+, 1 year" case where Oceanus already requires 1 year).
func TestClassBreakdownAndDedupe(t *testing.T) {
	d := &Data{
		Source: SourceInfo{Name: "test"},
		Routes: []Route{
			{From: "Home", To: "Target", Days: 365, Reqs: "Oceanus+, 1 year", Active: true},
		},
	}
	d.index()
	groups := func(name string) []defs.GroupDef {
		return []defs.GroupDef{{
			Name: "Oceanus",
			Requirements: defs.GroupRequirements{
				MinUploaded: "10 TiB",
				MinRatio:    1.0,
				MinAge:      "1Y",
				AnyOf: []defs.GroupRequirements{
					{MinSeedSize: "8 TiB"},
					{MinUploaded: "20 TiB"},
				},
			},
		}}
	}
	user := UserTracker{
		TrackerID: "t1", PathwayName: "Home",
		Stats: Stats{AgeDays: 100, UploadedGiB: 5 * 1024, Ratio: 1.5, SeedSizeGiB: 2 * 1024, AvgSeedSec: -1, Uploads: -1, BonusPoints: -1},
		Rates: Rates{UploadGiB: 20, SeedSizeGiB: 10},
	}
	res := FindPaths(d, []UserTracker{user}, "Target", groups, noInviteReqs)
	if len(res.Paths) != 1 {
		t.Fatalf("paths: %d", len(res.Paths))
	}
	step := res.Paths[0].Steps[0]

	// Exactly one top-level requirement: the class. The standalone
	// "1 year" age row must be deduped (Oceanus itself requires 1Y).
	if len(step.Reqs) != 1 {
		t.Fatalf("expected 1 top-level req (class only, age deduped), got %d: %+v", len(step.Reqs), step.Reqs)
	}
	cls := step.Reqs[0]
	if len(cls.Classes) != 1 || cls.Classes[0].Name != "Oceanus" {
		t.Fatalf("class breakdown missing: %+v", cls)
	}
	ce := cls.Classes[0]
	// Base rows: uploaded, ratio, age — each with quantitative have/need.
	if len(ce.Reqs) != 3 {
		t.Fatalf("expected 3 base rows, got %d: %+v", len(ce.Reqs), ce.Reqs)
	}
	for _, row := range ce.Reqs {
		if row.Kind == "" || row.Need <= 0 || row.NeedText == "" || row.HaveText == "" {
			t.Errorf("row missing quantitative data: %+v", row)
		}
	}
	// any_of alternatives present (one must be met).
	if len(ce.AnyOf) != 2 {
		t.Fatalf("expected 2 any_of alternatives, got %d", len(ce.AnyOf))
	}
	// ETA is the account-age minimum ONLY: 1Y − 100d = 265d. The unmet
	// upload (base) and the unmet seed-size/upload any_of are controllable —
	// they set the "+" floor flag but must NOT inflate the number (the old
	// behaviour reported ~614d from the seed-size projection).
	if ce.ETADays < 262 || ce.ETADays > 268 {
		t.Errorf("class ETA should be the ~265d age minimum, got %v", ce.ETADays)
	}
	if !ce.HasUnknown {
		t.Error("unmet upload + any_of should set the + floor flag")
	}
	if cls.ETADays != ce.ETADays {
		t.Errorf("class row ETA should equal the single class eval: %v vs %v", cls.ETADays, ce.ETADays)
	}
}

// TestKnownFloorPropagates: when some requirements can't be projected but
// account age can, the path total must still carry the known floor (the
// "Titan in 2 years, 1.9y left" case) and accumulate across hops — never
// report just the second hop's 3 months.
func TestKnownFloorPropagates(t *testing.T) {
	d := &Data{
		Source: SourceInfo{Name: "test"},
		Routes: []Route{
			{From: "Home", To: "Mid", Days: 0, Reqs: "Titan+", Active: true},
			{From: "Mid", To: "Target", Days: 90, Reqs: "3 months", Active: true},
		},
	}
	d.index()
	groups := func(name string) []defs.GroupDef {
		if name != "Home" {
			return nil
		}
		return []defs.GroupDef{{
			Name: "Titan",
			Requirements: defs.GroupRequirements{
				MinAge:     "2Y",
				MinUploads: 20, // not projectable → unknown, but age floor remains
			},
		}}
	}
	user := UserTracker{
		TrackerID: "t1", PathwayName: "Home",
		Stats: Stats{AgeDays: 36, UploadedGiB: -1, Ratio: -1, SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: 5, BonusPoints: -1},
	}
	res := FindPaths(d, []UserTracker{user}, "Target", groups, noInviteReqs)
	if len(res.Paths) != 1 {
		t.Fatalf("paths: %d", len(res.Paths))
	}
	p := res.Paths[0]
	if !p.HasUnknown {
		t.Error("uploads not projectable → path must be marked has_unknown (floor)")
	}
	// Hop 1 floor: 2Y (730d) − 36d ≈ 694d. Hop 2 estimate: 90d.
	// Total must accumulate: ≈ 784d — NOT 90d.
	if p.TotalETADays < 780 || p.TotalETADays > 790 {
		t.Errorf("total should accumulate hop floors: got %v, want ≈784", p.TotalETADays)
	}
	cls := p.Steps[0].Reqs[0]
	if !cls.HasUnknown || cls.ETADays < 690 || cls.ETADays > 700 {
		t.Errorf("class row should carry the age floor: %+v", cls)
	}
}

// TestMinMonthlyUploadsIgnoredInGroupEval proves min_monthly_uploads (no live
// stat exists for it yet — RocketHD/Aither-style uploader-class groundwork)
// is silently ignored by group requirement evaluation, exactly like
// MinCounts: it contributes no row, no ETA, and never blocks a group that is
// otherwise fully met.
func TestMinMonthlyUploadsIgnoredInGroupEval(t *testing.T) {
	req := defs.GroupRequirements{
		MinRatio:          1.5,
		MinMonthlyUploads: 10,
	}
	u := UserTracker{Stats: Stats{
		AgeDays: -1, UploadedGiB: -1, DownloadedGiB: -1, Ratio: 2.0,
		SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, Adoptions: -1, BonusPoints: -1,
	}}
	rows, eta, unknown := evalGroupReqs(req, u)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (only the ratio requirement — min_monthly_uploads must add nothing)", len(rows))
	}
	if !rows[0].Met {
		t.Errorf("ratio requirement should be met: %+v", rows[0])
	}
	if eta != 0 || unknown {
		t.Errorf("eta=%v unknown=%v — min_monthly_uploads must not block an otherwise fully-met group", eta, unknown)
	}
}

func TestCombinedTransferGroupRequirement(t *testing.T) {
	req := defs.GroupRequirements{MinTotalTransfer: "500 GiB"}
	u := UserTracker{Stats: Stats{
		AgeDays: -1, UploadedGiB: 400, DownloadedGiB: 100, Ratio: -1,
		SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, Adoptions: -1, BonusPoints: -1,
	}}
	rows, eta, unknown := evalGroupReqs(req, u)
	if len(rows) != 1 || !rows[0].Met || rows[0].Kind != "total_transfer" {
		t.Fatalf("combined transfer rows = %+v", rows)
	}
	if eta != 0 || unknown {
		t.Fatalf("eta=%v unknown=%v, want met", eta, unknown)
	}
}

func TestFindPathsNoRouteSuggestions(t *testing.T) {
	d := testData()
	// User only on a tracker with no outgoing routes at all.
	user := UserTracker{TrackerID: "t9", PathwayName: "Nowhere", Stats: Stats{AgeDays: -1}}
	res := FindPaths(d, []UserTracker{user}, "Target", func(string) []defs.GroupDef { return nil }, noInviteReqs)
	if res.Direct || len(res.Paths) != 0 {
		t.Fatalf("expected no paths: %+v", res.Paths)
	}
	if len(res.Suggestions) == 0 {
		t.Fatal("expected suggestions for trackers that can reach the target")
	}
	// Island (30d) should rank before Home (180d) and Mid (90d).
	if res.Suggestions[0].Name != "Island" {
		t.Errorf("suggestions should be ranked by days: %+v", res.Suggestions)
	}
}

// noInviteReqs is the default inviteReqsFor for tests (no def-level rules).
func noInviteReqs(string) *defs.InviteReqs { return nil }

// ─── "A or B and C" route requirements ───────────────────────────────────────

// TestParseAlternatives: the token grammar reads alternatives and conjuncts,
// and — critically — refuses to half-read the community data's prose.
func TestParseAlternatives(t *testing.T) {
	// Parsed as alternatives: kinds of each atom, per alternative.
	for _, tc := range []struct {
		in   string
		want [][]string
	}{
		{"9 months or 6 months and 10+ uploads", [][]string{{"age"}, {"age", "uploads"}}},
		{"9 months or 10+ uploads", [][]string{{"age"}, {"uploads"}}},
		{"3 years or 300 uploads", [][]string{{"age"}, {"uploads"}}},
		{"12 months or Elite+", [][]string{{"age"}, {"class"}}},
		{"Elite or 6 months", [][]string{{"class"}, {"age"}}},
		{"50 uploads or 75 adopted torrents + 5 uploads", [][]string{{"uploads"}, {"adoptions", "uploads"}}},
	} {
		got := ParseReqs(tc.in)
		if len(got) != 1 || got[0].Kind != "any_of" {
			t.Errorf("%q: expected one any_of token, got %+v", tc.in, got)
			continue
		}
		sets := got[0].AnyOf
		if len(sets) != len(tc.want) {
			t.Errorf("%q: %d alternatives, want %d", tc.in, len(sets), len(tc.want))
			continue
		}
		for i, want := range tc.want {
			if len(sets[i]) != len(want) {
				t.Errorf("%q alt %d: %+v, want kinds %v", tc.in, i, sets[i], want)
				continue
			}
			for j, k := range want {
				if sets[i][j].Kind != k {
					t.Errorf("%q alt %d atom %d: kind %q, want %q", tc.in, i, j, sets[i][j].Kind, k)
				}
			}
		}
	}

	// NOT alternatives — every one of these must keep its existing shape.
	for _, tc := range []struct {
		in   string
		kind string
		why  string
	}{
		{"Whale or Sailboat", "class", "class-only ORs stay a single class requirement (the def-ladder dedupe depends on it)"},
		{"Elite+ or VIP", "class", "class-only, with the + preserved"},
		{"BluArchivist or higher", "class", `"or higher" is the + modifier, not an alternative`},
		{"Profile link and screenshots", "class", "prose: 'screenshots' is not a class name"},
		{"3 profile links and screenshots", "unknown", "prose: no atom parses"},
		{"500GB download on MAM or 6 month account age", "unknown", "prose: the first clause doesn't parse"},
		{"2 years with either 250 movie uploads or 30 completed subtitle pots", "unknown", "prose"},
		{"90%+ seeding percentage OR 100GB seedsize", "unknown", "percent-seeding is not a stat Yata has"},
	} {
		got := ParseReqs(tc.in)
		if len(got) != 1 || got[0].Kind != tc.kind {
			t.Errorf("%q: got %+v, want a single %q token — %s", tc.in, got, tc.kind, tc.why)
		}
	}

	// "Prometheus+" keeps meaning "or higher" without an alternative.
	if q := ParseReqs("BluArchivist or higher")[0]; !q.Plus || len(q.Classes) != 1 || q.Classes[0] != "BluArchivist" {
		t.Errorf("or-higher: %+v", q)
	}
	// A token with conjuncts but no alternatives is just several
	// requirements written without a comma.
	if got := ParseReqs("Elite + 6 months"); len(got) != 2 || got[0].Kind != "class" || got[1].Kind != "age" {
		t.Errorf("pure conjunction should flatten: %+v", got)
	}
}

// TestAnyOfEvaluation: met when ANY alternative is fully met; the headline
// ETA is the fastest alternative that can be estimated end to end.
func TestAnyOfEvaluation(t *testing.T) {
	d := &Data{Source: SourceInfo{Name: "test"}, Routes: []Route{
		{From: "Seed", To: "Mam", Reqs: "9 months or 6 months and 10+ uploads", Active: true},
	}}
	d.index()
	base := Stats{AgeDays: 0, UploadedGiB: 100, Ratio: 2, SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, BonusPoints: -1}

	step := func(ageDays, uploads float64) Step {
		u := UserTracker{TrackerID: "t1", PathwayName: "Seed", Stats: base}
		u.Stats.AgeDays, u.Stats.Uploads = ageDays, uploads
		return DirectRoutesFrom(d, u, map[string]bool{}, ladderGroups, noInviteReqs)[0]
	}

	// 7 months + 12 uploads → the SECOND alternative is met in full.
	s := step(213, 12)
	if len(s.Reqs) != 1 || !s.Reqs[0].Met || s.Reqs[0].ETADays != 0 {
		t.Fatalf("second alternative met → requirement met: %+v", s.Reqs)
	}
	if len(s.Reqs[0].AnyOf) != 2 {
		t.Fatalf("both alternatives should still render: %+v", s.Reqs[0].AnyOf)
	}
	if s.ETADays != 0 || s.HasUnknown {
		t.Errorf("route should be fully met: eta=%v unknown=%v", s.ETADays, s.HasUnknown)
	}

	// 7 months, no uploads → alt 2 needs uploads (no ETA), alt 1 is 2 months
	// of pure account age away. The known age path drives the estimate.
	s = step(213, 0)
	q := s.Reqs[0]
	if q.Met {
		t.Fatal("nothing is met yet")
	}
	if want := 274.0 - 213.0; q.ETADays < want-1 || q.ETADays > want+1 {
		t.Errorf("ETA should be the remaining age on alternative 1 (%v), got %v", want, q.ETADays)
	}
	if q.HasUnknown {
		t.Error("alternative 1 is fully estimable, so the ETA is not a floor")
	}
	// The uploads row inside alternative 2 still shows its own progress.
	if r := q.AnyOf[1][1]; r.Kind != "uploads" || r.Met || r.Need != 10 {
		t.Errorf("alt 2 uploads row: %+v", r)
	}
}

// ─── def invite requirements merged into the community data ──────────────────

// inviteData is a tracker whose route texts state the invite-forum class in
// every possible relation to the def's own min_class: the same class, a
// higher one, a lower one, and not at all.
func inviteData() *Data {
	d := &Data{
		Source: SourceInfo{Name: "test"},
		Routes: []Route{
			{From: "Seed", To: "Same", Reqs: "Gold", Active: true},
			{From: "Seed", To: "Higher", Reqs: "Platinum+", Active: true},
			{From: "Seed", To: "Lower", Reqs: "Silver+", Active: true},
			{From: "Seed", To: "Either", Reqs: "Platinum or Bronze", Active: true},
			{From: "Seed", To: "Silent", Reqs: "No requirement", Active: true},
			{From: "Seed", To: "Stats", Reqs: "1 TiB upload", Active: true},
			{From: "Seed", To: "Aged", Reqs: "6 months", Active: true},
		},
	}
	d.index()
	return d
}

// ladderGroups is a four-rung ascending ladder (lowest first), no stat
// requirements — the class rows in these tests are about identity, not
// progress.
func ladderGroups(name string) []defs.GroupDef {
	if name != "Seed" {
		return nil
	}
	return []defs.GroupDef{{Name: "Bronze"}, {Name: "Silver"}, {Name: "Gold"}, {Name: "Platinum"}}
}

func inviteUser() UserTracker {
	return UserTracker{TrackerID: "t1", PathwayName: "Seed", Stats: Stats{
		AgeDays: 400, UploadedGiB: 2048, Ratio: 2, SeedSizeGiB: -1,
		AvgSeedSec: -1, Uploads: -1, BonusPoints: -1,
	}}
}

// stepsByTarget evaluates every direct route from the user's tracker.
func stepsByTarget(d *Data, ir func(string) *defs.InviteReqs) map[string]Step {
	out := map[string]Step{}
	for _, s := range DirectRoutesFrom(d, inviteUser(), map[string]bool{}, ladderGroups, ir) {
		out[s.To] = s
	}
	return out
}

func reqLabels(s Step) []string {
	var out []string
	for _, q := range s.Reqs {
		out = append(out, q.Label)
	}
	return out
}

// TestInviteReqsClassDedupe: a def min_class the route text already demands
// must not produce a second identical row (seedpool's "SuperPool" route text
// vs its SuperPool invite forum), while a class the route does NOT guarantee
// keeps its own row.
func TestInviteReqsClassDedupe(t *testing.T) {
	gold := func(name string) *defs.InviteReqs {
		if name != "Seed" {
			return nil
		}
		return &defs.InviteReqs{MinClass: "Gold"}
	}
	steps := stepsByTarget(inviteData(), gold)

	for _, tc := range []struct {
		to   string
		want []string
	}{
		{"Same", []string{"Gold"}},                         // identical → one row
		{"Higher", []string{"Platinum+"}},                  // above Gold on the ladder → implies it
		{"Lower", []string{"Silver+", "Gold"}},             // below Gold → both rows carry information
		{"Either", []string{"Platinum or Bronze", "Gold"}}, // one alternative is below Gold
		{"Silent", []string{"Gold"}},                       // "No requirement" contradicted → dropped
	} {
		got := reqLabels(steps[tc.to])
		if len(got) != len(tc.want) {
			t.Errorf("%s: got rows %q, want %q", tc.to, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got rows %q, want %q", tc.to, got, tc.want)
				break
			}
		}
	}
	if s := steps["Silent"]; s.ETADays != 0 || s.HasUnknown {
		t.Errorf("Silent: class met, no other reqs — expected a clean zero ETA, got %+v", s)
	}
}

// TestInviteReqsStatDedupe: a def stat threshold and a route stat threshold of
// the same kind collapse to ONE row at the stricter of the two.
func TestInviteReqsStatDedupe(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ir       defs.InviteReqs
		to       string
		wantKind string
		wantNeed float64
	}{
		{"def stricter", defs.InviteReqs{GroupRequirements: defs.GroupRequirements{MinUploaded: "5 TiB"}}, "Stats", "uploaded", 5 * 1024},
		{"route stricter", defs.InviteReqs{GroupRequirements: defs.GroupRequirements{MinUploaded: "500 GiB"}}, "Stats", "uploaded", 1024},
		{"age merged", defs.InviteReqs{GroupRequirements: defs.GroupRequirements{MinAge: "1Y"}}, "Aged", "age", 365},
	} {
		ir := tc.ir
		steps := stepsByTarget(inviteData(), func(name string) *defs.InviteReqs {
			if name != "Seed" {
				return nil
			}
			return &ir
		})
		var rows []ReqProgress
		for _, q := range steps[tc.to].Reqs {
			if q.Kind == tc.wantKind {
				rows = append(rows, q)
			}
		}
		if len(rows) != 1 {
			t.Errorf("%s: want exactly one %s row, got %+v", tc.name, tc.wantKind, rows)
			continue
		}
		if rows[0].Need != tc.wantNeed {
			t.Errorf("%s: want need %v, got %v (%q)", tc.name, tc.wantNeed, rows[0].Need, rows[0].Label)
		}
	}
}

// TestInviteReqsMinClassAnyOf: a def whose invite forum opens on ANY of
// several classes (LST's "Sailboat OR Whale" — parallel branches of the same
// ladder) is satisfied by a route that guarantees any one of them.
func TestInviteReqsMinClassAnyOf(t *testing.T) {
	d := &Data{Source: SourceInfo{Name: "test"}, Routes: []Route{
		{From: "Seed", To: "Both", Reqs: "Gold or Silver", Active: true},
		{From: "Seed", To: "One", Reqs: "Platinum", Active: true},
		{From: "Seed", To: "Mixed", Reqs: "Gold or Bronze", Active: true},
		{From: "Seed", To: "Silent", Reqs: "No requirement", Active: true},
	}}
	d.index()
	anyOf := func(name string) *defs.InviteReqs {
		if name != "Seed" {
			return nil
		}
		return &defs.InviteReqs{MinClassAnyOf: []string{"Silver", "Gold"}}
	}
	steps := stepsByTarget(d, anyOf)

	for _, tc := range []struct {
		to   string
		want []string
	}{
		{"Both", []string{"Gold or Silver"}},                    // exactly the def's pair
		{"One", []string{"Platinum"}},                           // outranks both alternatives
		{"Mixed", []string{"Gold or Bronze", "Silver or Gold"}}, // Bronze satisfies neither
		{"Silent", []string{"Silver or Gold"}},                  // def token stands alone
	} {
		got := reqLabels(steps[tc.to])
		if len(got) != len(tc.want) {
			t.Errorf("%s: got rows %q, want %q", tc.to, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got rows %q, want %q", tc.to, got, tc.want)
				break
			}
		}
	}
	// The def token evaluates as a real class row, not an opaque one.
	if rows := steps["Silent"].Reqs; len(rows) != 1 || len(rows[0].Classes) != 2 {
		t.Errorf("expected both alternatives evaluated: %+v", rows)
	}
}

// TestUnavailReasons: rows that can't be measured say WHY, so the UI can
// render them like an untrackable target instead of a bare "?".
func TestUnavailReasons(t *testing.T) {
	d := &Data{Source: SourceInfo{Name: "test"}, Routes: []Route{
		{From: "Seed", To: "Text", Reqs: "2 more proofs", Active: true},
		{From: "Seed", To: "Stat", Reqs: "5 uploads", Active: true},
		{From: "Seed", To: "Class", Reqs: "Mystery", Active: true},
	}}
	d.index()
	steps := stepsByTarget(d, noInviteReqs)

	want := map[string]string{"Text": "text", "Stat": "stat", "Class": "class"}
	for to, reason := range want {
		rows := steps[to].Reqs
		if len(rows) != 1 {
			t.Errorf("%s: expected one row, got %+v", to, rows)
			continue
		}
		if rows[0].Unavail != reason {
			t.Errorf("%s: unavail = %q, want %q", to, rows[0].Unavail, reason)
		}
		if rows[0].Met || rows[0].Note == "" {
			t.Errorf("%s: unavailable rows are never met and must explain why: %+v", to, rows[0])
		}
		if !steps[to].HasUnknown {
			t.Errorf("%s: an unmeasurable requirement must keep the step's ETA a floor", to)
		}
	}
	// A stat the tracker DOES report is never flagged unavailable, even when
	// there's no rate to project a date from.
	u := inviteUser()
	u.Stats.Uploads = 2
	for _, s := range DirectRoutesFrom(d, u, map[string]bool{}, ladderGroups, noInviteReqs) {
		if s.To == "Stat" && s.Reqs[0].Unavail != "" {
			t.Errorf("uploads 2/5 is trackable progress, not unavailable: %+v", s.Reqs[0])
		}
	}
}

// TestInviteReqsAddedWhenAbsent: a def requirement the community text doesn't
// mention is still added — this is what stops "No requirement" routes from
// reading as ready for a user who hasn't reached the invite-forum class.
func TestInviteReqsAddedWhenAbsent(t *testing.T) {
	newbie := inviteUser()
	newbie.Stats.AgeDays = 10
	ir := &defs.InviteReqs{GroupRequirements: defs.GroupRequirements{MinAge: "1Y"}}
	steps := map[string]Step{}
	for _, s := range DirectRoutesFrom(inviteData(), newbie, map[string]bool{}, ladderGroups,
		func(string) *defs.InviteReqs { return ir }) {
		steps[s.To] = s
	}
	s := steps["Silent"]
	if len(s.Reqs) != 1 || s.Reqs[0].Kind != "age" || s.Reqs[0].Met {
		t.Fatalf("expected a single unmet age row on the requirement-less route, got %+v", s.Reqs)
	}
	if s.ETADays != 355 {
		t.Errorf("expected 355 days of account age left, got %v", s.ETADays)
	}
}
