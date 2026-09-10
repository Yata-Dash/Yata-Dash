package notify

import (
	"strings"
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

// Notifications mirrors what config.Manager guarantees: the notification centre
// is always in the destination list. An engine test that left it out would be
// testing a config the app cannot produce.
func (s staticCfg) Notifications() models.NotificationConfig {
	out := s.cfg
	out.Destinations = append([]models.NotifyDestination{{
		ID: models.InAppDestinationID, Name: "Notification centre",
		Type: models.InAppDestinationType, Enabled: true,
	}}, out.Destinations...)
	return out
}

// The premise of the whole panel: with no destination configured, an alert used
// to be evaluated, matched and thrown away. It must now be recorded.
func TestRecordsWithNoWebhook(t *testing.T) {
	rec := &fakeRecorder{}
	e := New(staticCfg{models.NotificationConfig{}}, nil)
	e.SetRecorder(rec)

	rule := models.AlertRule{ID: "r1", Name: "Ratio falling"}
	tr := models.Tracker{ID: "t1", Name: "Aither"}
	e.send(e.cfg.Notifications(), rule, tr, "r1|t1", "ratio 0.8 below 1.0")

	if len(rec.got) != 1 {
		t.Fatalf("recorded %d alerts, want 1 — an alert with no webhook is dropped", len(rec.got))
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
func TestCooldownAppliesWithNoWebhook(t *testing.T) {
	rec := &fakeRecorder{}
	e := New(staticCfg{models.NotificationConfig{}}, nil)
	e.SetRecorder(rec)

	rule := models.AlertRule{ID: "r1", Name: "Ratio falling", CooldownMins: 60}
	tr := models.Tracker{ID: "t1", Name: "Aither"}
	for i := 0; i < 3; i++ {
		e.send(e.cfg.Notifications(), rule, tr, "r1|t1", "ratio low")
	}
	if len(rec.got) != 1 {
		t.Errorf("recorded %d alerts, want 1 — cooldown must hold without a webhook", len(rec.got))
	}
}

// No recorder is the pre-existing behaviour, and must stay working.
func TestSendWithoutRecorderIsSafe(t *testing.T) {
	e := New(staticCfg{models.NotificationConfig{}}, nil)
	e.send(e.cfg.Notifications(),
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

// Routing is per rule: the centre is a destination, so a rule can go in-app
// only, webhook only, or (by picking nothing) both.
func TestPerRuleRouting(t *testing.T) {
	hook := models.NotifyDestination{ID: "hook1", Name: "Hook", Type: "generic",
		URL: "http://127.0.0.1:9/none", Enabled: true}
	cond := []models.Condition{{Field: "ratio", Op: "lt", Value: "999"}}
	cfg := models.NotificationConfig{
		Destinations: []models.NotifyDestination{hook},
		Rules: []models.AlertRule{
			{ID: "a", Name: "Default", Enabled: true, Conditions: cond},
			{ID: "b", Name: "Centre only", Enabled: true, Conditions: cond,
				Destinations: []string{models.InAppDestinationID}},
			{ID: "c", Name: "Webhook only", Enabled: true, Conditions: cond,
				Destinations: []string{"hook1"}},
		},
	}
	rec := &fakeRecorder{}
	e := New(staticCfg{cfg}, nil)
	e.SetRecorder(rec)

	tr := models.Tracker{ID: "t1", Name: "Demo"}
	merged := models.MergedStats{"ratio": {Value: "1.5"}}
	e.Evaluate(tr, merged, true, TrendContext{}) // priming: standing thresholds

	got := map[string]bool{}
	for _, a := range rec.got {
		got[a.ruleName] = true
	}
	if !got["Default"] || !got["Centre only"] {
		t.Errorf("recorded %v, want Default and Centre only", got)
	}
	if got["Webhook only"] {
		t.Error("a webhook-only rule reached the centre")
	}
}

// The same routing on the ordinary fire path, not just priming.
func TestPerRuleRoutingOnFire(t *testing.T) {
	cfg := models.NotificationConfig{
		Destinations: []models.NotifyDestination{{ID: "hook1", Name: "Hook", Type: "generic",
			URL: "http://127.0.0.1:9/none", Enabled: true}},
	}
	rec := &fakeRecorder{}
	e := New(staticCfg{cfg}, nil)
	e.SetRecorder(rec)

	tr := models.Tracker{ID: "t1", Name: "Demo"}
	e.send(e.cfg.Notifications(),
		models.AlertRule{ID: "c", Name: "Webhook only", Destinations: []string{"hook1"}},
		tr, "c|t1", "detail")
	if len(rec.got) != 0 {
		t.Errorf("recorded %+v, want nothing — the rule names a webhook only", rec.got)
	}

	e.send(e.cfg.Notifications(),
		models.AlertRule{ID: "b", Name: "Centre only", Destinations: []string{models.InAppDestinationID}},
		tr, "b|t1", "detail")
	if len(rec.got) != 1 {
		t.Errorf("recorded %d, want 1", len(rec.got))
	}
}

// Disabling the centre turns it off everywhere: ResolveDestinations drops
// disabled destinations, so a rule that names it resolves to nothing.
func TestDisabledCentreStopsRecording(t *testing.T) {
	cfg := models.NotificationConfig{Destinations: []models.NotifyDestination{{
		ID: models.InAppDestinationID, Name: "Notification centre",
		Type: models.InAppDestinationType, Enabled: false,
	}}}
	rec := &fakeRecorder{}
	// Bypasses staticCfg's helper, which always enables the centre.
	e := New(rawCfg{cfg}, nil)
	e.SetRecorder(rec)
	e.send(cfg, models.AlertRule{ID: "a", Name: "Any"}, models.Tracker{ID: "t1", Name: "Demo"}, "a|t1", "d")
	if len(rec.got) != 0 {
		t.Errorf("recorded %+v with the centre disabled", rec.got)
	}
}

type rawCfg struct{ cfg models.NotificationConfig }

func (r rawCfg) Notifications() models.NotificationConfig { return r.cfg }

// Both were header banners dismissed to sessionStorage, so they came back every
// session and were re-dismissed forever. As conditions they name the tracker,
// respect a cooldown, and stay until read.
func TestScrapeSignalConditions(t *testing.T) {
	cfg := models.NotificationConfig{Rules: []models.AlertRule{
		{ID: "r1", Name: "Daily scrape limit reached", Enabled: true,
			Conditions: []models.Condition{{Field: "scrape_limited", Op: "is_true"}}},
		{ID: "r2", Name: "Session cookie expired", Enabled: true,
			Conditions: []models.Condition{{Field: "cookie_expired", Op: "is_true"}}},
	}}
	rec := &fakeRecorder{}
	e := New(staticCfg{cfg}, nil)
	e.SetRecorder(rec)

	tr := models.Tracker{ID: "t1", Name: "OnlyEncodes+"}
	// Neither signal set: both rules must stay quiet, so a healthy tracker is
	// never told it has a problem.
	e.Evaluate(tr, models.MergedStats{}, true, TrendContext{})
	if len(rec.got) != 0 {
		t.Fatalf("recorded %+v with no signals set", rec.got)
	}

	// The limit is reached. is_true is a state predicate, not a threshold, so
	// priming will not record it — it fires on the edge, which is what this is.
	e.Evaluate(tr, models.MergedStats{}, true, TrendContext{ScrapeLimited: true})
	if len(rec.got) != 1 || rec.got[0].ruleName != "Daily scrape limit reached" {
		t.Fatalf("recorded %+v, want the scrape-limit rule", rec.got)
	}
	if !strings.Contains(rec.got[0].body, "OnlyEncodes+") {
		t.Errorf("body = %q, want the tracker named", rec.got[0].body)
	}

	// The cookie goes stale: its kind belongs in the message, since "http_403"
	// and "login_page" call for different things.
	e.Evaluate(tr, models.MergedStats{}, true,
		TrendContext{ScrapeLimited: true, CookieExpiredKind: "login_page"})
	if len(rec.got) != 2 {
		t.Fatalf("recorded %d, want the cookie rule as well", len(rec.got))
	}
	if !strings.Contains(rec.got[1].body, "login_page") {
		t.Errorf("body = %q, want the failure kind", rec.got[1].body)
	}
}

// A tracker that was already at its scrape cap, or whose cookie had already
// gone stale, when Yata started. Priming is meant to stop a restart re-blasting
// webhooks about things that did not just change; it was also swallowing these
// two, which are standing problems and exactly what the panel is a worklist
// for. Before this they only ever appeared if the problem cleared and returned.
func TestStandingScrapeProblemsSurvivePriming(t *testing.T) {
	cfg := models.NotificationConfig{Rules: []models.AlertRule{
		{ID: "r1", Name: "Daily scrape limit reached", Enabled: true,
			Conditions: []models.Condition{{Field: "scrape_limited", Op: "is_true"}}},
		{ID: "r2", Name: "Session cookie expired", Enabled: true,
			Conditions: []models.Condition{{Field: "cookie_expired", Op: "is_true"}}},
	}}
	rec := &fakeRecorder{}
	e := New(staticCfg{cfg}, nil)
	e.SetRecorder(rec)

	tr := models.Tracker{ID: "t1", Name: "OnlyEncodes+"}
	// The FIRST pass for this tracker, with both already true.
	e.Evaluate(tr, models.MergedStats{}, true,
		TrendContext{ScrapeLimited: true, CookieExpiredKind: "login_page"})

	if len(rec.got) != 2 {
		t.Fatalf("recorded %d on the priming pass, want both standing problems", len(rec.got))
	}
	names := rec.got[0].ruleName + "|" + rec.got[1].ruleName
	if !strings.Contains(names, "Daily scrape limit reached") || !strings.Contains(names, "Session cookie expired") {
		t.Errorf("recorded %q", names)
	}
}

// The other half of the same rule. "reachable is_true" is true of every healthy
// tracker, so priming on it filled the panel with "Tracker Back" for everything
// that was working — which is why the operator test exists at all. Widening it
// for genuine problem states must not let that back in.
func TestHealthyStatesStillDoNotPrime(t *testing.T) {
	cfg := models.NotificationConfig{Rules: []models.AlertRule{
		{ID: "r1", Name: "Tracker Back", Enabled: true,
			Conditions: []models.Condition{{Field: "reachable", Op: "is_true"}}},
	}}
	rec := &fakeRecorder{}
	e := New(staticCfg{cfg}, nil)
	e.SetRecorder(rec)

	e.Evaluate(models.Tracker{ID: "t1", Name: "Aither"}, models.MergedStats{}, true, TrendContext{})
	if len(rec.got) != 0 {
		t.Fatalf("recorded %+v on the priming pass for a healthy tracker", rec.got)
	}
}
