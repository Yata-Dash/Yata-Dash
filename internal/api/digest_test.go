package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/pathways"
)

// ─────────────────────────────────────────────────────────────────────────────
// digestDueAt
// ─────────────────────────────────────────────────────────────────────────────

// digestAnchor is a known Monday (2024-01-01 was a Monday) so weekday-relative
// test cases don't need runtime weekday arithmetic to read.
var digestAnchor = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func TestDigestDueAt(t *testing.T) {
	monWeekly := models.DigestConfig{Enabled: true, Weekday: 1, Hour: 9} // Monday 09:00

	cases := []struct {
		name           string
		cfg            models.DigestConfig
		now            time.Time
		wantDue        bool
		wantScheduled  time.Time
		checkScheduled bool
	}{
		{
			name: "before this week's instant → previous week's instant is due",
			cfg:  monWeekly, now: digestAnchor.Add(8*time.Hour + 59*time.Minute),
			wantDue: true, wantScheduled: digestAnchor.AddDate(0, 0, -7).Add(9 * time.Hour), checkScheduled: true,
		},
		{
			name: "after this week's instant, same day → this week's instant",
			cfg:  monWeekly, now: digestAnchor.Add(10 * time.Hour),
			wantDue: true, wantScheduled: digestAnchor.Add(9 * time.Hour), checkScheduled: true,
		},
		{
			name: "midweek → most recent Monday, not next Monday",
			cfg:  monWeekly, now: digestAnchor.AddDate(0, 0, 3).Add(9 * time.Hour), // Thursday 09:00
			wantDue: true, wantScheduled: digestAnchor.Add(9 * time.Hour), checkScheduled: true,
		},
		{
			name: "exact boundary, never sent → due",
			cfg:  monWeekly, now: digestAnchor.Add(9 * time.Hour),
			wantDue: true, wantScheduled: digestAnchor.Add(9 * time.Hour), checkScheduled: true,
		},
		{
			name: "exact boundary, already sent at that instant → not due",
			cfg: models.DigestConfig{Enabled: true, Weekday: 1, Hour: 9,
				LastSentAt: digestAnchor.Add(9 * time.Hour).Unix()},
			now:     digestAnchor.Add(9 * time.Hour),
			wantDue: false, wantScheduled: digestAnchor.Add(9 * time.Hour), checkScheduled: true,
		},
		{
			name: "catch-up after downtime — long-stale LastSentAt still resolves to the LATEST missed instant",
			cfg: models.DigestConfig{Enabled: true, Weekday: 1, Hour: 9,
				LastSentAt: digestAnchor.AddDate(0, 0, -14).Unix()},
			now:     digestAnchor.AddDate(0, 0, 10).Add(9 * time.Hour), // Thursday, 10 days after anchor
			wantDue: true, wantScheduled: digestAnchor.AddDate(0, 0, 7).Add(9 * time.Hour), checkScheduled: true,
		},
		{
			name:    "disabled → never due regardless of LastSentAt",
			cfg:     models.DigestConfig{Enabled: false, Weekday: 1, Hour: 9},
			now:     digestAnchor.Add(9 * time.Hour),
			wantDue: false, checkScheduled: false,
		},
		{
			name:    "week wrap: Sunday schedule, now on Saturday → the Sunday before, across a month boundary",
			cfg:     models.DigestConfig{Enabled: true, Weekday: 0, Hour: 9},
			now:     digestAnchor.AddDate(0, 0, 5).Add(12 * time.Hour), // Saturday 12:00
			wantDue: true, wantScheduled: time.Date(2023, 12, 31, 9, 0, 0, 0, time.UTC), checkScheduled: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scheduled, due := digestDueAt(c.cfg, c.now)
			if due != c.wantDue {
				t.Errorf("due = %v, want %v", due, c.wantDue)
			}
			if c.checkScheduled && !scheduled.Equal(c.wantScheduled) {
				t.Errorf("scheduled = %v, want %v", scheduled, c.wantScheduled)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// buildDigest
// ─────────────────────────────────────────────────────────────────────────────

// TestBuildDigestQuietWeek: no enabled trackers with movement, no events, no
// newly-met pathway targets → the whole body collapses to the heartbeat line.
func TestBuildDigestQuietWeek(t *testing.T) {
	d := testDeps(t)
	tr := models.Tracker{ID: "t1", Name: "Quiet Tracker", Enabled: true}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	text, ready := buildDigest(d, time.Now())
	want := "All quiet this week — no stat movement, no group changes. 1 tracker watched."
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if len(ready) != 0 {
		t.Errorf("ready = %v, want empty (no pathways data loaded)", ready)
	}
}

// TestBuildDigestTrackerLinesAndFragments covers the per-tracker line: deltas
// (uploaded/downloaded/buffer/ratio), the no-data-at-all "no change" fallback,
// the targets m/T fragment, and the goal-pacing "behind" fragment.
func TestBuildDigestTrackerLinesAndFragments(t *testing.T) {
	d := testDeps(t)
	now := time.Now()

	moving := models.Tracker{
		ID: "moving", Name: "TrackerOne", Enabled: true,
		Targets:         map[string]string{"uploaded": "1 TiB", "downloaded": "5 TiB"},
		TargetDeadlines: map[string]string{"downloaded": now.UTC().AddDate(0, 0, 10).Format("2006-01-02")},
	}
	still := models.Tracker{ID: "still", Name: "TrackerTwo", Enabled: true}
	if err := d.Cfg.AddTracker(moving); err != nil {
		t.Fatal(err)
	}
	if err := d.Cfg.AddTracker(still); err != nil {
		t.Fatal(err)
	}

	// Two daily rollup points within the 7-day window: uploaded +210 GiB,
	// downloaded +12 GiB, buffer +198 GiB, ratio 1.50→1.58.
	day0 := now.Add(-6 * 24 * time.Hour)
	day1 := now.Add(-1 * 24 * time.Hour)
	if err := d.DB.RecordDaily("moving", day0, map[string]float64{
		"uploaded": 100, "downloaded": 50, "buffer": 20, "ratio": 1.50,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.RecordDaily("moving", day1, map[string]float64{
		"uploaded": 310, "downloaded": 62, "buffer": 218, "ratio": 1.58,
	}); err != nil {
		t.Fatal(err)
	}

	// Current merged stats: uploaded target (1 TiB) met; downloaded target
	// (5 TiB) unmet, and (with the 2.4 GiB/day rate the history above
	// implies) far behind the pace a 10-day deadline requires.
	if err := d.Stats.SaveAPI("moving", map[string]any{
		"uploaded": "2 TiB", "downloaded": "100 GiB", "ratio": "1.58",
	}); err != nil {
		t.Fatal(err)
	}

	text, _ := buildDigest(d, now)

	wantMoving := "TrackerOne: ↑210.0 GiB up · ↓12.0 GiB down · buffer +198.0 GiB · ratio 1.50→1.58 · targets 1/2 met · goal: behind (Downloaded needs 502.0 GiB/day)"
	if !strings.Contains(text, wantMoving) {
		t.Errorf("digest missing moving-tracker line.\n got: %s\nwant substring: %s", text, wantMoving)
	}
	if !strings.Contains(text, "TrackerTwo: no change") {
		t.Errorf("digest missing no-history tracker's \"no change\" line.\ngot: %s", text)
	}
}

// TestBuildDigestEventsWithDirection covers the "This week:" events section:
// a real def group ladder (aither.cc) classifies a rise as promoted (▲) and
// a fall as demoted (▼).
func TestBuildDigestEventsWithDirection(t *testing.T) {
	d := testDeps(t)
	now := time.Now()

	promoted := models.Tracker{ID: "p1", Name: "Promo Tracker", URL: "https://aither.cc", Enabled: true}
	demoted := models.Tracker{ID: "d1", Name: "Demo Tracker", URL: "https://aither.cc", Enabled: true}
	if err := d.Cfg.AddTracker(promoted); err != nil {
		t.Fatal(err)
	}
	if err := d.Cfg.AddTracker(demoted); err != nil {
		t.Fatal(err)
	}

	evAt := now.Add(-2 * 24 * time.Hour)
	if err := d.DB.AddEvent("p1", evAt, "group_change", "Phobos→Zeus"); err != nil { // rises in aither's ladder
		t.Fatal(err)
	}
	if err := d.DB.AddEvent("d1", evAt, "group_change", "Zeus→Phobos"); err != nil { // falls
		t.Fatal(err)
	}

	text, _ := buildDigest(d, now)
	day := evAt.Format("Mon")

	wantPromo := fmt.Sprintf("▲ Promo Tracker promoted Phobos → Zeus (%s)", day)
	wantDemo := fmt.Sprintf("▼ Demo Tracker demoted Zeus → Phobos (%s)", day)
	if !strings.Contains(text, "This week:") {
		t.Errorf("digest missing \"This week:\" events header.\ngot: %s", text)
	}
	if !strings.Contains(text, wantPromo) {
		t.Errorf("digest missing promotion line.\nwant substring: %s\ngot: %s", wantPromo, text)
	}
	if !strings.Contains(text, wantDemo) {
		t.Errorf("digest missing demotion line.\nwant substring: %s\ngot: %s", wantDemo, text)
	}
}

// fixturePathwaysData writes a minimal routes.json to a temp file and loads
// it — a small controlled fixture instead of depending on the large, real
// community dataset in defs/pathways/routes.json. "Aura4K" is a real def
// (defs/trackers/aura4k.json) with NO invite_requirements of its own, so a
// plain "None" route requirement isn't augmented by def-level extras the way
// it would be for a def like Aither (which carries a real min_class) —
// keeping this fixture's "always ready" route honest.
func fixturePathwaysData(t *testing.T) *pathways.Data {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "routes.json")
	raw := `{
		"schema_version": 1,
		"source": {"name": "test", "url": "https://test.invalid", "license": "test"},
		"routes": [
			{"from": "Aura4K", "to": "TargetX", "days": 0, "reqs": "None", "active": true}
		],
		"unlocks": {}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := pathways.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestBuildDigestNewlyMetPathwaysAndSnapshot covers the "newly requirements-met"
// diff (present now + absent from the snapshot → listed; present in both →
// not listed) and the snapshot round-trip through UpdateDigestState.
func TestBuildDigestNewlyMetPathwaysAndSnapshot(t *testing.T) {
	d := testDeps(t)
	d.Paths = fixturePathwaysData(t)

	owner := models.Tracker{ID: "own1", Name: "Owner", URL: "https://aura4k.net", Enabled: true}
	if err := d.Cfg.AddTracker(owner); err != nil {
		t.Fatal(err)
	}

	now := time.Now()

	// First digest: nothing in the snapshot yet → TargetX is newly-met.
	text, readyNow := buildDigest(d, now)
	if len(readyNow) != 1 || readyNow[0] != "TargetX" {
		t.Fatalf("readyNow = %v, want [TargetX]", readyNow)
	}
	if !strings.Contains(text, "Newly requirements-met: TargetX") {
		t.Errorf("expected TargetX announced as newly requirements-met, got: %s", text)
	}

	// Persist the snapshot exactly like a real send would.
	if err := d.Cfg.UpdateDigestState(now.Unix(), readyNow); err != nil {
		t.Fatal(err)
	}
	if got := d.Cfg.Notifications().Digest.LastReadyTargets; len(got) != 1 || got[0] != "TargetX" {
		t.Fatalf("stored snapshot = %v, want [TargetX]", got)
	}

	// Second digest: TargetX is already in the snapshot → readyNow still
	// reports it (full current set), but it's no longer "newly" met.
	text2, readyNow2 := buildDigest(d, now)
	if len(readyNow2) != 1 || readyNow2[0] != "TargetX" {
		t.Fatalf("readyNow2 = %v, want [TargetX] (full current set, snapshot or not)", readyNow2)
	}
	if strings.Contains(text2, "Newly requirements-met") {
		t.Errorf("TargetX already in the snapshot must not be re-announced, got: %s", text2)
	}
}

// TestReadyPathwayTargetNamesNilWithoutData: the pathways feature being
// absent (no dataset loaded) must degrade to an empty ready set, not a panic.
func TestReadyPathwayTargetNamesNilWithoutData(t *testing.T) {
	d := testDeps(t)
	if got := readyPathwayTargetNames(d); got != nil {
		t.Errorf("readyPathwayTargetNames with no pathways data = %v, want nil", got)
	}
}
