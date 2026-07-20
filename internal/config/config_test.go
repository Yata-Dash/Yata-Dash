package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFreshInstallSeedsDefaultAlertRules: a brand-new config.json (no
// destinations, no rules — nobody has touched Alerts yet) gets the two
// starter rules on its very first load, and the flag stops it happening
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
	if len(n.Rules) != 3 {
		t.Fatalf("expected 3 seeded rules, got %d: %+v", len(n.Rules), n.Rules)
	}
	var haveEvents, haveTarget, haveGuard bool
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
		}
	}
	if !haveEvents || !haveTarget || !haveGuard {
		t.Fatalf("missing an expected seeded rule: %+v", n.Rules)
	}

	// Re-opening the same (now-persisted) config must not re-seed.
	m2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m2.Notifications().Rules); got != 3 {
		t.Fatalf("second load re-seeded: got %d rules, want 3", got)
	}
}

// TestExistingSetupIsNotSeeded: a config.json from before this feature
// shipped, with a user-created rule already in place, must NOT get the
// starter rules injected — only the flag gets set so it's never
// re-evaluated.
func TestExistingSetupIsNotSeeded(t *testing.T) {
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
	if len(n.Rules) != 1 || n.Rules[0].Name != "My rule" {
		t.Fatalf("expected the user's existing rule to be untouched, got %+v", n.Rules)
	}

	// A second load is a pure no-op (flag already set).
	m2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m2.Notifications().Rules); got != 1 {
		t.Fatalf("second load changed rule count: got %d, want 1", got)
	}
}

// TestExistingDestinationOnlyIsNotSeeded: a destination with no rules yet
// (mid-setup) also counts as "touched" — no rules get injected.
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
	if len(n.Rules) != 0 {
		t.Fatalf("expected no rules injected when a destination already existed, got %+v", n.Rules)
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
