package config

import (
	"os"
	"path/filepath"
	"testing"
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
	if len(n.Rules) != 5 {
		t.Fatalf("expected 5 seeded rules, got %d: %+v", len(n.Rules), n.Rules)
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
	if got := len(m2.Notifications().Rules); got != 5 {
		t.Fatalf("second load re-seeded: got %d rules, want 5", got)
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
	if len(n.Rules) != 3 {
		t.Fatalf("expected the user's rule plus the two account-deadline rules, got %+v", n.Rules)
	}

	// A second load is a pure no-op (the counter has caught up).
	m2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m2.Notifications().Rules); got != 3 {
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
	if len(n.Rules) != 2 {
		t.Fatalf("expected only the two batch-2 rules, got %+v", n.Rules)
	}
	for _, r := range n.Rules {
		if r.Name != "Login required soon" && r.Name != "API key expiring" {
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
	if len(n.Rules) != 2 {
		t.Fatalf("expected only the two account-deadline rules, got %+v", n.Rules)
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
