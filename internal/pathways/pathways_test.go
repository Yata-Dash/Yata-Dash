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
