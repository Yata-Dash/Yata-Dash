package pathways

import "testing"

// unknownStats is a tracker with nothing measurable — what a disabled
// tracker always contributes (its stored numbers are frozen, so the API
// layer deliberately passes none).
func unknownStats() Stats {
	return Stats{
		AgeDays: -1, UploadedGiB: -1, DownloadedGiB: -1, Ratio: -1,
		SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, Adoptions: -1, BonusPoints: -1,
	}
}

// TestDisabledNeverReady is the safety guarantee: a disabled tracker must
// never be reported as meeting a target's requirements — including on a
// "No requirement" route, where nothing else in the evaluation would raise
// a flag. Otherwise the digest and the reqs-met filter would send the user
// off to claim an invite on the strength of no data at all.
func TestDisabledNeverReady(t *testing.T) {
	d := testData()
	u := UserTracker{
		TrackerID: "t1", PathwayName: "Home",
		Stats: unknownStats(), Disabled: true,
	}
	// Home → Mid is the "No requirement" route.
	if ready := ReadyTargets(d, []UserTracker{u}, testGroups, noInviteReqs); len(ready) != 0 {
		t.Errorf("disabled tracker must never be reqs-met, got %+v", ready)
	}
	// The same route evaluated as a direct route carries the unknown flag.
	routes := DirectRoutesFrom(d, u, map[string]bool{"Home": true}, testGroups, noInviteReqs)
	if len(routes) == 0 {
		t.Fatal("disabled tracker should still list its outbound routes")
	}
	for _, s := range routes {
		if !s.HasUnknown {
			t.Errorf("route to %s should be flagged unknown for a disabled tracker: %+v", s.To, s)
		}
	}

	// Enabled control: the same no-requirement route IS ready when the
	// tracker is live, proving the flag comes from Disabled and not the data.
	live := UserTracker{TrackerID: "t1", PathwayName: "Home", Stats: unknownStats()}
	if ready := ReadyTargets(d, []UserTracker{live}, testGroups, noInviteReqs); !ready["Mid"] {
		t.Errorf("enabled tracker on a no-requirement route should be ready, got %+v", ready)
	}
}

// TestDisabledPathMarkedAndRankedLast: paths starting from a disabled
// tracker are flagged for the UI badge and sort below every path from a
// tracker with real stats (so they also lose first to the maxPaths cap).
func TestDisabledPathMarkedAndRankedLast(t *testing.T) {
	d := testData()
	// "Island" reaches Target in one hop and is the disabled one; "Home" is a
	// fully-met enabled tracker.
	enabled := UserTracker{
		TrackerID: "t1", PathwayName: "Home",
		Stats: Stats{AgeDays: 200, UploadedGiB: 2048, Ratio: 2.0, SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, BonusPoints: -1},
	}
	disabled := UserTracker{
		TrackerID: "t2", PathwayName: "Island",
		Stats: unknownStats(), Disabled: true,
	}
	res := FindPaths(d, []UserTracker{disabled, enabled}, "Target", testGroups, noInviteReqs)
	if len(res.Paths) < 2 {
		t.Fatalf("expected paths from both trackers, got %d", len(res.Paths))
	}
	if res.Paths[0].StartDisabled {
		t.Errorf("enabled tracker's path must rank first, got %+v", res.Paths[0])
	}
	last := res.Paths[len(res.Paths)-1]
	if !last.StartDisabled {
		t.Errorf("disabled tracker's path must rank last, got %+v", last)
	}
	if !last.HasUnknown {
		t.Error("a disabled tracker's path must be flagged unknown, never a firm ETA")
	}
}
