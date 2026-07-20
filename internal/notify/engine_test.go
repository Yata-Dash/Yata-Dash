package notify

import (
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

func merged(fields map[string]any) models.MergedStats {
	m := models.MergedStats{}
	for k, v := range fields {
		m[k] = models.StatField{Value: v}
	}
	return m
}

func TestEvalCondition(t *testing.T) {
	m := merged(map[string]any{
		"ratio": "0.58", "buffer": "500 GiB", "warnings": "2", "active_event": "Global Freeleech",
	})
	cur := rawValues(m)
	prev := map[string]string{"group": "Member"}
	curWithGroup := map[string]string{"group": "Power User"}

	cases := []struct {
		name  string
		c     models.Condition
		cur   map[string]string
		reach bool
		want  bool
	}{
		{"ratio below threshold", models.Condition{Field: "ratio", Op: "lt", Value: "1.0"}, cur, true, true},
		{"ratio not above", models.Condition{Field: "ratio", Op: "gt", Value: "1.0"}, cur, true, false},
		{"buffer size compare GiB<TiB", models.Condition{Field: "buffer", Op: "lt", Value: "1 TiB"}, cur, true, true},
		{"warnings gt 0", models.Condition{Field: "warnings", Op: "gt", Value: "0"}, cur, true, true},
		{"freeleech active", models.Condition{Field: "freeleech_active", Op: "is_true"}, cur, true, true},
		{"reachable is_false when up", models.Condition{Field: "reachable", Op: "is_false"}, cur, true, false},
		{"reachable is_false when down", models.Condition{Field: "reachable", Op: "is_false"}, cur, false, true},
		{"group changed", models.Condition{Field: "group", Op: "changed"}, curWithGroup, true, true},
		{"group unchanged", models.Condition{Field: "group", Op: "changed"}, prev, true, false},
	}
	for _, tc := range cases {
		if got := evalCondition(tc.c, m, tc.cur, prev, tc.reach, TrendContext{}); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestEvalRuleMatchModes(t *testing.T) {
	m := merged(map[string]any{"ratio": "0.58", "warnings": "0"})
	cur := rawValues(m)
	prev := map[string]string{}

	ratioLow := models.Condition{Field: "ratio", Op: "lt", Value: "1.0"}
	warnHigh := models.Condition{Field: "warnings", Op: "gt", Value: "0"}

	all := models.AlertRule{Match: "all", Conditions: []models.Condition{ratioLow, warnHigh}}
	if evalRule(all, m, cur, prev, true, TrendContext{}) {
		t.Error("AND rule should be false (warnings not > 0)")
	}
	any := models.AlertRule{Match: "any", Conditions: []models.Condition{ratioLow, warnHigh}}
	if !evalRule(any, m, cur, prev, true, TrendContext{}) {
		t.Error("OR rule should be true (ratio < 1.0)")
	}
}

func TestEventDescriptionCarriesTextAndEnds(t *testing.T) {
	m := merged(map[string]any{
		"active_event":         "🌐 Global freeleech mode activated",
		"active_event_ends_at": float64(1772738619), // JSON numbers arrive as float64
	})
	got := eventDescription(m)
	if !strings.Contains(got, "Global freeleech mode activated") {
		t.Errorf("event text missing: %q", got)
	}
	if !strings.Contains(got, "(ends ") {
		t.Errorf("end time missing: %q", got)
	}

	// And the full rule description should use it for a freeleech is_true match.
	rule := models.AlertRule{Match: "all", Conditions: []models.Condition{{Field: "freeleech_active", Op: "is_true"}}}
	desc := describeMatch(rule, m, rawValues(m), map[string]string{}, true, TrendContext{})
	if !strings.Contains(desc, "Global freeleech mode activated") || !strings.Contains(desc, "(ends ") {
		t.Errorf("describeMatch should carry event text + ends, got %q", desc)
	}
}

// fakeCfg implements ConfigSource for the priming test.
type fakeCfg struct{ n models.NotificationConfig }

func (f fakeCfg) Notifications() models.NotificationConfig { return f.n }

func TestPrimingSuppressesFirstEval(t *testing.T) {
	rule := models.AlertRule{
		ID: "r1", Name: "low ratio", Enabled: true, Match: "all",
		Conditions:   []models.Condition{{Field: "ratio", Op: "lt", Value: "1.0"}},
		Destinations: []string{"d1"},
	}
	// No real destination → Send would fail, but priming must not even attempt
	// to fire. The destination is disabled so resolveDestinations is empty too.
	eng := New(fakeCfg{n: models.NotificationConfig{Rules: []models.AlertRule{rule}}}, nil)
	tr := models.Tracker{ID: "t1", Name: "T"}
	m := merged(map[string]any{"ratio": "0.5"})

	eng.Evaluate(tr, m, true, TrendContext{}) // priming pass
	if !eng.primed["t1"] {
		t.Fatal("tracker should be primed after first eval")
	}
	if !eng.firing["r1|t1"] {
		t.Fatal("firing state should record matched=true during priming")
	}
}

// TestUnreadMailEdgeTrigger walks the full unread-mail alert cycle: priming,
// mail arriving (fires), sitting unread (no re-fire), being read (re-arms).
func TestUnreadMailEdgeTrigger(t *testing.T) {
	rule := models.AlertRule{ID: "r1", Name: "Mail", Enabled: true, Match: "all",
		Conditions: []models.Condition{{Field: "unread_mail", Op: "is_true"}}}
	eng := New(fakeCfg{n: models.NotificationConfig{Rules: []models.AlertRule{rule}}}, nil)
	tr := models.Tracker{ID: "t1", Name: "T"}

	eng.Evaluate(tr, merged(map[string]any{"unread_mail": "false"}), true, TrendContext{}) // prime: all read
	if eng.firing["r1|t1"] {
		t.Fatal("no unread mail must not match")
	}
	eng.Evaluate(tr, merged(map[string]any{"unread_mail": "true"}), true, TrendContext{}) // mail arrives → rising edge
	if !eng.firing["r1|t1"] {
		t.Fatal("unread mail appearing must match (rising edge fires)")
	}
	eng.Evaluate(tr, merged(map[string]any{"unread_mail": "false"}), true, TrendContext{}) // read → clears, re-arms
	if eng.firing["r1|t1"] {
		t.Fatal("reading the mail must clear the match")
	}
	// Field absent entirely (unknown layout) → not true → still clear.
	eng.Evaluate(tr, merged(map[string]any{"ratio": "1.0"}), true, TrendContext{})
	if eng.firing["r1|t1"] {
		t.Fatal("unknown/unset flag must never read as unread")
	}
}

// TestEventFieldsNeverMatchInEvaluate guards evalCondition's explicit
// promoted/demoted/target_met case: a rule built entirely from event fields
// must never fire from the level-triggered poll — only EvaluateEvent (a real
// promotion/demotion/target crossing) can raise them.
func TestEventFieldsNeverMatchInEvaluate(t *testing.T) {
	rule := models.AlertRule{ID: "r1", Name: "Promo", Enabled: true, Match: "any",
		Conditions: []models.Condition{
			{Field: "promoted", Op: "is_true"},
			{Field: "demoted", Op: "is_true"},
			{Field: "target_met", Op: "is_true"},
		}}
	eng := New(fakeCfg{n: models.NotificationConfig{Rules: []models.AlertRule{rule}}}, nil)
	tr := models.Tracker{ID: "t1", Name: "T"}

	eng.Evaluate(tr, merged(map[string]any{"ratio": "1.0"}), true, TrendContext{})
	if eng.firing["r1|t1"] {
		t.Fatal("event fields must never match during a normal Evaluate poll")
	}
}

// TestEvaluateEventFiresRespectsScopeAndCooldown drives EvaluateEvent through
// a destination (a "generic" webhook with no URL — Send fails fast on
// "missing URL" with no real network call, but lastFired is recorded
// synchronously in send() before that goroutine even runs, so it's a safe,
// offline way to observe "this rule fired").
func TestEvaluateEventFiresRespectsScopeAndCooldown(t *testing.T) {
	dest := models.NotifyDestination{ID: "d1", Type: "generic", URL: "", Enabled: true}
	rule := models.AlertRule{
		ID: "r1", Name: "Promo", Enabled: true, Match: "all", TrackerIDs: []string{"t1"},
		Conditions:   []models.Condition{{Field: "promoted", Op: "is_true"}},
		CooldownMins: 10,
	}
	cfg := models.NotificationConfig{Destinations: []models.NotifyDestination{dest}, Rules: []models.AlertRule{rule}}
	eng := New(fakeCfg{n: cfg}, nil)

	trIn := models.Tracker{ID: "t1", Name: "In scope"}
	trOut := models.Tracker{ID: "t2", Name: "Out of scope"}
	ev := EventContext{Kind: "promoted", Detail: "promoted: User → Power User"}

	// Out-of-scope tracker: rule.Matches must block it — no fire recorded.
	eng.EvaluateEvent(trOut, merged(nil), ev, TrendContext{})
	if _, ok := eng.lastFired["r1|t2"]; ok {
		t.Fatal("rule scoped to t1 must not fire for an out-of-scope tracker")
	}

	// In-scope: fires once, recording lastFired for the cooldown.
	eng.EvaluateEvent(trIn, merged(nil), ev, TrendContext{})
	first, ok := eng.lastFired["r1|t1"]
	if !ok {
		t.Fatal("expected the rule to fire (and record lastFired) for the in-scope tracker")
	}

	// Immediately re-raising the same event must respect the 10-min cooldown.
	eng.EvaluateEvent(trIn, merged(nil), ev, TrendContext{})
	if eng.lastFired["r1|t1"] != first {
		t.Fatal("cooldown must suppress an immediate re-fire")
	}
}

// TestEvaluateEventMixedConditionsRequireBoth checks a rule combining an
// event field with a normal numeric condition: the event alone must not
// satisfy an "all" (AND) rule — the numeric condition still has to hold too.
func TestEvaluateEventMixedConditionsRequireBoth(t *testing.T) {
	dest := models.NotifyDestination{ID: "d1", Type: "generic", URL: "", Enabled: true}
	rule := models.AlertRule{
		ID: "r1", Name: "Promo+ratio", Enabled: true, Match: "all",
		Conditions: []models.Condition{{Field: "promoted", Op: "is_true"}, {Field: "ratio", Op: "lt", Value: "1.0"}},
	}
	cfg := models.NotificationConfig{Destinations: []models.NotifyDestination{dest}, Rules: []models.AlertRule{rule}}
	eng := New(fakeCfg{n: cfg}, nil)
	tr := models.Tracker{ID: "t1", Name: "T"}
	ev := EventContext{Kind: "promoted", Detail: "promoted: X → Y"}

	// ratio condition unmet — the promotion alone must not satisfy the AND rule.
	eng.EvaluateEvent(tr, merged(map[string]any{"ratio": "2.0"}), ev, TrendContext{})
	if _, ok := eng.lastFired["r1|t1"]; ok {
		t.Fatal("mixed AND rule must not fire when the non-event condition is unmet")
	}

	eng.EvaluateEvent(tr, merged(map[string]any{"ratio": "0.5"}), ev, TrendContext{})
	if _, ok := eng.lastFired["r1|t1"]; !ok {
		t.Fatal("mixed AND rule must fire once both the event and the numeric condition hold")
	}
}

// TestEvaluateTargetsEdgeTracking walks EvaluateTargets' per-row state
// machine: priming, the unmet→met fire, no re-fire while still met,
// met→unmet→met firing again, a brand-new row key priming silently even on
// an already-known tracker, and a removed-then-re-added key priming again
// (proving its old state was dropped, not remembered).
func TestEvaluateTargetsEdgeTracking(t *testing.T) {
	dest := models.NotifyDestination{ID: "d1", Type: "generic", URL: "", Enabled: true}
	rule := models.AlertRule{ID: "r1", Name: "Target met", Enabled: true, Match: "all",
		Conditions: []models.Condition{{Field: "target_met", Op: "is_true"}}}
	cfg := models.NotificationConfig{Destinations: []models.NotifyDestination{dest}, Rules: []models.AlertRule{rule}}
	eng := New(fakeCfg{n: cfg}, nil)
	tr := models.Tracker{ID: "t1", Name: "T"}
	m := merged(nil)

	// First sighting of the tracker/row — primes silently, never fires.
	eng.EvaluateTargets(tr, m, []TargetRow{{Key: "ratio", Label: "Ratio", Met: false}}, 0, 1, TrendContext{})
	if _, ok := eng.lastFired["r1|t1"]; ok {
		t.Fatal("first sighting must prime silently, never fire")
	}

	// unmet → met: fires exactly once.
	eng.EvaluateTargets(tr, m, []TargetRow{{Key: "ratio", Label: "Ratio", Met: true}}, 1, 1, TrendContext{})
	if _, ok := eng.lastFired["r1|t1"]; !ok {
		t.Fatal("unmet→met transition must fire")
	}
	delete(eng.lastFired, "r1|t1") // reset the observation point between assertions

	// Already-met row must not re-fire on a later pass.
	eng.EvaluateTargets(tr, m, []TargetRow{{Key: "ratio", Label: "Ratio", Met: true}}, 1, 1, TrendContext{})
	if _, ok := eng.lastFired["r1|t1"]; ok {
		t.Fatal("an already-met row must not re-fire")
	}

	// met → unmet → met fires again.
	eng.EvaluateTargets(tr, m, []TargetRow{{Key: "ratio", Label: "Ratio", Met: false}}, 0, 1, TrendContext{})
	eng.EvaluateTargets(tr, m, []TargetRow{{Key: "ratio", Label: "Ratio", Met: true}}, 1, 1, TrendContext{})
	if _, ok := eng.lastFired["r1|t1"]; !ok {
		t.Fatal("met→unmet→met must fire again")
	}
	delete(eng.lastFired, "r1|t1")

	// A brand-new row key appearing on an already-known tracker primes
	// silently even though it's already met.
	eng.EvaluateTargets(tr, m, []TargetRow{
		{Key: "ratio", Label: "Ratio", Met: true},
		{Key: "uploaded", Label: "Uploaded", Met: true},
	}, 2, 2, TrendContext{})
	if _, ok := eng.lastFired["r1|t1"]; ok {
		t.Fatal("a brand-new row key must prime silently even if already met")
	}

	// Dropping "uploaded" then re-adding it must prime again (not fire) —
	// proving its old met=true state was forgotten, not carried forward.
	eng.EvaluateTargets(tr, m, []TargetRow{{Key: "ratio", Label: "Ratio", Met: true}}, 1, 1, TrendContext{})
	eng.EvaluateTargets(tr, m, []TargetRow{
		{Key: "ratio", Label: "Ratio", Met: true},
		{Key: "uploaded", Label: "Uploaded", Met: true},
	}, 2, 2, TrendContext{})
	if _, ok := eng.lastFired["r1|t1"]; ok {
		t.Fatal("a re-added row key must prime silently, proving its old state was dropped")
	}
}

// TestDescribeEventDropsSiblingEventPlaceholders: in the seeded
// "promoted OR demoted" rule, a promotion's message must carry only the real
// event detail — never the sibling event field's "fires when…" placeholder —
// while a non-event condition still describes its live value.
func TestDescribeEventDropsSiblingEventPlaceholders(t *testing.T) {
	rule := models.AlertRule{ID: "r1", Name: "Promo", Match: "any",
		Conditions: []models.Condition{
			{Field: "promoted", Op: "is_true"},
			{Field: "demoted", Op: "is_true"},
		}}
	ev := EventContext{Kind: "promoted", Detail: "promoted: Member → Elite"}
	got := describeEvent(rule, ev, merged(nil), map[string]string{}, map[string]string{}, TrendContext{})
	if got != "promoted: Member → Elite" {
		t.Errorf("describeEvent = %q, want the bare event detail", got)
	}

	mixed := models.AlertRule{ID: "r2", Name: "Promo+ratio", Match: "all",
		Conditions: []models.Condition{
			{Field: "promoted", Op: "is_true"},
			{Field: "ratio", Op: "lt", Value: "1.0"},
		}}
	m := merged(map[string]any{"ratio": "0.5"})
	got = describeEvent(mixed, ev, m, rawValues(m), map[string]string{}, TrendContext{})
	if got != "promoted: Member → Elite and ratio 0.5 < 1.0" {
		t.Errorf("describeEvent mixed = %q", got)
	}
}

// TestStandingGuardConditions covers the four predictive-decline condition
// fields: a nil signal (no history, or not declining) always reads false —
// never a false positive — and a present signal follows normal numeric op
// semantics, including the lte boundary.
func TestStandingGuardConditions(t *testing.T) {
	eta := 9.0
	trends := TrendContext{RatioMinEtaDays: &eta}
	m, cur, prev := merged(nil), map[string]string{}, map[string]string{}

	cases := []struct {
		name string
		c    models.Condition
		want bool
	}{
		{"9 <= 14 matches", models.Condition{Field: "ratio_min_eta_days", Op: "lte", Value: "14"}, true},
		{"9 <= 9 boundary matches", models.Condition{Field: "ratio_min_eta_days", Op: "lte", Value: "9"}, true},
		{"9 <= 5 does not match", models.Condition{Field: "ratio_min_eta_days", Op: "lte", Value: "5"}, false},
	}
	for _, tc := range cases {
		if got := evalCondition(tc.c, m, cur, prev, true, trends); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}

	// Nil signal is always false, regardless of op — no history/not-declining
	// is neither above nor below anything.
	empty := TrendContext{}
	for _, c := range []models.Condition{
		{Field: "ratio_min_eta_days", Op: "lte", Value: "14"},
		{Field: "buffer_zero_eta_days", Op: "lte", Value: "14"},
		{Field: "seed_size_drop_7d_pct", Op: "gte", Value: "20"},
		{Field: "seeding_drop_7d_pct", Op: "gte", Value: "20"},
	} {
		if evalCondition(c, m, cur, prev, true, empty) {
			t.Errorf("%s with nil signal must not match", c.Field)
		}
	}
}

// TestStandingGuardDescriptions checks describeCondition's wording for all
// four standing-guard fields, with and without a live signal.
func TestStandingGuardDescriptions(t *testing.T) {
	eta, drop := 9.0, 23.4
	trends := TrendContext{
		RatioMinEtaDays:   &eta,
		BufferZeroEtaDays: &eta,
		SeedSizeDropPct:   &drop,
		SeedingDropPct:    &drop,
	}
	m, cur, prev := merged(nil), map[string]string{}, map[string]string{}

	cases := []struct {
		c    models.Condition
		want string
	}{
		{models.Condition{Field: "ratio_min_eta_days", Op: "lte", Value: "14"}, "ratio hits tracker minimum in ~9 days (≤ 14)"},
		{models.Condition{Field: "buffer_zero_eta_days", Op: "lte", Value: "14"}, "buffer runs out in ~9 days (≤ 14)"},
		{models.Condition{Field: "seed_size_drop_7d_pct", Op: "gte", Value: "20"}, "seed size down 23.4% over 7d (≥ 20%)"},
		{models.Condition{Field: "seeding_drop_7d_pct", Op: "gte", Value: "20"}, "seeding count down 23.4% over 7d (≥ 20%)"},
	}
	for _, tc := range cases {
		if got := describeCondition(tc.c, m, cur, prev, trends); got != tc.want {
			t.Errorf("describeCondition(%s) = %q, want %q", tc.c.Field, got, tc.want)
		}
	}

	noSignalCases := []struct {
		c    models.Condition
		want string
	}{
		{models.Condition{Field: "ratio_min_eta_days", Op: "lte", Value: "14"}, "ratio not declining toward the minimum"},
		{models.Condition{Field: "buffer_zero_eta_days", Op: "lte", Value: "14"}, "buffer not shrinking"},
		{models.Condition{Field: "seed_size_drop_7d_pct", Op: "gte", Value: "20"}, "seed size not dropping"},
		{models.Condition{Field: "seeding_drop_7d_pct", Op: "gte", Value: "20"}, "seeding count not dropping"},
	}
	for _, tc := range noSignalCases {
		if got := describeCondition(tc.c, m, cur, prev, TrendContext{}); got != tc.want {
			t.Errorf("describeCondition(%s, no signal) = %q, want %q", tc.c.Field, got, tc.want)
		}
	}
}

// TestEvaluateEventSeesTrendContext confirms TrendContext threads through
// evaluateEventLocked into a rule's non-event conditions — a standing guard
// mixed with an event field on the same AND rule only fires once both hold,
// mirroring TestEvaluateEventMixedConditionsRequireBoth for the ratio field.
func TestEvaluateEventSeesTrendContext(t *testing.T) {
	dest := models.NotifyDestination{ID: "d1", Type: "generic", URL: "", Enabled: true}
	rule := models.AlertRule{
		ID: "r1", Name: "Promo+guard", Enabled: true, Match: "all",
		Conditions: []models.Condition{
			{Field: "promoted", Op: "is_true"},
			{Field: "ratio_min_eta_days", Op: "lte", Value: "14"},
		},
	}
	cfg := models.NotificationConfig{Destinations: []models.NotifyDestination{dest}, Rules: []models.AlertRule{rule}}
	eng := New(fakeCfg{n: cfg}, nil)
	tr := models.Tracker{ID: "t1", Name: "T"}
	ev := EventContext{Kind: "promoted", Detail: "promoted: X → Y"}

	// No trend signal — the guard condition is false, so the AND rule must
	// not fire even though the promotion happened.
	eng.EvaluateEvent(tr, merged(nil), ev, TrendContext{})
	if _, ok := eng.lastFired["r1|t1"]; ok {
		t.Fatal("mixed AND rule must not fire when the guard has no signal")
	}

	eta := 9.0
	eng.EvaluateEvent(tr, merged(nil), ev, TrendContext{RatioMinEtaDays: &eta})
	if _, ok := eng.lastFired["r1|t1"]; !ok {
		t.Fatal("mixed AND rule must fire once the event and the guard both hold")
	}
}

// TestGoalBehindPaceCondition covers goal_behind_pace: is_true matches only
// when GoalsBehind is non-empty, is_false only when it's empty — a polled
// bool condition like reachable/freeleech_active, not a numeric threshold.
func TestGoalBehindPaceCondition(t *testing.T) {
	m, cur, prev := merged(nil), map[string]string{}, map[string]string{}

	behind := TrendContext{GoalsBehind: []string{"Uploaded needs 12.4 GiB/day"}}
	onPace := TrendContext{}

	cases := []struct {
		name   string
		op     string
		trends TrendContext
		want   bool
	}{
		{"is_true matches when behind on something", "is_true", behind, true},
		{"is_true does not match when on pace for everything", "is_true", onPace, false},
		{"is_false matches when on pace for everything", "is_false", onPace, true},
		{"is_false does not match when behind on something", "is_false", behind, false},
	}
	for _, tc := range cases {
		c := models.Condition{Field: "goal_behind_pace", Op: tc.op}
		if got := evalCondition(c, m, cur, prev, true, tc.trends); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestGoalBehindPaceDescription checks describeCondition's three wordings:
// is_true with a signal joins the behind entries; is_true with no signal
// reads "not behind on any goal"; is_false always reads "on pace for all
// goals" regardless of the live signal (mirrors freeleech_active's is_false
// wording, which doesn't re-check the actual state either).
func TestGoalBehindPaceDescription(t *testing.T) {
	m, cur, prev := merged(nil), map[string]string{}, map[string]string{}
	behind := TrendContext{GoalsBehind: []string{"Uploaded needs 12.4 GiB/day", "Seed Size overdue"}}

	cases := []struct {
		name   string
		c      models.Condition
		trends TrendContext
		want   string
	}{
		{
			"is_true with signal joins entries",
			models.Condition{Field: "goal_behind_pace", Op: "is_true"}, behind,
			"behind goal pace: Uploaded needs 12.4 GiB/day, Seed Size overdue",
		},
		{
			"is_true with no signal",
			models.Condition{Field: "goal_behind_pace", Op: "is_true"}, TrendContext{},
			"not behind on any goal",
		},
		{
			"is_false always reads on-pace",
			models.Condition{Field: "goal_behind_pace", Op: "is_false"}, behind,
			"on pace for all goals",
		},
	}
	for _, tc := range cases {
		if got := describeCondition(tc.c, m, cur, prev, tc.trends); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
