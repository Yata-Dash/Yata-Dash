package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
)

func decodeAlerts(t *testing.T, body string) alertsResponse {
	t.Helper()
	var out alertsResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return out
}

func getJSON(t *testing.T, d *Deps, path string) string {
	t.Helper()
	rr := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

// The end-to-end premise: a rule fires, no destination is configured, and the
// alert still reaches the panel.
func TestAlertRecordedWithNoDestinationConfigured(t *testing.T) {
	d := testDeps(t)
	tr := models.Tracker{ID: "t1", Name: "Aither", URL: "https://aither.cc", Type: "unit3d"}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	// A rule with no destinations, and no destinations configured anywhere.
	if err := d.Cfg.UpdateNotifications(models.NotificationConfig{
		Rules: []models.AlertRule{{
			ID: "r1", Name: "Promoted", Enabled: true,
			Conditions: []models.Condition{{Field: "promoted"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	d.Alerts = notify.New(d.Cfg, nil)
	d.Alerts.SetRecorder(NewAlertRecorder(d))
	d.Alerts.EvaluateEvent(tr, models.MergedStats{},
		notify.EventContext{Kind: "promoted", Detail: "promoted: Seeder → Power User"}, notify.TrendContext{})

	got := decodeAlerts(t, getJSON(t, d, "/api/alerts"))
	if got.Total != 1 || got.Unread != 1 {
		t.Fatalf("total=%d unread=%d, want 1/1 — the alert was dropped", got.Total, got.Unread)
	}
	a := got.Alerts[0]
	if a.RuleName != "Promoted" || a.TrackerName != "Aither" {
		t.Errorf("alert = %+v", a)
	}
	if !strings.Contains(a.Body, "Seeder → Power User") {
		t.Errorf("body = %q, want the same words a webhook would carry", a.Body)
	}
}

func TestMarkAlertsReadEndpoint(t *testing.T) {
	d := testDeps(t)
	rec := NewAlertRecorder(d)
	rec.RecordAlert("r1", "Ratio", "t1", "Aither", "Yata alert: Ratio", "Aither — ratio low")
	rec.RecordAlert("r2", "Login", "t2", "Zenith", "Yata alert: Login", "Zenith — login due")

	if got := decodeAlerts(t, getJSON(t, d, "/api/alerts")); got.Unread != 2 {
		t.Fatalf("unread = %d, want 2", got.Unread)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/alerts/read", strings.NewReader(`{}`))
	NewRouter(d).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST read = %d: %s", rr.Code, rr.Body.String())
	}
	if got := decodeAlerts(t, getJSON(t, d, "/api/alerts")); got.Unread != 0 {
		t.Errorf("unread after mark-all = %d, want 0", got.Unread)
	}
}

// Filtering the list must not change the unread count: it drives the header
// bubble, which is a claim about everything, not about the current view.
func TestUnreadCountIgnoresFilter(t *testing.T) {
	d := testDeps(t)
	rec := NewAlertRecorder(d)
	rec.RecordAlert("r1", "Ratio", "t1", "Aither", "t", "Aither — ratio low")
	rec.RecordAlert("r2", "Login", "t2", "Zenith", "t", "Zenith — login due")

	got := decodeAlerts(t, getJSON(t, d, "/api/alerts?tracker=t1"))
	if got.Total != 1 {
		t.Errorf("total = %d, want 1 — the filter applies to the list", got.Total)
	}
	if got.Unread != 2 {
		t.Errorf("unread = %d, want 2 — the bubble counts everything", got.Unread)
	}
}

func TestDeleteAlertEndpoint(t *testing.T) {
	d := testDeps(t)
	NewAlertRecorder(d).RecordAlert("r1", "Ratio", "t1", "Aither", "t", "b")
	got := decodeAlerts(t, getJSON(t, d, "/api/alerts"))

	rr := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rr, httptest.NewRequest(http.MethodDelete,
		"/api/alerts/"+strconv.FormatInt(got.Alerts[0].ID, 10), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", rr.Code, rr.Body.String())
	}
	if after := decodeAlerts(t, getJSON(t, d, "/api/alerts")); after.Total != 0 {
		t.Errorf("total = %d, want 0", after.Total)
	}
}
