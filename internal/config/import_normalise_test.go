package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// An import is a load from a different source, so an export from an older
// build must come out of it exactly as it would from a restart: retired
// tracker types migrated, the notification centre present — and the starter
// rules that config had already deleted still deleted.
func TestImportAppliesLoadMigrations(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Fully seeded (so seeding has nothing left to do), every starter rule
	// deleted, and a destination list without the centre — the shape that
	// used to import with alerts recording nowhere.
	raw := `{
		"settings": {},
		"trackers": [{"id":"a","name":"Anthelion","url":"https://anthelion.me","type":"gazelle","enabled":true}],
		"notifications": {
			"destinations": [{"id":"hook1","name":"Discord","type":"discord","enabled":true}],
			"rules": [],
			"seed_version": 5,
			"seeded_default_rules": true
		}
	}`
	if err := m.Import([]byte(raw)); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := m.Trackers()[0].Type; got != "gazelle_antneb" {
		t.Errorf("imported retired type = %q, want gazelle_antneb", got)
	}
	n := m.Notifications()
	hasCentre := false
	for _, d := range n.Destinations {
		if d.ID == models.InAppDestinationID {
			hasCentre = d.Enabled && d.Type == models.InAppDestinationType
		}
	}
	if !hasCentre {
		t.Errorf("imported config has no enabled notification centre: %+v", n.Destinations)
	}
	if len(n.Rules) != 0 {
		t.Errorf("import re-seeded starter rules the config had deleted: %+v", n.Rules)
	}
	if n.Digest.Weekday != 1 || n.Digest.Hour != 9 {
		t.Errorf("digest schedule not defaulted on import: %+v", n.Digest)
	}
}

// An export from before a seeding batch existed gets that batch on import,
// as it would on the next restart — and only that batch.
func TestImportOfOlderExportGetsOnlyMissingBatches(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := `{
		"settings": {},
		"trackers": [],
		"notifications": {"destinations": [], "rules": [], "seed_version": 4, "seeded_default_rules": true}
	}`
	if err := m.Import([]byte(raw)); err != nil {
		t.Fatalf("Import: %v", err)
	}
	n := m.Notifications()
	if n.SeedVersion != seedVersion {
		t.Errorf("SeedVersion = %d, want %d", n.SeedVersion, seedVersion)
	}
	if len(n.Rules) != 1 || n.Rules[0].Name != "Pinned path requirements met" {
		t.Errorf("want only the batch-5 rule, got %+v", n.Rules)
	}
}

// A config already at the current seed version never runs the seeding pass,
// which was the only place the centre was put back. It must still be restored
// on load, and written back so the repair is not redone on every start.
func TestOpenRestoresCentreOnFullySeededConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{
		"settings": {},
		"trackers": [],
		"notifications": {"destinations": [], "rules": [], "seed_version": 5, "seeded_default_rules": true}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"`+models.InAppDestinationID+`"`) {
		t.Errorf("the restored centre was not written back:\n%s", data)
	}
	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := m.Notifications(); len(n.Destinations) != 1 || n.Destinations[0].ID != models.InAppDestinationID || len(n.Rules) != 0 {
		t.Errorf("after reopen: destinations %+v, rules %+v", n.Destinations, n.Rules)
	}
}
