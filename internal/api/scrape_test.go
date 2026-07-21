package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// TestCookieExpired: explicit login signals flag immediately; empty_scrape
// only when the two latest attempts both came back empty (a single one can
// be anti-bot or maintenance); other failures never read as a dead cookie.
func TestCookieExpired(t *testing.T) {
	cases := []struct {
		name string
		h    store.ScrapeHealth
		want bool
	}{
		{"healthy", store.ScrapeHealth{LastOK: true}, false},
		{"session_expired", store.ScrapeHealth{LastKind: "session_expired"}, true},
		{"user_id_not_found", store.ScrapeHealth{LastKind: "user_id_not_found"}, true},
		{"single empty_scrape", store.ScrapeHealth{LastKind: "empty_scrape"}, false},
		{"double empty_scrape", store.ScrapeHealth{LastKind: "empty_scrape", PrevFailKind: "empty_scrape"}, true},
		{"empty after timeout", store.ScrapeHealth{LastKind: "empty_scrape", PrevFailKind: "timeout"}, false},
		{"timeout", store.ScrapeHealth{LastKind: "timeout", PrevFailKind: "timeout"}, false},
		{"forbidden", store.ScrapeHealth{LastKind: "forbidden"}, false},
	}
	for _, tc := range cases {
		if got := cookieExpired(tc.h); got != tc.want {
			t.Errorf("%s: cookieExpired = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestScrapeStatusHealthFields: /api/scrape-status carries the health tail —
// error kind, streak, and the cookie_expired flag — and drops it all again
// after a successful attempt.
func TestScrapeStatusHealthFields(t *testing.T) {
	d := testDeps(t)
	if err := d.Cfg.AddTracker(models.Tracker{
		ID: "t1", Name: "T1", URL: "https://example.org", Type: "unit3d",
		Username: "u", SessionCookie: "c", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	get := func() map[string]scrapeStatusEntry {
		rec := httptest.NewRecorder()
		scrapeStatus(d)(rec, httptest.NewRequest("GET", "/api/scrape-status", nil))
		var out map[string]scrapeStatusEntry
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	now := time.Now().UTC()
	if err := d.DB.RecordScrape("t1", now.Add(-2*time.Minute), false, "timeout"); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.RecordScrape("t1", now.Add(-time.Minute), false, "session_expired"); err != nil {
		t.Fatal(err)
	}
	e := get()["t1"]
	if e.LastErrorKind != "session_expired" || e.ConsecutiveFailures != 2 || !e.CookieExpired {
		t.Errorf("after failures: %+v", e)
	}
	if e.LastErrorAt != now.Add(-time.Minute).Unix() {
		t.Errorf("last_error_at = %d", e.LastErrorAt)
	}

	if err := d.DB.RecordScrape("t1", now, true, ""); err != nil {
		t.Fatal(err)
	}
	e = get()["t1"]
	if e.LastErrorKind != "" || e.ConsecutiveFailures != 0 || e.CookieExpired {
		t.Errorf("after success the health fields should clear: %+v", e)
	}
}
