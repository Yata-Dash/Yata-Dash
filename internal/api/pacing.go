// pacing.go computes goal-pacing verdicts for target rows carrying an
// optional deadline (Tracker.TargetDeadlines) — the rate NEEDED (remaining /
// days left) vs the rate the tracker HAS (existing GrowthRates), an
// on-track/behind verdict, and the goal_behind_pace alert signal. Mirrors
// web/src/utils/pacing.ts — parity of MEANING, not pixels. Reuses
// targeteval.go's key ordering/label/parsing helpers so both files agree on
// what a target row "is".
package api

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
)

// PacingState is one dated target row's pacing verdict.
type PacingState string

const (
	PacingDone      PacingState = "done"       // remaining <= 0 — met, nothing to pace
	PacingOverdue   PacingState = "overdue"    // deadline reached (today or past), still unmet
	PacingOnTrack   PacingState = "on_track"   // a growth rate exists and covers the required pace
	PacingBehind    PacingState = "behind"     // a growth rate exists but falls short
	PacingNoRate    PacingState = "no_rate"    // no measurable rate — required pace shown, no verdict
	PacingRatioInfo PacingState = "ratio_info" // ratio row — no verdict, just the needed-upload figure
)

// Pacing is the computed pacing for one dated target row.
type Pacing struct {
	State    PacingState
	DaysLeft int     // ceil(deadline - today); can be <= 0 when overdue
	Required float64 // remaining / days-left in the row's own unit; 0 for done/ratio_info
	Rate     float64 // the existing per-day growth rate; 0 when HasRate is false
	HasRate  bool
	// NeededUploadGiB is ratio_info's "extra upload to hit the target ratio"
	// figure — downloaded*target - uploaded — populated only when positive.
	NeededUploadGiB float64
}

// goalRateKey maps a target key to the GrowthRates map key that projects it
// (internal/stats's Engine.GrowthRates: uploaded, downloaded, seed_size,
// bonus_points, uploads_approved, buffer). Keys the rates map never
// populates (ratio, avg_seed, days, most custom counts) pass through
// unchanged — simply absent from rates, degrading to "no_rate" like any
// other unprojectable key.
func goalRateKey(key string) string {
	if key == "total_uploads" {
		return "uploads_approved"
	}
	return key
}

// computeGoalPacing computes one dated row's pacing. ok is false when the
// row isn't a valid dated row: no/unparseable deadline, no target value, or
// key is "days" (account age never takes a deadline — see sanitizeTargetDeadlines).
func computeGoalPacing(key, targetRaw, deadlineRaw string, merged models.MergedStats, rates map[string]float64, now time.Time) (Pacing, bool) {
	if key == "days" || strings.TrimSpace(targetRaw) == "" {
		return Pacing{}, false
	}
	deadline, err := time.Parse("2006-01-02", strings.TrimSpace(deadlineRaw))
	if err != nil {
		return Pacing{}, false
	}

	// Ratio rows are a wholly separate, verdict-less branch (see plan) — they
	// never reach the remaining/days-left math below.
	if key == "ratio" {
		return ratioPacing(targetRaw, merged), true
	}

	remaining, ok := goalRemaining(key, targetRaw, merged)
	if !ok {
		return Pacing{}, false
	}

	today := now.UTC().Truncate(24 * time.Hour)
	daysLeft := int(math.Ceil(deadline.Truncate(24*time.Hour).Sub(today).Hours() / 24))

	if remaining <= 0 {
		return Pacing{State: PacingDone, DaysLeft: daysLeft}, true
	}
	if daysLeft <= 0 {
		return Pacing{State: PacingOverdue, DaysLeft: daysLeft}, true
	}

	required := remaining / float64(daysLeft)
	rate, hasRate := rates[goalRateKey(key)]
	p := Pacing{DaysLeft: daysLeft, Required: required, Rate: rate, HasRate: hasRate}
	switch {
	case hasRate && rate >= required:
		p.State = PacingOnTrack
	case hasRate:
		p.State = PacingBehind
	default:
		p.State = PacingNoRate
	}
	return p, true
}

// goalRemaining computes a dated row's outstanding amount in its own unit
// (GiB for size fields, raw count/days otherwise). ok is false when either
// side doesn't parse — mirrors targeteval.go's baseTargetMet per-key switch
// (minus "ratio" and "days", handled by their own callers), but returns the
// gap instead of a met/unmet boolean.
func goalRemaining(key, targetRaw string, merged models.MergedStats) (float64, bool) {
	switch key {
	case "uploaded", "downloaded", "seed_size":
		return sizeRemaining(mergedFieldString(merged, key), targetRaw)
	case "total_uploads":
		return numRemaining(mergedFieldString(merged, "uploads_approved"), targetRaw)
	case "avg_seed":
		return seedTimeRemaining(mergedFieldString(merged, "avg_seed_time"), targetRaw)
	case "bonus_points", "adoptions", "snatched":
		return numRemaining(mergedFieldString(merged, key), targetRaw)
	default:
		return customRemaining(mergedFieldString(merged, key), targetRaw)
	}
}

func sizeRemaining(curRaw, tgtRaw string) (float64, bool) {
	tgt := parse.SizeToGiB(tgtRaw)
	if tgt == nil || *tgt <= 0 {
		return 0, false
	}
	cur := parse.SizeToGiB(curRaw)
	if cur == nil {
		return 0, false
	}
	return *tgt - *cur, true
}

func numRemaining(curRaw, tgtRaw string) (float64, bool) {
	tgt, ok := parseCleanFloat(tgtRaw)
	if !ok || tgt <= 0 {
		return 0, false
	}
	cur, ok := parseCleanFloat(curRaw)
	if !ok {
		return 0, false
	}
	return tgt - cur, true
}

// seedTimeRemaining is numRemaining's seconds counterpart, converted to days
// so avg_seed's "required" figure stays a plain per-day count like every
// other row (it's never rate-projected — see goalRateKey — but "no_rate"
// rows still show a needed-per-day figure).
func seedTimeRemaining(curRaw, tgtRaw string) (float64, bool) {
	tgt := parse.SeedTimeToSeconds(tgtRaw)
	if tgt == nil || *tgt <= 0 {
		return 0, false
	}
	cur := parse.SeedTimeToSeconds(curRaw)
	if cur == nil {
		return 0, false
	}
	return (*tgt - *cur) / 86400, true
}

// customRemaining handles a tracker-specific target key: compare as a size
// when BOTH sides parse as one, otherwise fall back to plain numeric
// comparison for both — mirrors targeteval.go's customMet exactly.
func customRemaining(curRaw, tgtRaw string) (float64, bool) {
	curV, tgtV := parse.SizeToGiB(curRaw), parse.SizeToGiB(tgtRaw)
	if curV == nil || tgtV == nil {
		c, cok := parseCleanFloat(curRaw)
		g, gok := parseCleanFloat(tgtRaw)
		curV, tgtV = nil, nil
		if cok {
			curV = &c
		}
		if gok {
			tgtV = &g
		}
	}
	if tgtV == nil || *tgtV <= 0 || curV == nil {
		return 0, false
	}
	return *tgtV - *curV, true
}

// ratioPacing computes a ratio row's info-only pacing: the extra upload
// (GiB) needed to reach the target ratio, populated only when positive.
// There's no honest ratio "rate" to pace against (see plan), so no
// on-track/behind verdict is ever produced here.
func ratioPacing(targetRaw string, merged models.MergedStats) Pacing {
	tgt, ok := parseRatioValue(targetRaw)
	if !ok || tgt <= 0 || math.IsInf(tgt, 1) {
		return Pacing{State: PacingRatioInfo}
	}
	downloaded := parse.SizeToGiB(mergedFieldString(merged, "downloaded"))
	uploaded := parse.SizeToGiB(mergedFieldString(merged, "uploaded"))
	if downloaded == nil || uploaded == nil {
		return Pacing{State: PacingRatioInfo}
	}
	p := Pacing{State: PacingRatioInfo}
	if needed := *downloaded*tgt - *uploaded; needed > 0 {
		p.NeededUploadGiB = needed
	}
	return p
}

// isGoalSizeKey reports whether a target key's pacing numbers are sizes
// (GiB/day) rather than a plain count/day — drives formatGoalRequired.
func isGoalSizeKey(key string) bool {
	switch key {
	case "uploaded", "downloaded", "seed_size":
		return true
	default:
		return false
	}
}

// formatGoalRequired renders a required-pace number in the row's unit for
// the goal_behind_pace alert wording: GiB/day for size-backed keys,
// otherwise a plain count/day.
func formatGoalRequired(key string, perDay float64) string {
	amount := strconv.FormatFloat(perDay, 'f', 1, 64)
	if isGoalSizeKey(key) {
		return amount + " GiB/day"
	}
	return amount + "/day"
}

// goalsBehindLabels computes the goal_behind_pace alert signal: one
// descriptive entry per dated row currently "behind" or "overdue". Ratio and
// account-age rows never contribute — ratio has no verdict, and age can't
// carry a deadline at all (sanitizeTargetDeadlines drops it before this ever
// runs). Nil/empty TargetDeadlines is a cheap no-op — most trackers set no
// goal pacing.
func goalsBehindLabels(t models.Tracker, merged models.MergedStats, rates map[string]float64, now time.Time) []string {
	if len(t.TargetDeadlines) == 0 {
		return nil
	}
	var out []string
	for _, key := range sortedTargetKeys(t.TargetDeadlines) {
		deadline := t.TargetDeadlines[key]
		tgt, hasTarget := t.Targets[key]
		if !hasTarget || deadline == "" {
			continue
		}
		p, ok := computeGoalPacing(key, tgt, deadline, merged, rates, now)
		if !ok {
			continue
		}
		switch p.State {
		case PacingOverdue:
			out = append(out, targetLabel(key)+" overdue")
		case PacingBehind:
			out = append(out, fmt.Sprintf("%s needs %s", targetLabel(key), formatGoalRequired(key, p.Required)))
		}
	}
	return out
}
