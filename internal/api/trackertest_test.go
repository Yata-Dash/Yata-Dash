package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// demoTracker builds a Type "test" tracker (defs/types/test.json: api.kind
// "demo", skip_html_scrape) — testAPI reads test_data.json (missing in
// testDeps, so it deterministically fails with "mock_read_error") and
// testScrape short-circuits to not_applicable for Type "test". Either way,
// NO real HTTP request is ever made, so these tests are fully offline and
// only care about the bookkeeping side effects (config persistence, the
// testResults/pendingTestResults caches, the scrape log).
func demoTracker(id, apiKey string) models.Tracker {
	return models.Tracker{
		ID: id, Name: "Demo " + id, URL: "http://demo.local/" + id,
		Type: "test", APIKey: apiKey, Enabled: true,
	}
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

	if _, ok := testResults.Load("tmatch1"); !ok {
		t.Error("matching-credentials test did not land in testResults")
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
	if _, ok := testResults.Load("tpend1"); ok {
		t.Fatal("differing-credentials test landed straight in testResults")
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
	if _, ok := testResults.Load("tpend1"); !ok {
		t.Error("pending test was not promoted to testResults after a matching save")
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
	if _, ok := testResults.Load("tdisc1"); ok {
		t.Error("a non-matching save must not promote the stale pending result")
	}
}

// (iv-b) A gazelle_json_cookie tracker (e.g. AlphaRatio, GreatPosterWall) has
// no API key at all — its credential is the session cookie. testAPI's
// pre-check must ask for THAT, not report a misleading "no API key set" for
// a tracker type that has no key concept, and must not block the fetch once
// the cookie is actually present.
func TestAPICookieTypeChecksSessionCookieNotAPIKey(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	noCookie := models.Tracker{
		ID: "cookie1", Name: "Cookie Tracker", URL: "https://cookie-tracker.example",
		Type: "gazelle_json_cookie", Enabled: true,
	}
	if err := d.Cfg.AddTracker(noCookie); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, router, "/api/trackers/cookie1/test", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("test: status %d, body %s", rec.Code, rec.Body.String())
	}
	var res TrackerTestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.API.Status != "not_configured" || res.API.Detail != "no_cookie" {
		t.Fatalf("API result = %+v, want not_configured/no_cookie", res.API)
	}

	// With a cookie set (still no API key), the pre-check must let it through
	// to the real fetch attempt instead of reporting a false no_key.
	withCookie := models.Tracker{
		ID: "cookie2", Name: "Cookie Tracker 2", URL: "http://127.0.0.1:1",
		Type: "gazelle_json_cookie", SessionCookie: "somecookie", Enabled: true,
	}
	if err := d.Cfg.AddTracker(withCookie); err != nil {
		t.Fatal(err)
	}
	rec2 := postJSON(t, router, "/api/trackers/cookie2/test", "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("test: status %d, body %s", rec2.Code, rec2.Body.String())
	}
	var res2 TrackerTestResult
	if err := json.Unmarshal(rec2.Body.Bytes(), &res2); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res2.API.Status == "not_configured" {
		t.Fatalf("API result = %+v, want past the credential pre-check (cookie was set)", res2.API)
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
	if _, ok := testResults.Load(adhocTestID); ok {
		t.Error("adhoc test cached a result under the throwaway ID")
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
