package notify

import (
	"fmt"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// TestAnnounceFiresAlreadyTrueRules: a newly saved rule fires at once for an
// in-scope tracker that already meets it, records firing state for every
// in-scope tracker, and leaves disabled and out-of-scope trackers alone.
func TestAnnounceFiresAlreadyTrueRules(t *testing.T) {
	rec := &fakeRecorder{}
	e := New(staticCfg{models.NotificationConfig{}}, nil)
	e.SetRecorder(rec)

	low := models.AlertRule{ID: "low", Name: "Low ratio", Enabled: true, Match: "all",
		Conditions: []models.Condition{{Field: "ratio", Op: "lt", Value: "1.0"}}}
	onlyB := models.AlertRule{ID: "onlyb", Name: "B ratio", Enabled: true, Match: "all",
		TrackerIDs: []string{"b"},
		Conditions: []models.Condition{{Field: "ratio", Op: "lt", Value: "1.0"}}}
	off := models.AlertRule{ID: "off", Name: "Off", Enabled: false,
		Conditions: []models.Condition{{Field: "ratio", Op: "lt", Value: "9"}}}
	trackers := []models.Tracker{
		{ID: "a", Name: "A", Enabled: true},
		{ID: "b", Name: "B", Enabled: true},
		{ID: "c", Name: "C", Enabled: false},
	}
	ratios := map[string]string{"a": "0.5", "b": "2.0", "c": "0.1"}

	e.Announce([]models.AlertRule{low, onlyB, off}, trackers,
		func(id string) models.MergedStats { return merged(map[string]any{"ratio": ratios[id]}) },
		func(string) TrendContext { return TrendContext{} })

	if len(rec.got) != 1 || rec.got[0].ruleID != "low" || rec.got[0].trackerID != "a" {
		t.Fatalf("recorded %+v, want exactly low|a", rec.got)
	}
	want := map[string]bool{"low|a": true, "low|b": false, "onlyb|b": false}
	for k, v := range want {
		got, ok := e.firing[k]
		if !ok || got != v {
			t.Errorf("firing[%s] = %v (set %v), want %v", k, got, ok, v)
		}
	}
	for _, k := range []string{"onlyb|a", "low|c", "off|a"} {
		if _, ok := e.firing[k]; ok {
			t.Errorf("firing[%s] set for an out-of-scope pair", k)
		}
	}
	if e.primed["a"] || e.primed["b"] {
		t.Error("Announce must not prime trackers")
	}

	// Already firing → a second Announce does not repeat it.
	e.Announce([]models.AlertRule{low}, trackers,
		func(id string) models.MergedStats { return merged(map[string]any{"ratio": ratios[id]}) },
		func(string) TrendContext { return TrendContext{} })
	if len(rec.got) != 1 {
		t.Fatalf("re-announce recorded %d alerts, want still 1", len(rec.got))
	}
}

// TestAnnounceComputesOncePerTrackerOutsideLock: merged stats and trend
// signals are gathered once per in-scope tracker, however many rules are
// being announced, and without holding the engine mutex — trendFn is costly
// and Evaluate must not queue behind it.
func TestAnnounceComputesOncePerTrackerOutsideLock(t *testing.T) {
	e := New(staticCfg{models.NotificationConfig{}}, nil)
	e.SetRecorder(&fakeRecorder{})

	var rules []models.AlertRule
	for i := 0; i < 5; i++ {
		rules = append(rules, models.AlertRule{ID: fmt.Sprint("r", i), Name: "R", Enabled: true, Match: "all",
			TrackerIDs: []string{"a", "b"},
			Conditions: []models.Condition{{Field: "ratio", Op: "lt", Value: "1.0"}}})
	}
	trackers := []models.Tracker{
		{ID: "a", Enabled: true}, {ID: "b", Enabled: true},
		{ID: "unreferenced", Enabled: true}, {ID: "disabled", Enabled: false},
	}
	mergedCalls, trendCalls := map[string]int{}, map[string]int{}
	lockFree := func(fn string) {
		if !e.mu.TryLock() {
			t.Errorf("%s called with the engine mutex held", fn)
			return
		}
		e.mu.Unlock()
	}
	e.Announce(rules, trackers,
		func(id string) models.MergedStats {
			lockFree("mergedFn")
			mergedCalls[id]++
			return merged(map[string]any{"ratio": "0.5"})
		},
		func(id string) TrendContext {
			lockFree("trendFn")
			trendCalls[id]++
			return TrendContext{}
		})

	for _, id := range []string{"a", "b"} {
		if mergedCalls[id] != 1 || trendCalls[id] != 1 {
			t.Errorf("tracker %s: mergedFn %d, trendFn %d calls; want 1 each", id, mergedCalls[id], trendCalls[id])
		}
	}
	for _, id := range []string{"unreferenced", "disabled"} {
		if mergedCalls[id] != 0 || trendCalls[id] != 0 {
			t.Errorf("tracker %s no rule references was computed (%d, %d)", id, mergedCalls[id], trendCalls[id])
		}
	}
}

// BenchmarkAnnounce: five rules over twenty trackers, with a per-call cost on
// trendFn standing in for Merged + GrowthRates + DeclineSignals.
func BenchmarkAnnounce(b *testing.B) {
	var rules []models.AlertRule
	for i := 0; i < 5; i++ {
		rules = append(rules, models.AlertRule{ID: fmt.Sprint("r", i), Name: "R", Enabled: true, Match: "all",
			Conditions: []models.Condition{{Field: "ratio", Op: "lt", Value: "1.0"}}})
	}
	var trackers []models.Tracker
	for i := 0; i < 20; i++ {
		trackers = append(trackers, models.Tracker{ID: fmt.Sprint("t", i), Enabled: true})
	}
	m := merged(map[string]any{"ratio": "2.0", "uploaded": "1.5 TiB", "avg_seed_time": "3M 6D"})
	trendCalls := 0
	trend := func(string) TrendContext {
		trendCalls++
		sink := 0.0
		for i := 0; i < 20000; i++ { // a little real work per call
			sink += float64(i) * 1.0001
		}
		_ = sink
		return TrendContext{}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := New(staticCfg{models.NotificationConfig{}}, nil)
		e.Announce(rules, trackers, func(string) models.MergedStats { return m }, trend)
	}
	b.ReportMetric(float64(trendCalls)/float64(b.N), "trendFn-calls/op")
}
