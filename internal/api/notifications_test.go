package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// TestPutNotificationsPreservesDigestState: the Alerts editor only ever
// round-trips the digest's schedule fields (enabled/weekday/hour/
// destinations) — a PUT must carry LastSentAt/LastReadyTargets forward
// exactly like SeededDefaultRules, never accept client-supplied values for
// them (the editor never sends real ones, but a stale/adversarial client
// payload must not be able to reset or forge the send history either).
func TestPutNotificationsPreservesDigestState(t *testing.T) {
	d := testDeps(t)

	// Seed server-maintained digest state as if a real send already happened.
	if err := d.Cfg.UpdateDigestState(1700000000, []string{"Aither", "Redacted"}); err != nil {
		t.Fatal(err)
	}

	// The editor's PUT payload never includes last_sent_at/last_ready_targets,
	// and here it also (adversarially) tries to forge them — both must be
	// ignored in favour of the stored values.
	payload := models.NotificationConfig{
		Destinations: []models.NotifyDestination{},
		Rules:        []models.AlertRule{},
		Digest: models.DigestConfig{
			Enabled: true, Weekday: 3, Hour: 14,
			LastSentAt:       999,
			LastReadyTargets: []string{"Forged"},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/notifications", bytes.NewReader(body))
	w := httptest.NewRecorder()
	putNotifications(d)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/notifications = %d, body: %s", w.Code, w.Body.String())
	}

	got := d.Cfg.Notifications().Digest
	if got.LastSentAt != 1700000000 {
		t.Errorf("LastSentAt = %d, want preserved 1700000000 (not the forged 999)", got.LastSentAt)
	}
	if len(got.LastReadyTargets) != 2 || got.LastReadyTargets[0] != "Aither" || got.LastReadyTargets[1] != "Redacted" {
		t.Errorf("LastReadyTargets = %v, want preserved [Aither Redacted] (not the forged [Forged])", got.LastReadyTargets)
	}
	// The schedule fields the editor DOES own must still come through untouched.
	if !got.Enabled || got.Weekday != 3 || got.Hour != 14 {
		t.Errorf("schedule fields = %+v, want enabled/weekday 3/hour 14 from the PUT body", got)
	}
}
