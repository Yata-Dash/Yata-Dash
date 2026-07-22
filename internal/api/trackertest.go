package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/scrape"
)

// CheckResult is the outcome of one connectivity check (API or scrape).
//
//	ok             — the request succeeded
//	fail           — a request was made but failed (Detail = error kind)
//	not_configured — a required credential is missing (Detail = which one)
//	not_applicable — this tracker doesn't use this method (Detail = why)
//	blocked        — testing now would break the scrape rate limits
//	                 (Detail = cooldown | daily_limit); try again later
type CheckResult struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fields int    `json:"fields,omitempty"`
}

// TrackerTestResult is the combined API + scrape connectivity test for one
// tracker, so the user can see exactly which of the two (or both) work.
type TrackerTestResult struct {
	API      CheckResult `json:"api"`
	Scrape   CheckResult `json:"scrape"`
	TestedAt int64       `json:"tested_at"` // unix seconds
}

// testResults caches the last test outcome per tracker so the trackers table
// can show a status indicator without re-hitting the tracker on every render.
// Cleared lazily — a deleted tracker's stale entry is harmless.
var testResults sync.Map // trackerID → TrackerTestResult

// pendingTest is a test result whose CREDENTIALS DIFFER from what's currently
// saved (the user tested unsaved form edits). It is promoted to testResults
// only if the tracker is then saved with matching credentials — see
// promoteOrDiscardPendingTest — so "test → save" shows the result in the
// table pill, but "test → cancel" never does (nothing is saved to match
// against, so the pending entry just sits there inert).
type pendingTest struct {
	Result        TrackerTestResult
	APIKey        string
	SessionCookie string
	Username      string
	URL           string
}

var pendingTestResults sync.Map // trackerID → pendingTest

// testOverrides is the optional body for POST /api/trackers/{id}/test — lets
// the edit panel test values still in the form (unsaved) instead of only
// what's persisted, e.g. checking a freshly pasted cookie before Save. Same
// pointer + masked-sentinel semantics as trackerPayload/applyPayload
// (trackers.go): absent field = use the stored value; maskedKey = unchanged;
// anything else (including "") = the value to test with. NEVER persisted.
type testOverrides struct {
	APIKey        *string `json:"api_key"`
	SessionCookie *string `json:"session_cookie"`
	Username      *string `json:"username"`
	URL           *string `json:"url"`
}

// applyTestOverrides mutates an in-memory copy of a stored tracker for one
// test run. Mirrors applyPayload's masked-sentinel rules for just these four
// fields — deliberately NOT the full payload, so a stray field in the body
// can never affect anything but what's being tested.
func applyTestOverrides(t *models.Tracker, p testOverrides) {
	if p.APIKey != nil && *p.APIKey != maskedKey {
		t.APIKey = strings.TrimSpace(*p.APIKey)
	}
	if p.SessionCookie != nil && *p.SessionCookie != maskedKey {
		t.SessionCookie = strings.TrimSpace(*p.SessionCookie)
	}
	if p.Username != nil {
		t.Username = strings.TrimSpace(*p.Username)
	}
	if p.URL != nil && strings.TrimSpace(*p.URL) != "" {
		t.URL = strings.TrimRight(strings.TrimSpace(*p.URL), "/")
	}
}

// promoteOrDiscardPendingTest runs after a tracker save (updateTracker,
// trackers.go): a pending test result from testing unsaved form values is
// promoted to testResults only if the just-saved credentials match exactly
// what was tested — otherwise it's discarded as stale.
func promoteOrDiscardPendingTest(saved models.Tracker) {
	v, ok := pendingTestResults.Load(saved.ID)
	if !ok {
		return
	}
	pendingTestResults.Delete(saved.ID)
	p := v.(pendingTest)
	if p.APIKey == saved.APIKey && p.SessionCookie == saved.SessionCookie &&
		p.Username == saved.Username && p.URL == saved.URL {
		testResults.Store(saved.ID, p.Result)
	}
}

// POST /api/trackers/{id}/test — actively test the tracker's API and profile
// scrape and return which works. An optional JSON body overrides api_key/
// session_cookie/username/url with current-but-unsaved form values (see
// testOverrides). Caches the result for the table indicator — but ONLY when
// the tested credentials match what's actually saved; a test of edited
// values goes to pendingTestResults instead (see promoteOrDiscardPendingTest).
func testTracker(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, ok := d.Cfg.Tracker(id)
		if !ok {
			jsonError(w, "tracker not found", http.StatusNotFound)
			return
		}
		orig := t // stored credentials, to tell whether overrides changed anything

		body, err := io.ReadAll(r.Body)
		if err != nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if len(bytes.TrimSpace(body)) > 0 {
			var p testOverrides
			if err := json.Unmarshal(body, &p); err != nil {
				jsonError(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			applyTestOverrides(&t, p) // in-memory copy only — never persisted
		}

		res := runTrackerTest(d, t)
		if t.APIKey == orig.APIKey && t.SessionCookie == orig.SessionCookie &&
			t.Username == orig.Username && t.URL == orig.URL {
			testResults.Store(t.ID, res)
			pendingTestResults.Delete(t.ID) // a fresh matching test supersedes any stale pending one
		} else {
			pendingTestResults.Store(t.ID, pendingTest{
				Result: res, APIKey: t.APIKey, SessionCookie: t.SessionCookie,
				Username: t.Username, URL: t.URL,
			})
		}
		// Level follows the outcome: a user asked for this test, so a failed
		// or rate-limit-blocked check is a warning, not a routine info line.
		msg := fmt.Sprintf("test: %s (%s) — api=%s scrape=%s",
			t.Name, t.ID, fmtCheck(res.API), fmtCheck(res.Scrape))
		if res.API.Status == "fail" || res.Scrape.Status == "fail" ||
			res.Scrape.Status == "blocked" {
			d.logWarnf("%s", msg)
		} else {
			d.logInfof("%s", msg)
		}
		jsonOK(w, res)
	}
}

// adhocTestID is the throwaway tracker ID used for POST /api/trackers/test-adhoc
// (Add-mode Test button, testing a tracker that hasn't been added yet). It is
// never used for anything persisted (see runAdhocTest's persist=false), so
// there is no risk of it colliding with — or leaking state to/from — a real
// tracker's rate-limit ledger or cached stats. Fixed rather than randomised:
// it's never written anywhere, so uniqueness buys nothing, and a readable
// constant makes it obvious in passing (e.g. a stack trace) that it's not a
// real tracker.
const adhocTestID = "adhoc-test"

// POST /api/trackers/test-adhoc — ad-hoc connectivity test for a tracker
// that hasn't been added yet (Add-mode Test button). Body = the full add
// form payload (trackerPayload — reused as-is; fields the add form doesn't
// send, e.g. targets, are simply absent). Resolves def/type from the URL the
// same way createTracker does, builds a synthetic unpersisted Tracker, and
// runs the same checks with persist=false so nothing touches a real
// tracker's bookkeeping. Never cached (no real ID exists yet) — modal
// display only.
func testAdhocTracker(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p trackerPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if p.URL == nil || strings.TrimSpace(*p.URL) == "" {
			jsonError(w, "url is required", http.StatusBadRequest)
			return
		}
		t := models.Tracker{
			ID:  adhocTestID,
			URL: strings.TrimRight(strings.TrimSpace(*p.URL), "/"),
		}
		if td, ok := d.Reg.TrackerByURL(t.URL); ok {
			t.Type = td.Type
		}
		applyPayload(&t, p) // an explicit type/credentials selection wins over the def match
		if t.Type == "" {
			t.Type = "unit3d"
		}
		jsonOK(w, runAdhocTest(d, t))
	}
}

// GET /api/trackers/test-status — cached last-test results for every tracker
// (the trackers table reads this on load; absent entries = "not tested yet").
func testStatusAll(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		out := map[string]TrackerTestResult{}
		for _, t := range d.Cfg.Trackers() {
			if v, ok := testResults.Load(t.ID); ok {
				out[t.ID] = v.(TrackerTestResult)
			}
		}
		jsonOK(w, out)
	}
}

// fmtCheck renders one check outcome for the log line, keeping the detail
// ("fail" alone hides WHY — connection_error vs rate limit vs bad key).
func fmtCheck(c CheckResult) string {
	if c.Detail == "" {
		return c.Status
	}
	return c.Status + ":" + c.Detail
}

// runTrackerTest runs both checks for a REAL saved tracker. On success it
// persists the freshly fetched data (a test doubles as a refresh) — the
// scrape, being a real request, also records a rate-limit attempt just like
// a normal scrape.
func runTrackerTest(d *Deps, t models.Tracker) TrackerTestResult {
	return TrackerTestResult{
		API:      testAPI(d, t, true),
		Scrape:   testScrape(d, t, true),
		TestedAt: time.Now().Unix(),
	}
}

// runAdhocTest runs the same two checks for a tracker that was never added
// (POST /api/trackers/test-adhoc). persist=false on testAPI/testScrape skips
// every bit of bookkeeping that's keyed by Tracker.ID — the rate-limit lock,
// the scrape-log entry, and the cached API/scrape stats — so an ad-hoc test
// can never corrupt a real tracker's history, and never needs cleaning up
// afterwards because it never wrote anything.
func runAdhocTest(d *Deps, t models.Tracker) TrackerTestResult {
	return TrackerTestResult{
		API:      testAPI(d, t, false),
		Scrape:   testScrape(d, t, false),
		TestedAt: time.Now().Unix(),
	}
}

func testAPI(d *Deps, t models.Tracker, persist bool) CheckResult {
	// Opt-out is a hard stop for the API too — a "Test" must never contact a
	// tracker whose operator asked not to be supported (testScrape enforces the
	// same via the scrape policy).
	if _, opted := d.Reg.OptOut(t.URL); opted {
		return CheckResult{Status: "not_applicable", Detail: "opted_out"}
	}
	kind := d.Reg.APIKind(t.URL, t.Type)
	if kind == "none" {
		return CheckResult{Status: "not_applicable", Detail: "scrape_only"}
	}
	// Real APIs need a key (and gazelle also a username) — surface these as
	// "not configured" rather than letting the fetcher return a raw error.
	if kind != "demo" {
		if strings.TrimSpace(t.APIKey) == "" {
			return CheckResult{Status: "not_configured", Detail: "no_key"}
		}
		if kind == "gazelle" && strings.TrimSpace(t.Username) == "" {
			return CheckResult{Status: "not_configured", Detail: "no_username"}
		}
	}
	fields, ferr := d.Fetch.Fetch(t)
	if ferr != nil {
		return CheckResult{Status: "fail", Detail: ferr.Kind}
	}
	if persist {
		_ = d.Stats.SaveAPI(t.ID, fields)
	}
	return CheckResult{Status: "ok", Fields: len(fields)}
}

func testScrape(d *Deps, t models.Tracker, persist bool) CheckResult {
	// Demo trackers never scrape.
	if t.Type == "test" {
		return CheckResult{Status: "not_applicable", Detail: "no_scrape_support"}
	}

	// Hold the per-tracker lock across evaluate→scrape→record (same contract
	// as runScrape) so a test can never race a refresh into double-hitting the
	// tracker — and evaluate the SAME policy cascade. A test that bypassed
	// cooldowns or daily caps would let the Test button hammer a tracker;
	// rate limits protect users' accounts and must stay airtight. An ad-hoc
	// test (persist=false) has no real tracker/ID to race against — and must
	// not leave an entry in the lock map for an ID that will never recur —
	// so it skips the lock entirely.
	if persist {
		mu := lockScrape(t.ID)
		defer mu.Unlock()
	}

	rs := d.Reg.ResolveScrape(t.URL, t.Type)
	pol := scrape.Evaluate(d.Cfg.Settings(), t, rs, d.DB, time.Now())
	if !pol.Allowed {
		switch pol.Reason {
		case "opted_out", "api_only", "no_scrape_support", "scrape_disabled":
			return CheckResult{Status: "not_applicable", Detail: pol.Reason}
		case "no_username", "no_cookie":
			return CheckResult{Status: "not_configured", Detail: pol.Reason}
		default: // cooldown | daily_limit — a request now would break the limits
			return CheckResult{Status: "blocked", Detail: pol.Reason}
		}
	}

	spec := scrape.Spec{
		ExtraLabels:     rs.Labels,
		ProfilePath:     rs.ProfilePath,
		EventTitleClass: rs.EventTitleClass,
		StatCardClasses: rs.StatCardClasses,
		PresenceFlags:   rs.PresenceFlags,
		Identify:        rs.Identify,
		Gazelle:         d.Reg.APIKind(t.URL, t.Type) == "gazelle",
		KnownUserID:     mergedString(d, t.ID, "user_id"),
	}
	result, serr := scrape.Profile(t, spec)
	if persist {
		recordScrapeAttempt(d, t, serr)
	}
	if serr != nil {
		return CheckResult{Status: "fail", Detail: serr.Kind}
	}
	if persist && len(result) > 0 {
		_ = d.Stats.SaveScrape(t.ID, toAnyMap(result))
	}
	return CheckResult{Status: "ok", Fields: len(result)}
}
