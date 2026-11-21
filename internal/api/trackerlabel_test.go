package api

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/logging"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
)

// labelDeps: a made-up def "Example Tracker" at https://named.example with a
// two-rung ladder, and a capturing logger.
func labelDeps(t *testing.T) (*Deps, *bytes.Buffer) {
	t.Helper()
	d := testDeps(t)
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	typeJSON := `{"schema_version":1,"key":"fam","label":"Family","api":{"kind":"unit3d"},
		"capabilities":{"api_stats":["uploaded"]}}`
	trackerJSON := `{"schema_version":1,"key":"named","name":"Example Tracker",
		"url":"https://named.example","type":"fam",
		"groups":[{"name":"User","requirements":{}},{"name":"Power User","requirements":{}}]}`
	if err := os.WriteFile(filepath.Join(dir, "types", "fam.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trackers", "named.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := defs.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	d.Reg = reg
	var buf bytes.Buffer
	lg, err := logging.New("", logging.Trace, 100, &buf, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	d.Log = lg
	return d, &buf
}

func TestTrackerLabelFallbacks(t *testing.T) {
	d, _ := labelDeps(t)
	cases := []struct {
		name string
		t    models.Tracker
		want string
	}{
		{"custom name wins", models.Tracker{ID: "a", Name: "Mine", URL: "https://named.example"}, "Mine"},
		{"empty name uses the def", models.Tracker{ID: "a", URL: "https://named.example"}, "Example Tracker"},
		{"blank name uses the def", models.Tracker{ID: "a", Name: " \t ", URL: "https://named.example/"}, "Example Tracker"},
		{"no def uses the host", models.Tracker{ID: "a", URL: "https://bob:pw@other.example:8443/secret?k=v#f"}, "other.example"},
		{"bare host", models.Tracker{ID: "a", URL: "other.example"}, "other.example"},
		{"a name that is just the URL uses the host", models.Tracker{ID: "a", Name: "https://other.example/x", URL: "https://other.example/x/"}, "other.example"},
		{"no URL uses the ID", models.Tracker{ID: "a"}, "a"},
		{"the ID is cleaned too", models.Tracker{ID: "a\nb"}, "a b"},
		{"nothing at all", models.Tracker{}, "unknown tracker"},
		{"control characters can't break the line", models.Tracker{ID: "a", Name: "Mi\nne\x1b"}, "Mi ne"},
	}
	for _, c := range cases {
		if got := trackerLabel(d, c.t); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// No registry (some recovery and test paths): still never blank.
	if got := trackerLabel(&Deps{}, models.Tracker{ID: "a", URL: "https://named.example"}); got != "named.example" {
		t.Errorf("nil registry: got %q, want the host", got)
	}
}

// Issue #95: a tracker added without a custom name stores Name "" and left
// log lines reading "fetch:  (id) failed". The def's name must appear instead,
// and the stored tracker must keep its empty name.
func TestUnnamedTrackerIsNamedInLogs(t *testing.T) {
	d, buf := labelDeps(t)
	tr := models.Tracker{ID: "u1", URL: "https://named.example", Type: "fam", Enabled: true}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	lastFetchState.Delete(tr.ID)
	t.Cleanup(func() { lastFetchState.Delete(tr.ID) })

	logFetchTransition(d, tr, "connection_error")
	resetCapDriftMemory()
	checkCapabilityDrift(d, tr, map[string]any{"surprise_field": "1"})

	out := buf.String()
	if !strings.Contains(out, "fetch: Example Tracker (u1) failed") {
		t.Errorf("fetch line should name the def:\n%s", out)
	}
	if !strings.Contains(out, "capabilities: Example Tracker") {
		t.Errorf("capability line should name the def:\n%s", out)
	}
	if strings.Contains(out, ":  (") {
		t.Errorf("a line still has a blank name:\n%s", out)
	}
	if st, _ := d.Cfg.Tracker(tr.ID); st.Name != "" {
		t.Errorf("stored name changed to %q; labels are for display only", st.Name)
	}
}

// The same blank name reached alerts as "— promoted: …". The alert body and
// the centre's tracker name must use the def's name too.
func TestUnnamedTrackerIsNamedInAlerts(t *testing.T) {
	d, _ := labelDeps(t)
	tr := models.Tracker{ID: "u1", URL: "https://named.example", Type: "fam", Enabled: true}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	if err := d.Cfg.UpdateNotifications(models.NotificationConfig{Rules: []models.AlertRule{{
		ID: "r1", Name: "Promoted", Enabled: true, Match: "all",
		Conditions: []models.Condition{{Field: "promoted", Op: "is_true"}},
	}}}); err != nil {
		t.Fatal(err)
	}
	d.Alerts = notify.New(d.Cfg, nil)
	d.Alerts.SetRecorder(NewAlertRecorder(d))

	recordGroupChange(d, tr, models.MergedStats{}, "User", "Power User", notify.TrendContext{})

	got := decodeAlerts(t, getJSON(t, d, "/api/alerts"))
	if len(got.Alerts) != 1 {
		t.Fatalf("got %d alerts, want 1: %+v", len(got.Alerts), got.Alerts)
	}
	a := got.Alerts[0]
	if !strings.HasPrefix(a.Body, "Example Tracker — ") {
		t.Errorf("body %q should start with the def's name", a.Body)
	}
	if a.TrackerName != "Example Tracker" {
		t.Errorf("tracker name %q, want the def's name", a.TrackerName)
	}
}
