package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/fetch"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
)

// laddered points d at a one-tracker registry whose def lives at baseURL and
// ranks "User" below "Power User", so a group change there classifies as a
// promotion and fires the "promoted" event.
func laddered(t *testing.T, d *Deps, baseURL string) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	typeJSON, err := os.ReadFile("../../defs/types/unit3d.json")
	if err != nil {
		t.Fatal(err)
	}
	trackerJSON := fmt.Sprintf(`{"schema_version":1,"key":"ladder","name":"Ladder","abbr":"LAD",
		"url":%q,"type":"unit3d","groups":[{"name":"User"},{"name":"Power User"}]}`, baseURL)
	if err := os.WriteFile(filepath.Join(dir, "types", "unit3d.json"), typeJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trackers", "ladder.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := defs.Load(dir)
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	d.Reg = reg
	d.Fetch = fetch.NewClient(reg, "")
}

// promotedAlerts wires the alert engine with the seeded-style promotion rule
// (cooldown 0, so a duplicate event is a duplicate alert) and returns a count
// of the alerts recorded so far.
func promotedAlerts(t *testing.T, d *Deps) func() int {
	t.Helper()
	if err := d.Cfg.UpdateNotifications(models.NotificationConfig{Rules: []models.AlertRule{{
		ID: "r1", Name: "Promotions", Enabled: true, Match: "any",
		Conditions: []models.Condition{{Field: "promoted", Op: "is_true"}},
	}}}); err != nil {
		t.Fatal(err)
	}
	d.Alerts = notify.New(d.Cfg, nil)
	d.Alerts.SetRecorder(NewAlertRecorder(d))
	return func() int { return decodeAlerts(t, getJSON(t, d, "/api/alerts")).Total }
}

// Non-forced refreshes of one tracker arriving together (the background loop
// and a dashboard poll, or two open tabs) must coalesce into ONE request to
// the tracker — and so one group_change event and one promotion alert. They
// used to all pass the min-age guard before any of them recorded its fetch.
func TestConcurrentRefreshesFetchOnce(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		// Long enough that every caller is in flight before the first one
		// could record anything.
		time.Sleep(100 * time.Millisecond)
		fmt.Fprint(w, `{"username":"u","group":"Power User","uploaded":400,"downloaded":100,"ratio":4}`)
	}))
	defer ts.Close()

	d := testDeps(t)
	laddered(t, d, ts.URL)
	alerts := promotedAlerts(t, d)
	tr := models.Tracker{ID: "conc-once", Name: "Ladder", URL: ts.URL, Type: "unit3d", APIKey: "k", Username: "u", Enabled: true}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	// The group the last refresh saw, so this fetch reads as a promotion.
	if err := d.Stats.SaveAPI(tr.ID, map[string]any{"group": "User"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lastFetchAt.Delete(tr.ID) })

	const callers = 4
	var start, done sync.WaitGroup
	start.Add(1)
	resps := make([]models.TrackerStatsResponse, callers)
	for i := range callers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			resps[i] = refreshTracker(d, tr, false)
		}()
	}
	start.Done()
	done.Wait()

	if n := hits.Load(); n != 1 {
		t.Errorf("tracker API hit %d times by %d concurrent non-forced refreshes, want 1", n, callers)
	}
	for i, r := range resps {
		if !r.OK || mergedFieldString(r.Fields, "group") != "Power User" {
			t.Errorf("caller %d: ok=%v group=%q, want the fresh data", i, r.OK, mergedFieldString(r.Fields, "group"))
		}
	}
	evs, err := d.DB.EventsSince(nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	changes := 0
	for _, e := range evs {
		if e.Kind == "group_change" {
			changes++
		}
	}
	if changes != 1 {
		t.Errorf("group_change events = %d, want 1: %+v", changes, evs)
	}
	if n := alerts(); n != 1 {
		t.Errorf("promotion alerts = %d, want 1", n)
	}

	// The user's refresh button still goes out every time.
	if r := refreshTracker(d, tr, true); !r.OK {
		t.Fatalf("forced refresh: %+v", r)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("forced refresh did not reach the tracker: %d hits, want 2", n)
	}
	if n := alerts(); n != 1 {
		t.Errorf("a forced refresh after the promotion replayed it: %d alerts, want 1", n)
	}
}

// A tracker whose API is down must be rate-limited like a healthy one: every
// open dashboard's poll used to re-hit it, because only a SUCCESS recorded a
// fetch time. The guarded answer must still say it failed, and a forced
// refresh must still try.
func TestFailedFetchGatesNonForcedRefreshes(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user" {
			hits.Add(1)
		}
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer ts.Close()

	d := testDeps(t)
	laddered(t, d, ts.URL)
	tr := models.Tracker{ID: "conc-fail", Name: "Ladder", URL: ts.URL, Type: "unit3d", APIKey: "k", Enabled: true}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	if err := d.Stats.SaveAPI(tr.ID, map[string]any{"ratio": 2.0}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lastFetchAt.Delete(tr.ID) })

	first := refreshTracker(d, tr, false)
	if first.OK || first.ErrorKind == "" {
		t.Fatalf("first refresh should fail: %+v", first)
	}
	second := refreshTracker(d, tr, false)
	if n := hits.Load(); n != 1 {
		t.Errorf("a non-forced refresh re-hit a failing tracker: %d hits, want 1", n)
	}
	if second.OK || second.ErrorKind != first.ErrorKind || second.Error != first.Error {
		t.Errorf("guarded answer = ok:%v %q/%q, want the failure it is standing in for (%q/%q)",
			second.OK, second.ErrorKind, second.Error, first.ErrorKind, first.Error)
	}
	if _, ok := second.Fields["ratio"]; !ok {
		t.Errorf("guarded answer dropped the stored stats: %+v", second.Fields)
	}

	if r := refreshTracker(d, tr, true); r.OK {
		t.Errorf("forced refresh against a down tracker reported ok")
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("forced refresh did not reach the tracker: %d hits, want 2", n)
	}
}

// Removing every target and later adding back one that was met in the
// meantime is a NEW target, which must prime silently. The engine used to keep
// the old rows through the targetless passes and fire on the stale "unmet".
func TestTargetsClearedThenReaddedDoNotFire(t *testing.T) {
	d := testDeps(t)
	if err := d.Cfg.UpdateNotifications(models.NotificationConfig{Rules: []models.AlertRule{{
		ID: "r1", Name: "Target met", Enabled: true, Match: "all",
		Conditions: []models.Condition{{Field: "target_met", Op: "is_true"}},
	}}}); err != nil {
		t.Fatal(err)
	}
	d.Alerts = notify.New(d.Cfg, nil)
	d.Alerts.SetRecorder(NewAlertRecorder(d))

	tr := models.Tracker{ID: "tgt1", Name: "Targets", URL: "https://targets.invalid", Type: "unit3d", Enabled: true}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	pass := func(targets map[string]string, uploaded string) int {
		t.Helper()
		if err := d.Stats.SaveAPI(tr.ID, map[string]any{"uploaded": uploaded}); err != nil {
			t.Fatal(err)
		}
		merged, err := d.Stats.Merged(tr.ID)
		if err != nil {
			t.Fatal(err)
		}
		tr.Targets = targets
		evaluateTrackerTargets(d, tr, merged, notify.TrendContext{})
		return decodeAlerts(t, getJSON(t, d, "/api/alerts")).Total
	}
	target := map[string]string{"uploaded": "10 TiB"}
	if n := pass(target, "1.00 TiB"); n != 0 {
		t.Fatalf("priming fired %d alert(s)", n)
	}
	if n := pass(nil, "20.00 TiB"); n != 0 { // targets removed; met while untracked
		t.Fatalf("a targetless pass fired %d alert(s)", n)
	}
	if n := pass(target, "20.00 TiB"); n != 0 {
		t.Fatalf("re-adding an already-met target fired %d alert(s) — it must prime silently", n)
	}
	// The edge still works once primed.
	if n := pass(map[string]string{"uploaded": "30 TiB"}, "20.00 TiB"); n != 0 {
		t.Fatalf("an unmet target fired: %d", n)
	}
	if n := pass(map[string]string{"uploaded": "30 TiB"}, "31.00 TiB"); n != 1 {
		t.Fatalf("crossing a tracked target fired %d alert(s), want 1", n)
	}
}

// A pass shares one pathway mapping across its trackers rather than rebuilding
// it per tracker (N² store reads), and must hand every tracker exactly what a
// fresh mapUserTrackers would at that moment — the refreshed tracker's entry
// patched in with its new stats and rates.
func TestRefreshCyclePinUsersMatchesFreshMapping(t *testing.T) {
	d := pathDeps(t)
	a := models.Tracker{ID: "cyc-a", Name: "Aither", URL: "https://aither.cc", Type: "unit3d", Enabled: true}
	b := models.Tracker{ID: "cyc-b", Name: "Blutopia", URL: "https://blutopia.cc", Type: "unit3d", Enabled: true}
	for _, tr := range []models.Tracker{a, b} {
		if err := d.Cfg.AddTracker(tr); err != nil {
			t.Fatal(err)
		}
		if err := d.Stats.SaveAPI(tr.ID, map[string]any{"uploaded": "1.00 TiB", "downloaded": "500.00 GiB", "ratio": 2.0}); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(mapUserTrackers(d)); n != 2 {
		t.Fatalf("fixture: both trackers should map to the dataset, got %d", n)
	}

	cyc := &refreshCycle{}
	refresh := func(tr models.Tracker, uploaded string) {
		t.Helper()
		if err := d.Stats.SaveAPI(tr.ID, map[string]any{"uploaded": uploaded, "downloaded": "500.00 GiB", "ratio": 2.0}); err != nil {
			t.Fatal(err)
		}
		merged, err := d.Stats.Merged(tr.ID)
		if err != nil {
			t.Fatal(err)
		}
		got := cyc.pinUsers(d, tr, merged, d.Stats.GrowthRates(tr.ID))
		if want := mapUserTrackers(d); !reflect.DeepEqual(got, want) {
			t.Fatalf("after refreshing %s:\n got  %+v\n want %+v", tr.ID, got, want)
		}
	}
	refresh(a, "2.00 TiB") // builds the mapping
	refresh(b, "3.00 TiB") // patches b
	refresh(a, "4.00 TiB") // patches a again

	// Built once: a change that did not come through this pass is not seen,
	// which is what proves the mapping is not being rebuilt per tracker.
	if err := d.Stats.SaveAPI(b.ID, map[string]any{"uploaded": "9.00 TiB", "downloaded": "500.00 GiB", "ratio": 2.0}); err != nil {
		t.Fatal(err)
	}
	merged, _ := d.Stats.Merged(a.ID)
	for _, u := range cyc.pinUsers(d, a, merged, d.Stats.GrowthRates(a.ID)) {
		if u.TrackerID == b.ID && u.Stats.UploadedGiB != 3*1024 {
			t.Errorf("the pass rebuilt its mapping: b uploaded = %v GiB, want the 3 TiB it was refreshed with", u.Stats.UploadedGiB)
		}
	}

	// No pass: always a fresh mapping.
	var lone *refreshCycle
	if got, want := lone.pinUsers(d, a, merged, nil), mapUserTrackers(d); !reflect.DeepEqual(got, want) {
		t.Errorf("a lone refresh must map afresh:\n got  %+v\n want %+v", got, want)
	}
}
