package notify

import (
	"strings"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// DryRunResult is one tracker's evaluation of a rule during a dry run.
type DryRunResult struct {
	TrackerID   string `json:"tracker_id"`
	TrackerName string `json:"tracker_name"`
	Matched     bool   `json:"matched"`
	Detail      string `json:"detail"`
}

// DryRun evaluates a rule against every in-scope enabled tracker's current
// merged stats WITHOUT sending anything and without touching engine state.
// rule.Enabled is deliberately NOT checked — dry run exists to test rules
// before enabling them (the UI carries the "currently disabled" caveat).
// Limitations are inherent to a point-in-time preview: "changed" conditions
// have no previous value to compare against (never match), and trackers are
// assumed reachable (matching Announce's behaviour).
func DryRun(rule models.AlertRule, trackers []models.Tracker, mergedFn func(string) models.MergedStats, trendFn func(string) TrendContext) []DryRunResult {
	out := []DryRunResult{}
	// A rule with no conditions never matches/fires in the engine (Evaluate
	// and Announce both skip it) — enforce the same here rather than letting
	// evalRule's empty-AND-list semantics report "would fire".
	if len(rule.Conditions) == 0 {
		return out
	}
	for _, t := range trackers {
		if !t.Enabled || !rule.Matches(t.ID) {
			continue
		}
		merged := mergedFn(t.ID)
		cur := rawValues(merged)
		prev := map[string]string{} // no history in a dry run — "changed" can't match
		trends := trendFn(t.ID)
		out = append(out, DryRunResult{
			TrackerID:   t.ID,
			TrackerName: t.Name,
			Matched:     evalRule(rule, merged, cur, prev, true, trends),
			Detail:      describeDryRun(rule, merged, cur, prev, trends),
		})
	}
	return out
}

// describeDryRun lists each condition with the tracker's current value and a
// met/unmet mark, e.g. "ratio 5.20 < 1.0 ✗ and warnings 1 ≥ 1 ✓". Wording
// comes from the shared describeCondition so the preview reads exactly like
// the live alert message would.
func describeDryRun(rule models.AlertRule, merged models.MergedStats, cur, prev map[string]string, trends TrendContext) string {
	parts := make([]string, 0, len(rule.Conditions))
	for _, c := range rule.Conditions {
		met := evalCondition(c, merged, cur, prev, true, trends)
		mark := " ✗"
		if met {
			mark = " ✓"
		}
		var desc string
		switch {
		case c.Op == "changed":
			desc = c.Field + " changed (not previewable)"
		case c.Field == "reachable":
			desc = describeCondition(c, merged, cur, prev, trends) + " (assumed)"
		case c.Field == "freeleech_active" && c.Op == "is_true" && !met:
			// The live wording (eventDescription) only ever renders on a match;
			// for an unmet preview row "no active event" is the honest state.
			desc = "no active event"
		case (c.Field == "unread_mail" || c.Field == "unread_notifications") && c.Op == "is_true" && !met:
			// Same honesty rule: "unread mail waiting ✗" reads wrong — show the
			// is_false wording ("no unread mail") for unmet preview rows.
			desc = describeCondition(models.Condition{Field: c.Field, Op: "is_false"}, merged, cur, prev, trends)
		default:
			desc = describeCondition(c, merged, cur, prev, trends)
		}
		parts = append(parts, desc+mark)
	}
	return strings.Join(parts, conditionSep(rule))
}
