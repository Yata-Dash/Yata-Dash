package pathways

import "testing"

func pinUser(name string, age float64) UserTracker {
	return UserTracker{
		TrackerID: "id-" + name, PathwayName: name,
		Stats: Stats{AgeDays: age, UploadedGiB: 2048, Ratio: 2.0, SeedSizeGiB: -1, AvgSeedSec: -1, Uploads: -1, BonusPoints: -1},
	}
}

// A pin is looked up, not found: the exact chain is measured even when the
// search would rank it out, and the first open hop carries the live numbers.
func TestEvalPinMeasuresTheExactChain(t *testing.T) {
	d := testData()
	home := pinUser("Home", 200)
	res := EvalPin(d, []string{"Home", "Mid", "Target"}, []UserTracker{home}, testGroups, noInviteReqs)

	if res.State != PinOK {
		t.Fatalf("state = %q, want ok (%+v)", res.State, res)
	}
	if res.StartIndex != 0 || res.Destination != "Target" {
		t.Errorf("start=%d dest=%q", res.StartIndex, res.Destination)
	}
	if len(res.Steps) != 2 || res.Steps[0].To != "Mid" || res.Steps[1].To != "Target" {
		t.Fatalf("steps = %+v", res.Steps)
	}
	if res.Steps[0].Estimated || !res.Steps[1].Estimated {
		t.Error("only the first open hop should be measured live")
	}
	// Home → Mid lists "No requirement": one row, met.
	if res.Met != 1 || res.Total != 1 {
		t.Errorf("met/total = %d/%d, want 1/1", res.Met, res.Total)
	}
}

// The user's position on the chain is wherever they have got to, not where
// they pinned it from: once the intermediate tracker is in Yata the pin
// measures from there, with that tracker's stats.
func TestEvalPinAdvancesToTheFurthestOwnedHop(t *testing.T) {
	d := testData()
	users := []UserTracker{pinUser("Home", 200), pinUser("Mid", 10)}
	res := EvalPin(d, []string{"Home", "Mid", "Target"}, users, testGroups, noInviteReqs)

	if res.State != PinOK || res.StartIndex != 1 {
		t.Fatalf("state=%q start=%d, want ok from Mid", res.State, res.StartIndex)
	}
	if res.StartTrackerID != "id-Mid" {
		t.Errorf("measured from %q, want Mid's tracker", res.StartTrackerID)
	}
	if len(res.Steps) != 1 || res.Steps[0].From != "Mid" {
		t.Fatalf("steps = %+v", res.Steps)
	}
	// Mid → Target wants 3 months; Mid is 10 days old, so not met.
	if res.Met != 0 || res.Total != 1 {
		t.Errorf("met/total = %d/%d, want 0/1", res.Met, res.Total)
	}

	// Holding the destination itself is the end of the road.
	res = EvalPin(d, []string{"Home", "Target"}, []UserTracker{pinUser("Target", 1)}, testGroups, noInviteReqs)
	if res.State != PinReached {
		t.Errorf("state = %q, want reached", res.State)
	}
}

// Dataset changes are reported, never acted on. The chain the user chose is
// returned whole so the card can say which hop went, and unpin stays theirs.
func TestEvalPinReportsWhatTheDatasetLost(t *testing.T) {
	d := testData()
	home := []UserTracker{pinUser("Home", 200)}

	res := EvalPin(d, []string{"Home", "Dead", "Target"}, home, testGroups, noInviteReqs)
	if res.State != PinInactive || res.BrokenHop != 1 {
		t.Errorf("inactive route: state=%q broken=%d", res.State, res.BrokenHop)
	}
	if len(res.Hops) != 3 {
		t.Error("the stored chain must come back untouched")
	}

	res = EvalPin(d, []string{"Home", "Gone", "Target"}, home, testGroups, noInviteReqs)
	if res.State != PinMissing || res.BrokenHop != 1 {
		t.Errorf("missing route: state=%q broken=%d", res.State, res.BrokenHop)
	}

	// A user who has since removed every tracker on the chain.
	res = EvalPin(d, []string{"Home", "Target"}, nil, testGroups, noInviteReqs)
	if res.State != PinNoStart {
		t.Errorf("no owned hop: state = %q", res.State)
	}

	// Garbage in the settings file is not a crash.
	if res = EvalPin(d, []string{"Home"}, home, testGroups, noInviteReqs); res.State != PinMissing {
		t.Errorf("one-hop pin: state = %q", res.State)
	}
}

// Closest first: ready, then only-stats-left, then by the age floor — and
// anything not in play after all of those.
func TestSortPinsClosestFirst(t *testing.T) {
	mk := func(name, state string, eta float64, unknown, disabled bool) PinResult {
		p := PinResult{Destination: name, State: state}
		if state == PinOK {
			p.Steps = []Step{{ETADays: eta, HasUnknown: unknown}}
			p.TotalETADays = eta
			p.StartDisabled = disabled
		}
		return p
	}
	pins := []PinResult{
		mk("broken", PinMissing, 0, false, false),
		mk("wait-90", PinOK, 90, false, false),
		mk("reached", PinReached, 0, false, false),
		mk("wait-30", PinOK, 30, true, false),
		mk("stats-left", PinOK, 0, true, false),
		mk("disabled", PinOK, 0, true, true),
		mk("ready", PinOK, 0, false, false),
	}
	SortPins(pins)
	var got []string
	for _, p := range pins {
		got = append(got, p.Destination)
	}
	want := []string{"ready", "stats-left", "wait-30", "wait-90", "disabled", "reached", "broken"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
