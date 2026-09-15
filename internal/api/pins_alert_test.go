package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
	"github.com/Yata-Dash/Yata-Dash/internal/pathways"
)

// A pinned path's next hop crossing into "every requirement met" is the one
// moment on a path that is news, and it has to reach the notification centre
// through the same edge rules as target_met: prime silently, fire once on the
// transition, stay quiet after.
func TestPinnedPathReadyFiresOnceOnTheTransition(t *testing.T) {
	d := testDeps(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "routes.json")
	raw := `{
		"schema_version": 1,
		"source": {"name": "test", "url": "https://test.invalid", "license": "test"},
		"routes": [
			{"from": "Aura4K", "to": "TargetX", "days": 0, "reqs": "1 TiB", "active": true}
		],
		"unlocks": {}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := pathways.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	d.Paths = data

	owner := models.Tracker{ID: "own1", Name: "Owner", URL: "https://aura4k.net", Enabled: true}
	if err := d.Cfg.AddTracker(owner); err != nil {
		t.Fatal(err)
	}
	s := d.Cfg.Settings()
	s.PathwayPins = []models.PathwayPin{{Hops: []string{"Aura4K", "TargetX"}}}
	if err := d.Cfg.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := d.Cfg.UpdateNotifications(models.NotificationConfig{
		Rules: []models.AlertRule{{
			ID: "r1", Name: "Pinned path requirements met", Enabled: true, Match: "all",
			Conditions: []models.Condition{{Field: "pathway_ready", Op: "is_true"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	d.Alerts = notify.New(d.Cfg, nil)
	d.Alerts.SetRecorder(NewAlertRecorder(d))

	pass := func(uploaded string) int {
		t.Helper()
		if err := d.Stats.SaveAPI(owner.ID, map[string]any{"uploaded": uploaded, "downloaded": "100 GiB", "ratio": "5.0"}); err != nil {
			t.Fatal(err)
		}
		merged, err := d.Stats.Merged(owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		evaluateTrackerPins(d, owner, merged, notify.TrendContext{})
		return decodeAlerts(t, getJSON(t, d, "/api/alerts")).Total
	}

	if n := pass("500 GiB"); n != 0 {
		t.Fatalf("first sighting fired %d alert(s) — must prime silently", n)
	}
	if n := pass("2 TiB"); n != 1 {
		t.Fatalf("crossing into ready fired %d alert(s), want 1", n)
	}
	if n := pass("3 TiB"); n != 1 {
		t.Fatalf("still ready re-fired: %d alert(s), want 1", n)
	}
	got := decodeAlerts(t, getJSON(t, d, "/api/alerts"))
	if len(got.Alerts) != 1 || got.Alerts[0].TrackerID != owner.ID {
		t.Fatalf("alert = %+v, want one for the owner tracker", got.Alerts)
	}
	if !strings.Contains(got.Alerts[0].Body, "TargetX") {
		t.Errorf("body %q should name the destination", got.Alerts[0].Body)
	}
	t.Log(got.Alerts[0].Body)
}
