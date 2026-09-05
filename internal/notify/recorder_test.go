package notify

import (
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

type capturedAlert struct {
	ruleID, ruleName, trackerID, trackerName, title, body string
}

type fakeRecorder struct{ got []capturedAlert }

func (f *fakeRecorder) RecordAlert(ruleID, ruleName, trackerID, trackerName, title, body string) {
	f.got = append(f.got, capturedAlert{ruleID, ruleName, trackerID, trackerName, title, body})
}

type staticCfg struct{ cfg models.NotificationConfig }

func (s staticCfg) Notifications() models.NotificationConfig { return s.cfg }

// The premise of the whole panel: with no destination configured, an alert used
// to be evaluated, matched and thrown away. It must now be recorded.
func TestRecordsWithNoDestination(t *testing.T) {
	rec := &fakeRecorder{}
	e := New(staticCfg{models.NotificationConfig{}}, nil)
	e.SetRecorder(rec)

	rule := models.AlertRule{ID: "r1", Name: "Ratio falling"}
	tr := models.Tracker{ID: "t1", Name: "Aither"}
	e.send(models.NotificationConfig{}, rule, tr, "r1|t1", "ratio 0.8 below 1.0")

	if len(rec.got) != 1 {
		t.Fatalf("recorded %d alerts, want 1 — a destination-less alert is dropped", len(rec.got))
	}
	a := rec.got[0]
	if a.ruleID != "r1" || a.ruleName != "Ratio falling" || a.trackerID != "t1" || a.trackerName != "Aither" {
		t.Errorf("identity = %+v", a)
	}
	// The panel must show the same words a webhook would, or "what Discord
	// said" and "what the panel says" drift.
	if a.title != "Yata alert: Ratio falling" {
		t.Errorf("title = %q", a.title)
	}
	if a.body != "Aither — ratio 0.8 below 1.0" {
		t.Errorf("body = %q", a.body)
	}
}

// Cooldown used to be stamped only when a destination existed, so it was not
// tracked at all for a destination-less user — invisible while the alert was
// being dropped, and one row per poll once it is not.
func TestCooldownAppliesWithNoDestination(t *testing.T) {
	rec := &fakeRecorder{}
	e := New(staticCfg{models.NotificationConfig{}}, nil)
	e.SetRecorder(rec)

	rule := models.AlertRule{ID: "r1", Name: "Ratio falling", CooldownMins: 60}
	tr := models.Tracker{ID: "t1", Name: "Aither"}
	for i := 0; i < 3; i++ {
		e.send(models.NotificationConfig{}, rule, tr, "r1|t1", "ratio low")
	}
	if len(rec.got) != 1 {
		t.Errorf("recorded %d alerts, want 1 — cooldown must hold without a destination", len(rec.got))
	}
}

// No recorder is the pre-existing behaviour, and must stay working.
func TestSendWithoutRecorderIsSafe(t *testing.T) {
	e := New(staticCfg{models.NotificationConfig{}}, nil)
	e.send(models.NotificationConfig{},
		models.AlertRule{ID: "r1", Name: "x"}, models.Tracker{ID: "t1", Name: "y"}, "r1|t1", "detail")
}

// Every alert path converges on send, so recording there catches the one-shot
// event paths too — no per-path work.
func TestEventPathIsRecorded(t *testing.T) {
	rec := &fakeRecorder{}
	cfg := models.NotificationConfig{Rules: []models.AlertRule{{
		ID: "r1", Name: "Promoted", Enabled: true,
		Conditions: []models.Condition{{Field: "promoted"}},
	}}}
	e := New(staticCfg{cfg}, nil)
	e.SetRecorder(rec)

	e.EvaluateEvent(models.Tracker{ID: "t1", Name: "Aither"}, models.MergedStats{},
		EventContext{Kind: "promoted", Detail: "promoted: Seeder → Power User"}, TrendContext{})

	if len(rec.got) != 1 {
		t.Fatalf("recorded %d alerts, want 1 from the event path", len(rec.got))
	}
	if rec.got[0].body != "Aither — promoted: Seeder → Power User" {
		t.Errorf("body = %q", rec.got[0].body)
	}
}

// A condition already true when Yata starts must reach the panel. The engine
// primes silently so a restart does not re-blast webhooks, which meant a
// standing problem — a login deadline already close — was recorded nowhere and
// would not surface until the user fixed it and let it lapse again.
func TestStandingConditionRecordedOnPriming(t *testing.T) {
	rec := &fakeRecorder{}
	cfg := models.NotificationConfig{Rules: []models.AlertRule{{
		ID: "r1", Name: "Login required soon", Enabled: true,
		Conditions: []models.Condition{{Field: "login_days_remaining", Op: "lte", Value: "7"}},
	}}}
	e := New(staticCfg{cfg}, nil)
	e.SetRecorder(rec)

	tr := models.Tracker{ID: "t1", Name: "Zenith"}
	merged := models.MergedStats{"login_days_remaining": {Value: "3"}}

	e.Evaluate(tr, merged, true, TrendContext{}) // the priming pass
	if len(rec.got) != 1 {
		t.Fatalf("recorded %d, want 1 — a standing condition must reach the panel", len(rec.got))
	}
	if rec.got[0].ruleName != "Login required soon" || rec.got[0].trackerName != "Zenith" {
		t.Errorf("alert = %+v", rec.got[0])
	}

	// Still true on the next pass: no edge, so nothing new.
	e.Evaluate(tr, merged, true, TrendContext{})
	if len(rec.got) != 1 {
		t.Errorf("recorded %d after a second pass, want 1", len(rec.got))
	}
}

// Priming records but must not notify: a webhook says "this just changed", and
// a condition that was already true when Yata started did not.
func TestPrimingDoesNotSendWebhooks(t *testing.T) {
	rec := &fakeRecorder{}
	cfg := models.NotificationConfig{
		Destinations: []models.NotifyDestination{{
			ID: "d1", Name: "test", Enabled: true, Type: "generic",
			URL: "http://127.0.0.1:1/never",
		}},
		Rules: []models.AlertRule{{
			ID: "r1", Name: "Ratio", Enabled: true,
			Conditions: []models.Condition{{Field: "ratio", Op: "lt", Value: "1"}},
		}},
	}
	e := New(staticCfg{cfg}, nil)
	e.SetRecorder(rec)

	tr := models.Tracker{ID: "t1", Name: "Aither"}
	e.Evaluate(tr, models.MergedStats{"ratio": {Value: "0.5"}}, true, TrendContext{})

	if len(rec.got) != 1 {
		t.Fatalf("recorded %d, want 1", len(rec.got))
	}
	// The dispatch path stamps lastFired; the record-only path must not, since
	// nothing was sent and the next real edge should still be free to fire.
	if _, stamped := e.lastFired["r1|t1"]; stamped {
		t.Error("priming stamped lastFired — nothing was sent")
	}
}

// A condition false at startup and true later is a real edge: it notifies, and
// it is recorded exactly once.
func TestEdgeAfterPrimingStillFires(t *testing.T) {
	rec := &fakeRecorder{}
	cfg := models.NotificationConfig{Rules: []models.AlertRule{{
		ID: "r1", Name: "Ratio", Enabled: true,
		Conditions: []models.Condition{{Field: "ratio", Op: "lt", Value: "1"}},
	}}}
	e := New(staticCfg{cfg}, nil)
	e.SetRecorder(rec)

	tr := models.Tracker{ID: "t1", Name: "Aither"}
	e.Evaluate(tr, models.MergedStats{"ratio": {Value: "2.0"}}, true, TrendContext{}) // primes, no match
	if len(rec.got) != 0 {
		t.Fatalf("recorded %d on a non-matching prime, want 0", len(rec.got))
	}
	e.Evaluate(tr, models.MergedStats{"ratio": {Value: "0.5"}}, true, TrendContext{}) // the edge
	if len(rec.got) != 1 {
		t.Fatalf("recorded %d after the edge, want 1", len(rec.got))
	}
	if _, stamped := e.lastFired["r1|t1"]; !stamped {
		t.Error("a real fire did not stamp lastFired")
	}
}

// Priming records a THRESHOLD condition, which describes a measurement that
// stays true while the problem lasts. It must not record a state predicate:
// "reachable is true" matches every healthy tracker, so the panel would open
// with a row per tracker saying nothing is wrong.
func TestPrimingOnlyRecordsStandingThresholds(t *testing.T) {
	rec := &fakeRecorder{}
	cfg := models.NotificationConfig{Rules: []models.AlertRule{
		{ID: "r1", Name: "Login required soon", Enabled: true,
			Conditions: []models.Condition{{Field: "login_days_remaining", Op: "lte", Value: "7"}}},
		{ID: "r2", Name: "Tracker back", Enabled: true,
			Conditions: []models.Condition{{Field: "reachable", Op: "is_true"}}},
	}}
	e := New(staticCfg{cfg}, nil)
	e.SetRecorder(rec)

	e.Evaluate(models.Tracker{ID: "t1", Name: "Zenith"},
		models.MergedStats{"login_days_remaining": {Value: "3"}}, true, TrendContext{})

	if len(rec.got) != 1 {
		t.Fatalf("recorded %d, want only the threshold rule: %+v", len(rec.got), rec.got)
	}
	if rec.got[0].ruleName != "Login required soon" {
		t.Errorf("recorded %q", rec.got[0].ruleName)
	}
}

// "Tracker back" must still fire normally once primed — narrowing what priming
// records must not narrow what actually alerts.
func TestStatePredicateStillFiresOnItsEdge(t *testing.T) {
	rec := &fakeRecorder{}
	cfg := models.NotificationConfig{Rules: []models.AlertRule{{
		ID: "r2", Name: "Tracker back", Enabled: true,
		Conditions: []models.Condition{{Field: "reachable", Op: "is_true"}},
	}}}
	e := New(staticCfg{cfg}, nil)
	e.SetRecorder(rec)

	tr := models.Tracker{ID: "t1", Name: "Unwalled"}
	e.Evaluate(tr, models.MergedStats{}, false, TrendContext{}) // primes: unreachable
	e.Evaluate(tr, models.MergedStats{}, true, TrendContext{})  // comes back
	if len(rec.got) != 1 {
		t.Fatalf("recorded %d, want 1 on the real edge", len(rec.got))
	}
}
