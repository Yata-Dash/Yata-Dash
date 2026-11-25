package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

func patchAlerts(t *testing.T, d *Deps, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/notifications", strings.NewReader(body)))
	return rec
}

func seedAlerts(t *testing.T, d *Deps) {
	t.Helper()
	if err := d.Cfg.UpdateNotifications(models.NotificationConfig{
		Destinations: []models.NotifyDestination{
			{ID: "d1", Name: "Discord", Type: "discord", URL: "https://hooks.example/1", Enabled: true},
			{ID: "d2", Name: "Gotify", Type: "gotify", URL: "https://gotify.example", Token: "tok", Enabled: true},
		},
		Rules: []models.AlertRule{
			{ID: "r1", Name: "low ratio", Enabled: true, Match: "all",
				Conditions: []models.Condition{{Field: "ratio", Op: "lt", Value: "1"}}},
			{ID: "r2", Name: "hit and runs", Enabled: true, Match: "all",
				Conditions: []models.Condition{{Field: "hit_and_runs", Op: "gt", Value: "0"}}},
		},
		Digest: models.DigestConfig{Enabled: false, Weekday: 1, Hour: 9, Destinations: []string{}},
	}); err != nil {
		t.Fatal(err)
	}
}

func ruleNamed(n models.NotificationConfig, id string) (models.AlertRule, bool) {
	for _, r := range n.Rules {
		if r.ID == id {
			return r, true
		}
	}
	return models.AlertRule{}, false
}

// SR-09: two tabs loaded the alerts and each changed a different rule. With a
// whole-config PUT the second save put the first tab's rule back. Each patch
// names only its own rule, so both edits survive, and so does a delete.
func TestNotificationsPatchKeepsOtherTabsChanges(t *testing.T) {
	d := testDeps(t)
	seedAlerts(t, d)

	// Tab B renames r1 and deletes destination d2.
	if rec := patchAlerts(t, d, `{"upsert_rules":[{"id":"r1","name":"ratio under 1","enabled":true,"match":"all",
		"conditions":[{"field":"ratio","op":"lt","value":"1"}]}],"delete_destinations":["d2"]}`); rec.Code != http.StatusOK {
		t.Fatalf("tab B: %d %s", rec.Code, rec.Body.String())
	}
	// Tab A, loaded before B saved, turns r2 off and the digest on.
	if rec := patchAlerts(t, d, `{"upsert_rules":[{"id":"r2","name":"hit and runs","enabled":false,"match":"all",
		"conditions":[{"field":"hit_and_runs","op":"gt","value":"0"}]}],"digest":{"enabled":true}}`); rec.Code != http.StatusOK {
		t.Fatalf("tab A: %d %s", rec.Code, rec.Body.String())
	}

	n := d.Cfg.Notifications()
	if r, _ := ruleNamed(n, "r1"); r.Name != "ratio under 1" {
		t.Errorf("tab B's rename was undone: %q", r.Name)
	}
	if r, _ := ruleNamed(n, "r2"); r.Enabled {
		t.Error("tab A's change to r2 was lost")
	}
	for _, dst := range n.Destinations {
		if dst.ID == "d2" {
			t.Error("tab B's deleted destination came back")
		}
	}
	if !n.Digest.Enabled || n.Digest.Weekday != 1 || n.Digest.Hour != 9 {
		t.Errorf("digest = %+v, want only enabled changed", n.Digest)
	}
	if len(n.Rules) != 2 || n.Rules[0].ID != "r1" || n.Rules[1].ID != "r2" {
		t.Errorf("rules reordered or lost: %+v", n.Rules)
	}
}

// New items are appended; a destination edited with its secrets left empty
// keeps the stored ones, as PUT does for a re-imported export.
func TestNotificationsPatchAddsItemsAndKeepsSecrets(t *testing.T) {
	d := testDeps(t)
	seedAlerts(t, d)
	rec := patchAlerts(t, d, `{"upsert_rules":[{"id":"r3","name":"new","enabled":true,"match":"all",
		"conditions":[{"field":"warnings","op":"gt","value":"0"}]}],
		"upsert_destinations":[{"id":"d2","name":"Gotify renamed","type":"gotify","enabled":true}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	n := d.Cfg.Notifications()
	if len(n.Rules) != 3 || n.Rules[2].ID != "r3" {
		t.Errorf("rules = %+v, want r3 appended", n.Rules)
	}
	for _, dst := range n.Destinations {
		if dst.ID == "d2" && (dst.Name != "Gotify renamed" || dst.URL != "https://gotify.example" || dst.Token != "tok") {
			t.Errorf("d2 = %+v, want renamed with its stored URL and token", dst)
		}
	}
	// The reply is the stored config, which the editor can show.
	var got models.NotificationConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Rules) != 3 {
		t.Errorf("reply rules = %+v (%v)", got.Rules, err)
	}
}

// Server-owned state is never taken from a patch, and the notification
// centre can't be deleted.
func TestNotificationsPatchKeepsServerState(t *testing.T) {
	d := testDeps(t)
	seedAlerts(t, d)
	if err := d.Cfg.UpdateDigestState(1700000000, []string{"Pathway A"}); err != nil {
		t.Fatal(err)
	}
	before := d.Cfg.Notifications()
	rec := patchAlerts(t, d, `{"delete_destinations":["`+models.InAppDestinationID+`"],
		"digest":{"hour":20,"last_sent_at":5},"seed_version":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	n := d.Cfg.Notifications()
	if n.Digest.LastSentAt != 1700000000 || len(n.Digest.LastReadyTargets) != 1 || n.Digest.Hour != 20 {
		t.Errorf("digest = %+v", n.Digest)
	}
	if n.SeedVersion != before.SeedVersion || n.SeededDefaultRules != before.SeededDefaultRules {
		t.Errorf("seeding state changed: %d/%v → %d/%v", before.SeedVersion, before.SeededDefaultRules, n.SeedVersion, n.SeededDefaultRules)
	}
	inApp := false
	for _, dst := range n.Destinations {
		inApp = inApp || dst.ID == models.InAppDestinationID
	}
	if !inApp {
		t.Error("the notification centre was deleted")
	}
}

func TestNotificationsPatchRejectsBadDigestSchedule(t *testing.T) {
	d := testDeps(t)
	seedAlerts(t, d)
	for _, body := range []string{`{"digest":{"weekday":7}}`, `{"digest":{"hour":-1}}`, `{"upsert_rules":[`} {
		if rec := patchAlerts(t, d, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, rec.Code)
		}
	}
	if n := d.Cfg.Notifications(); n.Digest.Weekday != 1 || n.Digest.Hour != 9 {
		t.Errorf("a refused patch changed the digest: %+v", n.Digest)
	}
}
