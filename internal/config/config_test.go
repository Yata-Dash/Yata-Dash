package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// TestFreshInstallSeedsDefaultAlertRules: a brand-new config.json (no
// destinations, no rules — nobody has touched Alerts yet) gets every seeding
// batch on its very first load, and the version counter stops it happening
// again on a later load.
func TestFreshInstallSeedsDefaultAlertRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n := m.Notifications()
	if !n.SeededDefaultRules {
		t.Fatal("expected SeededDefaultRules to be set after a fresh-install load")
	}
	if n.SeedVersion != seedVersion {
		t.Fatalf("expected SeedVersion %d after a fresh-install load, got %d", seedVersion, n.SeedVersion)
	}
	if len(n.Rules) != 7 {
		t.Fatalf("expected 7 seeded rules, got %d: %+v", len(n.Rules), n.Rules)
	}
	var haveEvents, haveTarget, haveGuard, haveLogin, haveKey bool
	for _, r := range n.Rules {
		if !r.Enabled {
			t.Errorf("seeded rule %q must be enabled", r.Name)
		}
		switch r.Name {
		case "Promotions & demotions":
			haveEvents = true
			if r.Match != "any" || len(r.Conditions) != 2 ||
				r.Conditions[0].Field != "promoted" || r.Conditions[1].Field != "demoted" {
				t.Errorf("Promotions & demotions rule malformed: %+v", r)
			}
		case "Target met":
			haveTarget = true
			if len(r.Conditions) != 1 || r.Conditions[0].Field != "target_met" {
				t.Errorf("Target met rule malformed: %+v", r)
			}
		case "Ratio approaching minimum":
			haveGuard = true
			if r.Match != "all" || len(r.Conditions) != 1 ||
				r.Conditions[0].Field != "ratio_min_eta_days" || r.Conditions[0].Op != "lte" ||
				r.Conditions[0].Value != "14" || r.CooldownMins != 1440 {
				t.Errorf("Ratio approaching minimum rule malformed: %+v", r)
			}
		case "Login required soon":
			haveLogin = true
			if r.Match != "all" || len(r.Conditions) != 1 ||
				r.Conditions[0].Field != "login_days_remaining" || r.Conditions[0].Op != "lte" ||
				r.Conditions[0].Value != "7" || r.CooldownMins != 1440 {
				t.Errorf("Login required soon rule malformed: %+v", r)
			}
		case "API key expiring":
			haveKey = true
			if r.Match != "all" || len(r.Conditions) != 1 ||
				r.Conditions[0].Field != "api_key_expiry_days" || r.Conditions[0].Op != "lte" ||
				r.Conditions[0].Value != "14" || r.CooldownMins != 1440 {
				t.Errorf("API key expiring rule malformed: %+v", r)
			}
		}
	}
	if !haveEvents || !haveTarget || !haveGuard || !haveLogin || !haveKey {
		t.Fatalf("missing an expected seeded rule: %+v", n.Rules)
	}

	// Re-opening the same (now-persisted) config must not re-seed.
	m2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m2.Notifications().Rules); got != 7 {
		t.Fatalf("second load re-seeded: got %d rules, want 7", got)
	}
}

// TestExistingSetupIsNotSeededWithStarters: a config.json with a user-created
// rule already in place must NOT get batch 1's starter rules injected — that
// setup was deliberate. Batch 2 is a different case and is asserted below:
// its rules guard fields that did not exist when the user built their setup.
func TestExistingSetupIsNotSeededWithStarters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	raw := `{
		"server": {"host": "0.0.0.0", "port": 8420},
		"trackers": [],
		"settings": {},
		"notifications": {
			"destinations": [],
			"rules": [{"id": "user1", "name": "My rule", "enabled": true, "match": "all",
				"conditions": [{"field": "ratio", "op": "lt", "value": "1.0"}]}]
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n := m.Notifications()
	if !n.SeededDefaultRules {
		t.Fatal("expected the flag to be set even when nothing was injected")
	}
	if n.Rules[0].Name != "My rule" {
		t.Fatalf("expected the user's existing rule to be kept first, got %+v", n.Rules)
	}
	for _, r := range n.Rules {
		switch r.Name {
		case "Promotions & demotions", "Target met", "Ratio approaching minimum":
			t.Fatalf("batch 1 starter rule %q injected over an existing setup", r.Name)
		}
	}
	// Batch 2 DOES apply here, deliberately: login_days_remaining and
	// api_key_expiry_days did not exist when this user built their rules, so
	// withholding the guards would leave exactly the long-standing accounts
	// most at risk with no warning at all.
	// …and so does batch 4, for the same reason: the scrape-limit and
	// expired-cookie warnings used to be banners this user could not have
	// written a rule for.
	if len(n.Rules) != 5 {
		t.Fatalf("expected the user's rule plus the four later seeded rules, got %+v", n.Rules)
	}

	// A second load is a pure no-op (the counter has caught up).
	m2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m2.Notifications().Rules); got != 5 {
		t.Fatalf("second load changed rule count: got %d, want 3", got)
	}
}

// TestAlreadySeededInstallGetsOnlyTheNewBatch: an install carrying the
// pre-counter seeded_default_rules flag has had batch 1 and nothing else. It
// must be migrated to version 1 and then given batch 2 exactly once —
// including the starter rules it deleted staying deleted.
func TestAlreadySeededInstallGetsOnlyTheNewBatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
		"server": {"host": "0.0.0.0", "port": 8420},
		"trackers": [],
		"settings": {},
		"notifications": {
			"destinations": [],
			"rules": [],
			"seeded_default_rules": true
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n := m.Notifications()
	if n.SeedVersion != seedVersion {
		t.Fatalf("expected SeedVersion %d, got %d", seedVersion, n.SeedVersion)
	}
	if len(n.Rules) != 4 {
		t.Fatalf("expected only the batch-2 and batch-4 rules, got %+v", n.Rules)
	}
	for _, r := range n.Rules {
		switch r.Name {
		case "Login required soon", "API key expiring",
			"Daily scrape limit reached", "Session cookie expired":
		default:
			t.Fatalf("unexpected rule seeded: %+v", r)
		}
	}

	// Deleting a seeded rule must stick across a restart.
	n.Rules = nil
	if err := m.UpdateNotifications(n); err != nil {
		t.Fatal(err)
	}
	m2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m2.Notifications().Rules); got != 0 {
		t.Fatalf("deleted seeded rules came back: %+v", m2.Notifications().Rules)
	}
}

// TestExistingDestinationOnlyIsNotSeeded: a destination with no rules yet
// (mid-setup) also counts as "touched" for batch 1 — no starter rules get
// injected. Batch 2 still applies, as above.
func TestExistingDestinationOnlyIsNotSeeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
		"server": {"host": "0.0.0.0", "port": 8420},
		"trackers": [],
		"settings": {},
		"notifications": {
			"destinations": [{"id": "d1", "name": "My Discord", "type": "discord", "url": "https://example.invalid", "enabled": true}],
			"rules": []
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n := m.Notifications()
	if !n.SeededDefaultRules {
		t.Fatal("expected the flag to be set")
	}
	for _, r := range n.Rules {
		switch r.Name {
		case "Promotions & demotions", "Target met", "Ratio approaching minimum":
			t.Fatalf("batch 1 starter rule %q injected when a destination already existed", r.Name)
		}
	}
	if len(n.Rules) != 4 {
		t.Fatalf("expected only the batch-2 and batch-4 rules, got %+v", n.Rules)
	}
}

// TestDigestDefaultsOnFreshInstall: a brand-new config.json gets the weekly
// digest schedule defaulted to Monday 09:00 (weekday=1, hour=9) — nobody's
// touched Alerts yet, so the struct is still all-zero when applyDefaults runs.
func TestDigestDefaultsOnFreshInstall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dig := m.Notifications().Digest
	if dig.Weekday != 1 || dig.Hour != 9 {
		t.Fatalf("fresh-install digest defaults = weekday %d hour %d, want 1/9", dig.Weekday, dig.Hour)
	}
	if dig.Enabled {
		t.Error("a fresh install's digest must default to disabled")
	}
}

// TestDigestDefaultsNotReappliedOnceTouched: once a user has actually set a
// digest schedule (even just Sunday/hour 0 — the zero-valued weekday/hour
// that would otherwise look "untouched"), a later load must NOT stomp it back
// to Monday 09:00. Enabled=true is the unambiguous "touched" signal here.
func TestDigestDefaultsNotReappliedOnceTouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
		"server": {"host": "0.0.0.0", "port": 8420},
		"trackers": [],
		"settings": {},
		"notifications": {
			"destinations": [], "rules": [], "seeded_default_rules": true,
			"digest": {"enabled": true, "weekday": 0, "hour": 0, "destinations": []}
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dig := m.Notifications().Digest
	if !dig.Enabled || dig.Weekday != 0 || dig.Hour != 0 {
		t.Fatalf("existing digest config was overwritten: got %+v, want enabled/Sunday/00:00 preserved", dig)
	}
}

// TestMigrateRetiredTrackerType: a config written before the gazelle type was
// renamed must keep working. An unresolvable type collects nothing and says
// nothing, so leaving it would quietly break an existing user's tracker.
func TestMigrateRetiredTrackerType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	seed := `{"trackers":[
		{"id":"a","name":"Anthelion","url":"https://anthelion.me","type":"gazelle","enabled":true},
		{"id":"b","name":"Other","url":"https://example.org","type":"unit3d","enabled":true}
	]}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	trackers := m.Trackers()
	if trackers[0].Type != "gazelle_antneb" {
		t.Errorf("retired type = %q, want gazelle_antneb", trackers[0].Type)
	}
	if trackers[1].Type != "unit3d" {
		t.Errorf("an unrelated type was rewritten: %q", trackers[1].Type)
	}
	// The rewrite must be persisted, not just applied in memory — otherwise it
	// runs again on every start and never actually fixes the file.
	m2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if m2.Trackers()[0].Type != "gazelle_antneb" {
		t.Error("the migration was not written back to disk")
	}
}

// The notification centre must always be a destination, so rules always have
// somewhere to resolve to and "turn it off" is just disabling it.
func TestInAppDestinationAlwaysPresent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	find := func(n models.NotificationConfig) (models.NotifyDestination, bool) {
		for _, d := range n.Destinations {
			if d.ID == models.InAppDestinationID {
				return d, true
			}
		}
		return models.NotifyDestination{}, false
	}
	got, ok := find(m.Notifications())
	if !ok || !got.Enabled || got.Type != models.InAppDestinationType {
		t.Fatalf("fresh config = %+v, want an enabled in-app destination", got)
	}

	// A save that omits it must not be able to drop it — the editor sends the
	// whole list back, and losing it would leave rules resolving nowhere.
	n := m.Notifications()
	n.Destinations = []models.NotifyDestination{{ID: "abc", Name: "Discord", Type: "discord", Enabled: true}}
	if err := m.UpdateNotifications(n); err != nil {
		t.Fatal(err)
	}
	if _, ok := find(m.Notifications()); !ok {
		t.Error("saving without the centre dropped it")
	}

	// Disabling it, though, must stick.
	n = m.Notifications()
	for i := range n.Destinations {
		if n.Destinations[i].ID == models.InAppDestinationID {
			n.Destinations[i].Enabled = false
		}
	}
	if err := m.UpdateNotifications(n); err != nil {
		t.Fatal(err)
	}
	if got, _ := find(m.Notifications()); got.Enabled {
		t.Error("disabling the centre did not stick")
	}
}

// A rule that names a webhook used to reach that webhook AND the panel, because
// recording was unconditional. Now that an explicit list means "only these", the
// migration must add the centre or such a rule silently stops being recorded.
func TestSeedV3RoutesExistingRulesToTheCentre(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{
		"server": {"host": "0.0.0.0", "port": 8420},
		"trackers": [],
		"settings": {},
		"notifications": {
			"destinations": [{"id":"abc","name":"Discord","type":"discord","enabled":true}],
			"rules": [
				{"id":"r1","name":"Named","enabled":true,"destinations":["abc"],
				 "conditions":[{"field":"ratio","op":"lt","value":"1"}]},
				{"id":"r2","name":"Unset","enabled":true,"destinations":[],
				 "conditions":[{"field":"ratio","op":"lt","value":"1"}]}
			],
			"seed_version": 2,
			"seeded_default_rules": true
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n := m.Notifications()
	byID := map[string]models.AlertRule{}
	for _, r := range n.Rules {
		byID[r.ID] = r
	}
	if !slices.Contains(byID["r1"].Destinations, models.InAppDestinationID) {
		t.Errorf("r1 destinations = %v, want the centre appended", byID["r1"].Destinations)
	}
	// A rule that picked nothing already means "everywhere" — leave it alone,
	// or it silently becomes pinned to today's destination list.
	if len(byID["r2"].Destinations) != 0 {
		t.Errorf("r2 destinations = %v, want left empty", byID["r2"].Destinations)
	}
	// Idempotent: re-opening must not append it twice.
	m2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range m2.Notifications().Rules {
		if r.ID != "r1" {
			continue
		}
		got := 0
		for _, id := range r.Destinations {
			if id == models.InAppDestinationID {
				got++
			}
		}
		if got != 1 {
			t.Errorf("centre appears %d times after a second open", got)
		}
	}
}
