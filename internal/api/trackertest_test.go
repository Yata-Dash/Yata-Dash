package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// demoTracker builds a Type "test" tracker (defs/types/test.json: api.kind
// "demo", skip_html_scrape) — testAPI reads test_data.json (missing in
// testDeps, so it deterministically fails with "mock_read_error") and
// testScrape short-circuits to not_applicable for Type "test". Either way,
// NO real HTTP request is ever made, so these tests are fully offline and
// only care about the bookkeeping side effects (config persistence, the
// stored checks / pendingTestResults, the scrape log).
func demoTracker(id, apiKey string) models.Tracker {
	return models.Tracker{
		ID: id, Name: "Demo " + id, URL: "http://demo.local/" + id,
		Type: "test", APIKey: apiKey, Enabled: true,
	}
}

// hasStoredCheck reports whether any channel outcome is recorded for the ID.
func hasStoredCheck(t *testing.T, d *Deps, id string) bool {
	t.Helper()
	all, err := d.DB.Checks()
	if err != nil {
		t.Fatal(err)
	}
	return len(all[id]) > 0
}

// TestStatusFollowsRefreshesAndConfiguration: the status column's answer
// is stored (so it outlives a restart), and an ordinary refresh writes it
// too — a Test is not the only way to learn a cookie has
// expired. Static states are worked out on read (a demo tracker's scrape is
// "N/A" without anyone recording it), and a tracker nothing is known about
// is absent rather than invented.
func TestStatusFollowsRefreshesAndConfiguration(t *testing.T) {
	d := testDeps(t)
	if err := d.Cfg.AddTracker(demoTracker("tstat1", "k")); err != nil {
		t.Fatal(err)
	}
	// Static only: the scrape channel of a demo tracker is N/A, the API has
	// not been tried.
	st := currentTestStatus(d)
	if got := st["tstat1"]; got.Scrape.Status != "not_applicable" || got.API.Status != "untested" {
		t.Fatalf("before any attempt: %+v", got)
	}

	// A refresh records the API outcome (mock data is missing in testDeps,
	// so it fails — the point is that the failure is now on record).
	refreshTracker(d, mustTracker(t, d, "tstat1"), true)
	st = currentTestStatus(d)
	got := st["tstat1"]
	if got.API.Status != "fail" || got.API.Source != "refresh" || got.API.At == 0 {
		t.Fatalf("after a refresh: %+v", got.API)
	}
	if got.TestedAt != got.API.At {
		t.Errorf("tested_at = %d, want the newest channel's time %d", got.TestedAt, got.API.At)
	}

	// A Test overrides the refresh's record and says so.
	router := NewRouter(d)
	if rec := postJSON(t, router, "/api/trackers/tstat1/test", ""); rec.Code != http.StatusOK {
		t.Fatalf("test: %d %s", rec.Code, rec.Body.String())
	}
	if got := currentTestStatus(d)["tstat1"]; got.API.Source != "test" {
		t.Fatalf("after a Test: %+v", got.API)
	}

	// The present configuration outranks any past outcome: take the key away
	// and the channel is "not set up" now, whatever the last attempt said.
	// (Survival across a reopen is the store's test, checks_test.go.)
	_ = d.Cfg.UpdateTracker("tstat1", func(tr *models.Tracker) { tr.Type = "unit3d"; tr.APIKey = "" })
	if got := currentTestStatus(d)["tstat1"]; got.API.Status != "not_configured" || got.API.Detail != "no_key" {
		t.Fatalf("no key: %+v", got.API)
	}
}

// TestStatusFallsBackToScrapeLog: before any row of its own exists, the
// scrape channel reads the scrape log's last attempt — a daily scraper must
// not show "Not tried yet" for a day after the upgrade that added the table.
func TestStatusFallsBackToScrapeLog(t *testing.T) {
	d := testDeps(t)
	tr := models.Tracker{ID: "tlog1", Name: "Log", URL: "http://log.local", Type: "unit3d",
		APIKey: "k", Username: "u", SessionCookie: "c", Enabled: true}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.RecordScrape("tlog1", time.Unix(1_700_000_000, 0), false, "session_expired"); err != nil {
		t.Fatal(err)
	}
	got := currentTestStatus(d)["tlog1"].Scrape
	if got.Status != "fail" || got.Detail != "session_expired" || got.At != 1_700_000_000 || got.Source != "refresh" {
		t.Fatalf("scrape from the log: %+v", got)
	}
	// A row of its own wins once one exists.
	_ = d.DB.RecordCheck(store.Check{TrackerID: "tlog1", Channel: "scrape", At: 1_700_000_500, Status: "ok", Fields: 9, Source: "refresh"})
	if got := currentTestStatus(d)["tlog1"].Scrape; got.Status != "ok" || got.At != 1_700_000_500 {
		t.Fatalf("stored row should win: %+v", got)
	}
}

func mustTracker(t *testing.T, d *Deps, id string) models.Tracker {
	t.Helper()
	tr, ok := d.Cfg.Tracker(id)
	if !ok {
		t.Fatalf("tracker %s missing", id)
	}
	return tr
}

func postJSON(t *testing.T, router http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest("POST", path, nil)
	} else {
		r = httptest.NewRequest("POST", path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	return rec
}

// (i) An override test must never persist the overridden values onto the
// stored tracker — it's a throwaway in-memory copy.
func TestOverrideTestDoesNotPersistTrackerChanges(t *testing.T) {
	d := testDeps(t)
	tr := demoTracker("tov1", "storedkey")
	tr.SessionCookie = "storedcookie"
	tr.Username = "storeduser"
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(d)

	rec := postJSON(t, router, "/api/trackers/tov1/test",
		`{"api_key":"overriddenkey","session_cookie":"overriddencookie","username":"overriddenuser"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("test: status %d, body %s", rec.Code, rec.Body.String())
	}

	got, ok := d.Cfg.Tracker("tov1")
	if !ok {
		t.Fatal("tracker vanished")
	}
	if got.APIKey != "storedkey" || got.SessionCookie != "storedcookie" || got.Username != "storeduser" {
		t.Errorf("stored tracker was mutated by an override test: %+v", got)
	}
}

// (ii) A test whose (post-override) credentials equal what's already saved
// — including the common case of no override body at all — is cached as the
// tracker's official test-status result, not left pending.
func TestMatchingCredentialsTestStoresToTestResults(t *testing.T) {
	d := testDeps(t)
	if err := d.Cfg.AddTracker(demoTracker("tmatch1", "k1")); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(d)

	rec := postJSON(t, router, "/api/trackers/tmatch1/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("test: status %d, body %s", rec.Code, rec.Body.String())
	}

	if !hasStoredCheck(t, d, "tmatch1") {
		t.Error("matching-credentials test was not recorded as the tracker's last outcome")
	}
	if _, ok := pendingTestResults.Load("tmatch1"); ok {
		t.Error("matching-credentials test unexpectedly went pending")
	}
}

// (iii) A test of DIFFERING (unsaved) credentials goes to pendingTestResults
// instead of testResults, and is promoted only once the tracker is actually
// saved with those same credentials.
func TestDifferingCredentialsGoesPendingAndPromotesOnMatchingSave(t *testing.T) {
	d := testDeps(t)
	if err := d.Cfg.AddTracker(demoTracker("tpend1", "stored")); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(d)

	rec := postJSON(t, router, "/api/trackers/tpend1/test", `{"api_key":"different"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("test: status %d, body %s", rec.Code, rec.Body.String())
	}
	if hasStoredCheck(t, d, "tpend1") {
		t.Fatal("differing-credentials test was recorded as an outcome straight away")
	}
	if _, ok := pendingTestResults.Load("tpend1"); !ok {
		t.Fatal("differing-credentials test did not go pending")
	}

	// Save with the SAME credentials that were tested — the pending result
	// must promote.
	putReq := httptest.NewRequest(http.MethodPut, "/api/trackers/tpend1", strings.NewReader(`{"api_key":"different"}`))
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("save: status %d, body %s", putRec.Code, putRec.Body.String())
	}

	got, _ := d.Cfg.Tracker("tpend1")
	if got.APIKey != "different" {
		t.Fatalf("save did not persist api_key: %+v", got)
	}
	if !hasStoredCheck(t, d, "tpend1") {
		t.Error("pending test was not recorded after a matching save")
	}
	if _, ok := pendingTestResults.Load("tpend1"); ok {
		t.Error("pending entry was not cleared after promotion")
	}
}

// (iv) A test of differing credentials, followed by a save that does NOT
// match what was tested, discards the pending result rather than promoting
// stale data.
func TestPendingDiscardedOnNonMatchingSave(t *testing.T) {
	d := testDeps(t)
	if err := d.Cfg.AddTracker(demoTracker("tdisc1", "stored")); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(d)

	rec := postJSON(t, router, "/api/trackers/tdisc1/test", `{"api_key":"tested-value"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("test: status %d, body %s", rec.Code, rec.Body.String())
	}
	if _, ok := pendingTestResults.Load("tdisc1"); !ok {
		t.Fatal("differing-credentials test did not go pending")
	}

	// Save with credentials that DON'T match the pending snapshot.
	putReq := httptest.NewRequest(http.MethodPut, "/api/trackers/tdisc1", strings.NewReader(`{"api_key":"totally-different"}`))
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("save: status %d, body %s", putRec.Code, putRec.Body.String())
	}

	if _, ok := pendingTestResults.Load("tdisc1"); ok {
		t.Error("pending entry survived a non-matching save")
	}
	if hasStoredCheck(t, d, "tdisc1") {
		t.Error("a non-matching save must not promote the stale pending result")
	}
}

// (v) The ad-hoc endpoint (Add-mode Test) returns a result for a tracker
// that was never added, and persists NOTHING: no config row, no cache
// entry, no scrape-log row under its throwaway ID.
func TestAdhocEndpointReturnsResultAndPersistsNothing(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	rec := postJSON(t, router, "/api/trackers/test-adhoc",
		`{"url":"http://demo.local/adhoc","type":"test","api_key":"whatever"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("adhoc test: status %d, body %s", rec.Code, rec.Body.String())
	}
	var res TrackerTestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.API.Status == "" || res.Scrape.Status == "" {
		t.Errorf("adhoc result missing check statuses: %+v", res)
	}

	if len(d.Cfg.Trackers()) != 0 {
		t.Error("adhoc test persisted a tracker into config")
	}
	if hasStoredCheck(t, d, adhocTestID) {
		t.Error("adhoc test recorded an outcome under the throwaway ID")
	}
	if _, ok := pendingTestResults.Load(adhocTestID); ok {
		t.Error("adhoc test left a pending entry under the throwaway ID")
	}
	n, err := d.DB.ScrapesSince(adhocTestID, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("adhoc test recorded %d scrape-log entries under the throwaway ID, want 0", n)
	}

	// A URL-less body is rejected outright.
	rec = postJSON(t, router, "/api/trackers/test-adhoc", `{"type":"test"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("adhoc test without url: status %d, want 400", rec.Code)
	}
}
