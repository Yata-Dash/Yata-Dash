package api

import (
	"math"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

func float64p(f float64) *float64 { return &f }

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 0.001 }

// TestRatioEtaDays covers ratio_min_eta_days' math: a real decline projects
// an ETA, current-below-minimum is 0 (not nil — the guard is already true),
// and every "no signal" case (no minimum, no rate, rate not declining, an
// Infinity ratio) is nil.
func TestRatioEtaDays(t *testing.T) {
	cases := []struct {
		name string
		cur  string
		min  float64
		rate *float64
		want *float64
	}{
		{"declining ratio crosses the minimum in 10 days", "1.2", 1.0, float64p(-0.02), float64p(10)},
		{"already at/below the minimum is 0, not nil", "0.9", 1.0, float64p(-0.02), float64p(0)},
		{"Infinity ratio never crosses anything", "Infinity", 1.0, float64p(-0.02), nil},
		{"no known minimum (min_ratio unset)", "1.2", 0, float64p(-0.02), nil},
		{"no rate signal", "1.2", 1.0, nil, nil},
		{"rate not declining (rising)", "1.2", 1.0, float64p(0.02), nil},
	}
	for _, tc := range cases {
		got := ratioEtaDays(tc.cur, tc.min, tc.rate)
		if (got == nil) != (tc.want == nil) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		if got != nil && !almostEqual(*got, *tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, *got, *tc.want)
		}
	}
}

// TestBufferZeroEtaDays covers buffer_zero_eta_days' math: a shrinking
// buffer projects an ETA; a growing buffer, a missing rate, and a zero/absent
// current buffer are all nil.
func TestBufferZeroEtaDays(t *testing.T) {
	cases := []struct {
		name   string
		buffer string
		rate   *float64
		want   *float64
	}{
		{"shrinking buffer projects an ETA", "100 GiB", float64p(-10), float64p(10)},
		{"growing buffer is nil", "100 GiB", float64p(10), nil},
		{"no rate signal is nil", "100 GiB", nil, nil},
		{"zero current buffer is nil", "0 B", float64p(-10), nil},
	}
	for _, tc := range cases {
		got := bufferZeroEtaDays(tc.buffer, tc.rate)
		if (got == nil) != (tc.want == nil) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		if got != nil && !almostEqual(*got, *tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, *got, *tc.want)
		}
	}
}

// TestBuildTrendContextSkipsWithNoRules: the DeclineSignals history query
// must never run (and the returned context stays the zero value) when the
// install has no alert rules — buildTrendContext's cheap no-op path.
func TestBuildTrendContextSkipsWithNoRules(t *testing.T) {
	d := testDeps(t)
	if err := d.Cfg.UpdateNotifications(models.NotificationConfig{}); err != nil {
		t.Fatal(err)
	}
	tr := models.Tracker{ID: "t1", URL: "//test.local"}
	// TrendContext now carries a slice field (GoalsBehind), so it's no longer
	// comparable with == — check each field is at its zero value instead.
	got := buildTrendContext(d, tr, models.MergedStats{}, nil)
	if got.RatioMinEtaDays != nil || got.BufferZeroEtaDays != nil ||
		got.SeedSizeDropPct != nil || got.SeedingDropPct != nil || len(got.GoalsBehind) != 0 {
		t.Errorf("expected a zero-value TrendContext with no rules, got %+v", got)
	}
}

// TestBuildTrendContextUsesDefMinRatio confirms the minRatio lookup wires
// through to the tracker's def (test_tracker.json declares rules.min_ratio:
// 0.4) end-to-end into a real RatioMinEtaDays, given a genuinely declining
// ratio history.
func TestBuildTrendContextUsesDefMinRatio(t *testing.T) {
	d := testDeps(t)
	if err := d.Cfg.UpdateNotifications(models.NotificationConfig{
		Rules: []models.AlertRule{{
			ID: "r1", Name: "guard", Enabled: true, Match: "all",
			Conditions: []models.Condition{{Field: "ratio_min_eta_days", Op: "lte", Value: "14"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	tr := models.Tracker{ID: "t1", URL: "//test.local"} // test_tracker.json: min_ratio 0.4

	now := time.Now().UTC()
	for i := 7; i >= 0; i-- {
		v := 0.60 - float64(7-i)*(0.02/7) // slow decline, well above the noise floor
		_ = d.DB.RecordDaily("t1", now.AddDate(0, 0, -i), map[string]float64{"ratio": v})
	}
	merged := models.MergedStats{"ratio": models.StatField{Value: "0.58"}}
	if got := buildTrendContext(d, tr, merged, nil); got.RatioMinEtaDays == nil {
		t.Error("expected a ratio ETA using the def's min_ratio")
	}
}
