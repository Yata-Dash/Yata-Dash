// trends.go computes the standing-guard TrendContext used by alert rule
// evaluation — refreshTracker's Evaluate/EvaluateEvent/EvaluateTargets calls,
// the notifications dry-run handler, and Announce all build it through
// buildTrendContext so the ETA/drop-% math lives in exactly one place.
package api

import (
	"math"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
)

// buildTrendContext computes tracker t's standing-guard signals from its
// decline signals plus its current merged stats. Skips the DeclineSignals
// history query entirely when there are no alert rules configured — a cheap
// no-op for the common case of an install with no alerts. rates is the
// SAME GrowthRates result the caller already computed for this refresh pass
// (resp.Rates in refreshTracker) — never re-queried here, so a rule mixing
// goal_behind_pace with anything else sees numbers from one calculation.
func buildTrendContext(d *Deps, t models.Tracker, merged models.MergedStats, rates map[string]float64) notify.TrendContext {
	if len(d.Cfg.Notifications().Rules) == 0 {
		return notify.TrendContext{}
	}
	signals := d.Stats.DeclineSignals(t.ID)

	var minRatio float64
	if td, ok := d.Reg.TrackerByURL(t.URL); ok && td.Rules != nil {
		minRatio = td.Rules.MinRatio
	}

	return notify.TrendContext{
		RatioMinEtaDays:   ratioEtaDays(mergedFieldString(merged, "ratio"), minRatio, signals.RatioPerDay),
		BufferZeroEtaDays: bufferZeroEtaDays(mergedFieldString(merged, "buffer"), signals.BufferPerDay),
		SeedSizeDropPct:   signals.SeedSizeDrop7dPct,
		SeedingDropPct:    signals.SeedingDrop7dPct,
		GoalsBehind:       goalsBehindLabels(t, merged, rates, time.Now()),
	}
}

// ratioEtaDays is the ratio_min_eta_days math: days until the current ratio
// crosses minRatio at ratioPerDay's signed rate. nil when there's no known
// minimum, no measurable decline, or the ratio can't be read as a plain
// number (curRaw's "Infinity" sentinel means it never crosses anything).
// current already at/past the minimum reports 0, not nil — the guard is
// already true, and 0 reads correctly against any lte/lt/eq threshold.
func ratioEtaDays(curRaw string, minRatio float64, ratioPerDay *float64) *float64 {
	if minRatio <= 0 || ratioPerDay == nil || *ratioPerDay >= 0 {
		return nil
	}
	cur, ok := parseRatioValue(curRaw)
	if !ok || math.IsInf(cur, 1) {
		return nil
	}
	eta := 0.0
	if cur > minRatio {
		eta = (cur - minRatio) / -*ratioPerDay
	}
	return &eta
}

// bufferZeroEtaDays is the buffer_zero_eta_days math: days until the current
// buffer (GiB) reaches 0 at bufferPerDay's signed rate. nil when there's no
// measurable shrink or no positive current buffer to project from.
func bufferZeroEtaDays(bufferRaw string, bufferPerDay *float64) *float64 {
	if bufferPerDay == nil || *bufferPerDay >= 0 {
		return nil
	}
	buf := parse.SizeToGiB(bufferRaw)
	if buf == nil || *buf <= 0 {
		return nil
	}
	eta := *buf / -*bufferPerDay
	return &eta
}

// trendFnFor builds the trendFn closure Announce/DryRun call per tracker ID —
// shared so both call sites (internal/api/notifications.go) construct the
// TrendContext identically to refreshTracker, including fetching rates the
// same way (GrowthRates) rather than leaving goal pacing signal-less.
func trendFnFor(d *Deps) func(string) notify.TrendContext {
	return func(id string) notify.TrendContext {
		t, ok := d.Cfg.Tracker(id)
		if !ok {
			return notify.TrendContext{}
		}
		m, _ := d.Stats.Merged(id)
		rates := d.Stats.GrowthRates(id)
		return buildTrendContext(d, t, m, rates)
	}
}
