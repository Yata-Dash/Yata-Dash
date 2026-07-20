package notify

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
)

// Logger is the subset of the app logger the engine uses (avoids an import cycle).
type Logger interface {
	Infof(string, ...any)
	Warnf(string, ...any)
	Debugf(string, ...any)
}

// ConfigSource provides the current notification config to the engine.
type ConfigSource interface {
	Notifications() models.NotificationConfig
}

// numericFields are stat fields exposed to conditions as numbers, with the unit
// used for comparison. Sizes compare in GiB, durations in days.
var numericFields = map[string]string{
	"ratio":         "",
	"buffer":        "GiB",
	"uploaded":      "GiB",
	"downloaded":    "GiB",
	"seed_size":     "GiB",
	"seeding":       "",
	"leeching":      "",
	"warnings":      "",
	"hit_and_runs":  "",
	"bonus_points":  "",
	"avg_seed_time": "days",
}

// Engine evaluates alert rules against fresh stats and fires webhooks on the
// rising edge (false→true) of a rule. State is kept in memory; the first
// evaluation per tracker after start "primes" silently so a restart never
// re-fires conditions that are already true.
type Engine struct {
	cfg ConfigSource
	log Logger

	mu        sync.Mutex
	firing    map[string]bool              // "ruleID|trackerID" → currently matched
	lastFired map[string]time.Time         // "ruleID|trackerID" → last notification time
	prevVals  map[string]map[string]string // trackerID → field → previous raw value
	primed    map[string]bool              // trackerID → baseline established
	// targetState is EvaluateTargets' edge-tracking state: trackerID → target
	// row key → met, as of the last pass. Separate from prevVals/primed
	// because target rows are per-tracker structural data (rows can appear/
	// disappear as the user edits targets), not a single field snapshot.
	targetState map[string]map[string]bool
}

// New creates an alert engine.
func New(cfg ConfigSource, log Logger) *Engine {
	return &Engine{
		cfg:         cfg,
		log:         log,
		firing:      map[string]bool{},
		lastFired:   map[string]time.Time{},
		prevVals:    map[string]map[string]string{},
		primed:      map[string]bool{},
		targetState: map[string]map[string]bool{},
	}
}

// TrendContext carries the standing-guard signals for one evaluation pass.
// Nil pointers = no signal → the condition is false. The zero value is what
// callers without trend data (some tests) pass.
type TrendContext struct {
	RatioMinEtaDays   *float64
	BufferZeroEtaDays *float64
	SeedSizeDropPct   *float64
	SeedingDropPct    *float64
	// GoalsBehind carries goal_behind_pace's signal: one descriptive entry
	// per dated target row (see internal/api/pacing.go) currently behind or
	// overdue on its deadline. Empty/nil = on pace for every dated goal.
	GoalsBehind []string
}

// Evaluate runs every rule for one tracker against its merged stats. reachable
// reports whether the latest fetch succeeded (drives the `reachable` field).
// trends carries this pass's standing-guard signals (zero value if the caller
// has none to offer).
func (e *Engine) Evaluate(t models.Tracker, merged models.MergedStats, reachable bool, trends TrendContext) {
	cfg := e.cfg.Notifications()
	if len(cfg.Rules) == 0 {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	prev := e.prevVals[t.ID]
	if prev == nil {
		prev = map[string]string{}
	}
	cur := rawValues(merged)
	primed := e.primed[t.ID]

	for _, rule := range cfg.Rules {
		if !rule.Enabled || len(rule.Conditions) == 0 {
			continue
		}
		if !rule.Matches(t.ID) {
			continue
		}
		matched := evalRule(rule, merged, cur, prev, reachable, trends)
		key := rule.ID + "|" + t.ID
		if primed && matched && !e.firing[key] {
			e.fire(cfg, rule, t, merged, cur, prev, reachable, trends, key)
		}
		e.firing[key] = matched
	}

	// Update previous-value snapshot + mark primed for next time.
	e.prevVals[t.ID] = cur
	e.primed[t.ID] = true
}

// Announce immediately fires the given (typically newly-created) rules for any
// in-scope tracker that ALREADY meets their conditions, using last-known merged
// stats. This gives the user instant confirmation on setup instead of waiting
// for a future transition. It records firing state so the normal edge-triggered
// evaluation won't re-fire the same already-true condition.
func (e *Engine) Announce(rules []models.AlertRule, trackers []models.Tracker, mergedFn func(string) models.MergedStats, trendFn func(string) TrendContext) {
	cfg := e.cfg.Notifications()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, rule := range rules {
		if !rule.Enabled || len(rule.Conditions) == 0 {
			continue
		}
		for _, t := range trackers {
			if !t.Enabled || !rule.Matches(t.ID) {
				continue
			}
			merged := mergedFn(t.ID)
			cur := rawValues(merged)
			prev := e.prevVals[t.ID]
			if prev == nil {
				prev = map[string]string{}
			}
			trends := trendFn(t.ID)
			matched := evalRule(rule, merged, cur, prev, true, trends)
			key := rule.ID + "|" + t.ID
			if matched && !e.firing[key] {
				e.fire(cfg, rule, t, merged, cur, prev, true, trends, key)
			}
			// Record state but DON'T mark the tracker primed — let normal
			// priming baseline the OTHER (existing) rules silently.
			e.firing[key] = matched
		}
	}
}

// EventContext describes a one-shot tracker event being evaluated: a
// promotion/demotion (internal/api/stats.go's recordGroupChange) or a target
// row crossing into met (EvaluateTargets below). Unlike Evaluate's level-
// triggered conditions, these happen once, at the moment they're detected —
// there is no "currently true" state to poll later.
type EventContext struct {
	Kind   string // "promoted" | "demoted" | "target_met"
	Detail string // human text, e.g. "promoted: User → Power User" or "met target 3/5 — Ratio"
}

// EvaluateEvent fires every rule matching a one-shot tracker event. A
// condition matches in event context when its field equals ev.Kind (any op
// counts as is_true for event fields); other conditions in the same rule
// still evaluate against merged/cur/prev as usual, so e.g. "promoted AND
// ratio < 1" only fires alongside a real promotion. Cooldown/destination
// logic is identical to fire() (via send) — but firing/primed are never
// touched, since events aren't a level that can stay "on".
func (e *Engine) EvaluateEvent(t models.Tracker, merged models.MergedStats, ev EventContext, trends TrendContext) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evaluateEventLocked(t, merged, ev, trends)
}

// evaluateEventLocked is EvaluateEvent's body, factored out so EvaluateTargets
// (which already holds e.mu while diffing per-row state) can fire several
// target_met events in one pass without re-entering the mutex.
func (e *Engine) evaluateEventLocked(t models.Tracker, merged models.MergedStats, ev EventContext, trends TrendContext) {
	cfg := e.cfg.Notifications()
	if len(cfg.Rules) == 0 {
		return
	}
	prev := e.prevVals[t.ID]
	if prev == nil {
		prev = map[string]string{}
	}
	cur := rawValues(merged)
	for _, rule := range cfg.Rules {
		if !rule.Enabled || len(rule.Conditions) == 0 {
			continue
		}
		if !rule.Matches(t.ID) {
			continue
		}
		if !evalEventRule(rule, ev, merged, cur, prev, trends) {
			continue
		}
		key := rule.ID + "|" + t.ID
		e.send(cfg, rule, t, key, describeEvent(rule, ev, merged, cur, prev, trends))
	}
}

// evalEventRule is evalRule's event-context counterpart: a condition matching
// ev.Kind counts as true regardless of its op; everything else falls back to
// the normal evalCondition (reachable=true — an event is only ever raised
// from a live, successful refresh).
func evalEventRule(rule models.AlertRule, ev EventContext, merged models.MergedStats, cur, prev map[string]string, trends TrendContext) bool {
	any := rule.Match == "any"
	for _, c := range rule.Conditions {
		ok := c.Field == ev.Kind || evalCondition(c, merged, cur, prev, true, trends)
		if any && ok {
			return true
		}
		if !any && !ok {
			return false
		}
	}
	return !any
}

// describeEvent renders a rule's fired message for an event: the triggering
// condition uses ev.Detail (the real promotion/demotion/target text) instead
// of describeCondition's generic "fires when…" wording, which only shows up
// in normal Evaluate/DryRun previews where the event never actually matches.
func describeEvent(rule models.AlertRule, ev EventContext, merged models.MergedStats, cur, prev map[string]string, trends TrendContext) string {
	parts := make([]string, 0, len(rule.Conditions))
	for _, c := range rule.Conditions {
		if c.Field == ev.Kind {
			parts = append(parts, ev.Detail)
			continue
		}
		// Other EVENT fields in the same rule (e.g. the seeded "promoted OR
		// demoted" rule) describe as "fires when…" placeholders — meta-text,
		// not information about THIS firing — so they're dropped from the
		// message. Non-event conditions still describe their live values.
		if isEventField(c.Field) {
			continue
		}
		parts = append(parts, describeCondition(c, merged, cur, prev, trends))
	}
	return strings.Join(parts, conditionSep(rule))
}

// isEventField reports whether a condition field is a one-shot event kind.
func isEventField(f string) bool {
	return f == "promoted" || f == "demoted" || f == "target_met"
}

// TargetRow is one evaluated target row for event tracking — an EDGE unit,
// not a display unit: any_of alternatives are separate rows here even though
// they collapse to one logical row in the target_met message's m/T (see
// internal/api/targeteval.go).
type TargetRow struct {
	Key   string // stable identity, e.g. "uploaded", "min_counts.vanguard_seeds", "any_of.0"
	Label string // display label, e.g. "Ratio", "One of: Uploaded"
	Met   bool
}

// EvaluateTargets diffs rows against the tracker's previous snapshot and
// fires a target_met event for each row transitioning unmet→met. First
// sighting of a tracker OR of a new row key primes silently (no fire) — a
// restart or a newly-added target must never fire on data that was already
// true. Rows absent from this pass (the user removed a target) simply drop
// out of the tracked state. logicalMet/logicalTotal are precomputed by the
// caller and feed the m/T in the fired message.
func (e *Engine) EvaluateTargets(t models.Tracker, merged models.MergedStats, rows []TargetRow, logicalMet, logicalTotal int, trends TrendContext) {
	e.mu.Lock()
	defer e.mu.Unlock()

	state := e.targetState[t.ID]
	next := make(map[string]bool, len(rows))
	for _, row := range rows {
		wasMet, known := state[row.Key]
		next[row.Key] = row.Met
		if !known {
			continue // new tracker or new row key — prime silently
		}
		if !wasMet && row.Met {
			ev := EventContext{Kind: "target_met", Detail: fmt.Sprintf("met target %d/%d — %s", logicalMet, logicalTotal, row.Label)}
			e.evaluateEventLocked(t, merged, ev, trends)
		}
	}
	e.targetState[t.ID] = next
}

// fire sends the rule's message to its destinations (respecting cooldown).
func (e *Engine) fire(cfg models.NotificationConfig, rule models.AlertRule, t models.Tracker,
	merged models.MergedStats, cur, prev map[string]string, reachable bool, trends TrendContext, key string) {
	e.send(cfg, rule, t, key, describeMatch(rule, merged, cur, prev, reachable, trends))
}

// send delivers a rule's message to its destinations, respecting cooldown.
// Shared by fire() (level-triggered rules from Evaluate/Announce) and the
// one-shot event path (EvaluateEvent/EvaluateTargets) — everything past
// "what's the detail text" is identical.
func (e *Engine) send(cfg models.NotificationConfig, rule models.AlertRule, t models.Tracker, key, detail string) {
	if cd := time.Duration(rule.CooldownMins) * time.Minute; cd > 0 {
		if last, ok := e.lastFired[key]; ok && time.Since(last) < cd {
			return
		}
	}
	title := fmt.Sprintf("Yata alert: %s", rule.Name)
	msg := fmt.Sprintf("%s — %s", t.Name, detail)
	dests := resolveDestinations(cfg, rule)
	if len(dests) == 0 {
		return
	}
	e.lastFired[key] = time.Now()
	for _, d := range dests {
		go func(dest models.NotifyDestination) {
			if err := Send(dest, title, msg); err != nil {
				if e.log != nil {
					e.log.Warnf("notify: %q → %s failed: %v", rule.Name, dest.Name, err)
				}
				return
			}
			if e.log != nil {
				e.log.Infof("notify: %q fired → %s (%s)", rule.Name, dest.Name, t.Name)
			}
		}(d)
	}
}

// resolveDestinations returns the enabled destinations a rule targets (empty
// rule.Destinations = all enabled destinations).
func resolveDestinations(cfg models.NotificationConfig, rule models.AlertRule) []models.NotifyDestination {
	return ResolveDestinations(cfg, rule.Destinations)
}

// ResolveDestinations returns the enabled destinations matching ids (empty =
// all enabled destinations). Exported so callers outside this package can
// resolve a destination-ID list the same way rules do — the weekly digest
// (internal/api/digest.go) resolves NotificationConfig.Digest.Destinations
// through this exact function.
func ResolveDestinations(cfg models.NotificationConfig, ids []string) []models.NotifyDestination {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []models.NotifyDestination
	for _, d := range cfg.Destinations {
		if !d.Enabled {
			continue
		}
		if len(want) == 0 || want[d.ID] {
			out = append(out, d)
		}
	}
	return out
}

func evalRule(rule models.AlertRule, merged models.MergedStats, cur, prev map[string]string, reachable bool, trends TrendContext) bool {
	any := rule.Match == "any"
	for _, c := range rule.Conditions {
		ok := evalCondition(c, merged, cur, prev, reachable, trends)
		if any && ok {
			return true
		}
		if !any && !ok {
			return false
		}
	}
	// AND with all true → true; OR with none true → false.
	return !any
}

func evalCondition(c models.Condition, merged models.MergedStats, cur, prev map[string]string, reachable bool, trends TrendContext) bool {
	switch c.Field {
	case "reachable":
		return boolMatch(c.Op, reachable)
	case "freeleech_active":
		_, active := merged["active_event"]
		return boolMatch(c.Op, active)
	case "unread_mail", "unread_notifications":
		// Scraped presence flags ("true"/"false"; unset = unknown → not true).
		return boolMatch(c.Op, cur[c.Field] == "true")
	case "promoted", "demoted", "target_met":
		// One-shot event fields only ever match inside EvaluateEvent's
		// point-in-time firing — a level-triggered poll (Evaluate/Announce)
		// or a dry-run preview has no "event happening right now" to see.
		return false
	// Standing guards — polled predictive-decline signals (internal/stats's
	// DeclineSignals, threaded in via TrendContext). A nil signal always
	// reads false: no history/not declining is neither above nor below
	// anything, never a false positive.
	case "ratio_min_eta_days":
		return trendMatch(c, trends.RatioMinEtaDays)
	case "buffer_zero_eta_days":
		return trendMatch(c, trends.BufferZeroEtaDays)
	case "seed_size_drop_7d_pct":
		return trendMatch(c, trends.SeedSizeDropPct)
	case "seeding_drop_7d_pct":
		return trendMatch(c, trends.SeedingDropPct)
	case "goal_behind_pace":
		return boolMatch(c.Op, len(trends.GoalsBehind) > 0)
	}
	if c.Op == "changed" {
		p, hadPrev := prev[c.Field]
		return hadPrev && p != cur[c.Field]
	}
	// Numeric comparison.
	unit := numericFields[c.Field]
	have, ok := numericValue(c.Field, unit, cur[c.Field])
	if !ok {
		return false
	}
	want, ok := numericValue(c.Field, unit, c.Value)
	if !ok {
		return false
	}
	return compareNum(c.Op, have, want)
}

// trendMatch evaluates a standing-guard condition against its live trend
// signal. signal is already a plain float (no field/unit parsing needed) —
// only c.Value goes through numericValue.
func trendMatch(c models.Condition, signal *float64) bool {
	if signal == nil {
		return false
	}
	want, ok := numericValue(c.Field, "", c.Value)
	if !ok {
		return false
	}
	return compareNum(c.Op, *signal, want)
}

// compareNum applies a condition's op to two already-parsed numbers. Shared
// by evalCondition's merged-stat path and trendMatch's standing-guard path.
func compareNum(op string, have, want float64) bool {
	switch op {
	case "lt":
		return have < want
	case "lte":
		return have <= want
	case "gt":
		return have > want
	case "gte":
		return have >= want
	case "eq":
		return have == want
	case "ne":
		return have != want
	}
	return false
}

func boolMatch(op string, v bool) bool {
	switch op {
	case "is_true":
		return v
	case "is_false":
		return !v
	}
	return false
}

// numericValue parses a field/condition string into a comparable number in the
// field's unit (GiB for sizes, days for durations, raw otherwise).
func numericValue(field, unit, raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	switch unit {
	case "GiB":
		if g := parse.SizeToGiB(raw); g != nil {
			return *g, true
		}
		// Bare number → already GiB.
		return parse.AnyFloat(raw), true
	case "days":
		if s := parse.SeedTimeToSeconds(raw); s != nil {
			return *s / 86400.0, true
		}
		return parse.AnyFloat(raw), true
	default:
		return parse.AnyFloat(raw), true
	}
}

// rawValues flattens merged fields to plain strings (for changed-detection).
func rawValues(merged models.MergedStats) map[string]string {
	out := make(map[string]string, len(merged))
	for k, f := range merged {
		out[k] = fmt.Sprintf("%v", f.Value)
	}
	return out
}

// eventDescription renders the active event banner text plus its end time
// (e.g. "Global Freeleech (ends Jan 5, 2026 3:00 PM UTC)"). Used so event
// alerts carry the real announcement, not just "freeleech active".
func eventDescription(merged models.MergedStats) string {
	text := ""
	if f, ok := merged["active_event"]; ok {
		text = strings.TrimSpace(fmt.Sprintf("%v", f.Value))
	}
	if text == "" {
		text = "event active"
	}
	out := "event: " + text
	if f, ok := merged["active_event_ends_at"]; ok {
		if sec := toUnixSeconds(f.Value); sec > 0 {
			out += " (ends " + time.Unix(sec, 0).Format("Jan 2, 2006 3:04 PM MST") + ")"
		}
	}
	return out
}

// toUnixSeconds coerces a stored value (JSON number, int, or numeric string)
// to a unix-seconds int64. Returns 0 when it can't be parsed.
func toUnixSeconds(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
			return int64(f)
		}
	}
	return 0
}

// describeMatch builds a human description of why a rule fired.
func describeMatch(rule models.AlertRule, merged models.MergedStats, cur, prev map[string]string, reachable bool, trends TrendContext) string {
	parts := make([]string, 0, len(rule.Conditions))
	for _, c := range rule.Conditions {
		parts = append(parts, describeCondition(c, merged, cur, prev, trends))
	}
	return strings.Join(parts, conditionSep(rule))
}

// describeCondition renders one condition's human description. It is the
// single source of wording for BOTH live alert messages (describeMatch) and
// dry-run previews (describeDryRun) — change it here and both stay in sync.
func describeCondition(c models.Condition, merged models.MergedStats, cur, prev map[string]string, trends TrendContext) string {
	switch c.Field {
	case "reachable":
		if c.Op == "is_false" {
			return "tracker unreachable"
		}
		return "tracker reachable"
	case "freeleech_active":
		if c.Op == "is_true" {
			// Pass the actual banner text + end time through — these events
			// aren't always freeleech (open registration, double-upload, …).
			return eventDescription(merged)
		}
		return "no active event"
	case "unread_mail":
		if c.Op == "is_true" {
			return "unread mail waiting"
		}
		return "no unread mail"
	case "unread_notifications":
		if c.Op == "is_true" {
			return "unread notifications waiting"
		}
		return "no unread notifications"
	case "promoted":
		return "promoted (fires when a promotion happens)"
	case "demoted":
		return "demoted (fires when a demotion happens)"
	case "target_met":
		return "target met (fires when a target is reached)"
	case "ratio_min_eta_days":
		if trends.RatioMinEtaDays == nil {
			return "ratio not declining toward the minimum"
		}
		return fmt.Sprintf("ratio hits tracker minimum in ~%s days (%s %s)", formatDays(*trends.RatioMinEtaDays), opSymbol(c.Op), c.Value)
	case "buffer_zero_eta_days":
		if trends.BufferZeroEtaDays == nil {
			return "buffer not shrinking"
		}
		return fmt.Sprintf("buffer runs out in ~%s days (%s %s)", formatDays(*trends.BufferZeroEtaDays), opSymbol(c.Op), c.Value)
	case "seed_size_drop_7d_pct":
		if trends.SeedSizeDropPct == nil {
			return "seed size not dropping"
		}
		return fmt.Sprintf("seed size down %s%% over 7d (%s %s%%)", formatPct(*trends.SeedSizeDropPct), opSymbol(c.Op), c.Value)
	case "seeding_drop_7d_pct":
		if trends.SeedingDropPct == nil {
			return "seeding count not dropping"
		}
		return fmt.Sprintf("seeding count down %s%% over 7d (%s %s%%)", formatPct(*trends.SeedingDropPct), opSymbol(c.Op), c.Value)
	case "goal_behind_pace":
		if c.Op == "is_false" {
			return "on pace for all goals"
		}
		if len(trends.GoalsBehind) == 0 {
			return "not behind on any goal"
		}
		return "behind goal pace: " + strings.Join(trends.GoalsBehind, ", ")
	}
	if c.Op == "changed" {
		return fmt.Sprintf("%s changed: %s → %s", c.Field, prev[c.Field], cur[c.Field])
	}
	have := cur[c.Field]
	if have == "" {
		have = "—" // no data for this field (e.g. never scraped)
	}
	return fmt.Sprintf("%s %s %s %s", c.Field, have, opSymbol(c.Op), c.Value)
}

// formatDays rounds a standing-guard ETA to the nearest whole day — the
// underlying rate is noisy enough that fractional days would be false
// precision.
func formatDays(days float64) string {
	return strconv.Itoa(int(math.Round(days)))
}

// formatPct renders a standing-guard drop percentage to one decimal place.
func formatPct(pct float64) string {
	return strconv.FormatFloat(pct, 'f', 1, 64)
}

// conditionSep is the joiner between condition descriptions for a rule.
func conditionSep(rule models.AlertRule) string {
	if rule.Match == "any" {
		return " or "
	}
	return " and "
}

func opSymbol(op string) string {
	switch op {
	case "lt":
		return "<"
	case "lte":
		return "≤"
	case "gt":
		return ">"
	case "gte":
		return "≥"
	case "eq":
		return "="
	case "ne":
		return "≠"
	}
	return op
}
