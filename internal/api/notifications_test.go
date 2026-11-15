package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestPutNotificationsRejectsOutOfRangeDigestSchedule(t *testing.T) {
	cases := []struct {
		name          string
		weekday, hour int
		wantInError   string
	}{
		{"weekday too high", 7, 9, "weekday"},
		{"weekday negative", -1, 9, "weekday"},
		{"weekday far out", 20, 9, "weekday"},
		{"hour too high", 1, 24, "hour"},
		{"hour negative", 1, -1, "hour"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := testDeps(t)
			before := d.Cfg.Notifications().Digest
			body, err := json.Marshal(models.NotificationConfig{
				Digest: models.DigestConfig{Enabled: true, Weekday: c.weekday, Hour: c.hour},
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPut, "/api/notifications", bytes.NewReader(body))
			w := httptest.NewRecorder()
			putNotifications(d)(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("PUT = %d, want 400; body: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), c.wantInError) {
				t.Errorf("error %q does not name the bad field %q", w.Body.String(), c.wantInError)
			}
			if got := d.Cfg.Notifications().Digest; got.Enabled != before.Enabled ||
				got.Weekday != before.Weekday || got.Hour != before.Hour {
				t.Errorf("rejected PUT still changed the digest: %+v → %+v", before, got)
			}
		})
	}

	// The edges themselves are valid.
	d := testDeps(t)
	for _, wh := range [][2]int{{0, 0}, {6, 23}} {
		body, err := json.Marshal(models.NotificationConfig{
			Digest: models.DigestConfig{Enabled: true, Weekday: wh[0], Hour: wh[1]},
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/notifications", bytes.NewReader(body))
		w := httptest.NewRecorder()
		putNotifications(d)(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("weekday %d hour %d: PUT = %d, want 200; body: %s", wh[0], wh[1], w.Code, w.Body.String())
		}
	}
}

// Overlapping digest runs deliver the digest only once.
func TestRunDigestIfDueSendsOnce(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		// Long enough for unserialised runs to all pass the due check.
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := testDeps(t)
	if err := d.Cfg.UpdateNotifications(models.NotificationConfig{
		Destinations: []models.NotifyDestination{{ID: "hook", Name: "hook", Type: "generic", URL: srv.URL, Enabled: true}},
		// Never sent, so always due.
		Digest: models.DigestConfig{Enabled: true, Weekday: 1, Hour: 9},
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			RunDigestIfDue(d)
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("digest delivered %d times by overlapping runs, want 1", got)
	}
	if d.Cfg.Notifications().Digest.LastSentAt == 0 {
		t.Error("LastSentAt was not recorded")
	}
}

// "Send now" during a delivery gets an immediate 409 instead of queuing.
func TestSendDigestNowDuringDeliveryIsRefused(t *testing.T) {
	d := testDeps(t)
	digestMu.Lock() // a delivery in progress
	defer digestMu.Unlock()

	rec := httptest.NewRecorder()
	sendDigestNow(d)(rec, httptest.NewRequest(http.MethodPost, "/api/notifications/digest/send", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while a delivery holds the lock (body %s)", rec.Code, rec.Body.String())
	}
}
