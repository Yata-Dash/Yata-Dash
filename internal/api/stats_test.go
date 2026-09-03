package api

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/config"
	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/fetch"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/stats"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

func testDeps(t *testing.T) *Deps {
	t.Helper()
	// The login lockout is package-level state keyed by IP, and every test
	// arrives from httptest's single RemoteAddr — so one test exhausting the
	// attempt limit locks out every test that runs after it, for fifteen
	// minutes of simulated time. Clearing it here keeps tests independent of
	// their order.
	resetLoginLimiter()
	dir := t.TempDir()
	cfg, err := config.Open(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reg, err := defs.Load("../../defs")
	if err != nil {
		t.Fatal(err)
	}
	return &Deps{
		Cfg:   cfg,
		DB:    db,
		Reg:   reg,
		Fetch: fetch.NewClient(reg, filepath.Join(dir, "missing.json")),
		Stats: stats.New(db),
		// httptest.NewRequest stamps every request with Host "example.com",
		// which the host guard rightly refuses. Allowing it here keeps that
		// guard out of the way of tests aimed at other things; hostguard_test
		// builds its own Deps to exercise the check itself.
		AllowedHosts: []string{"example.com"},
	}
}

// TestStaleDataSurvivesTrackerOutage is the core resilience guarantee:
// when a tracker is down/unreachable, /api/stats must keep returning the
// last stored stats (with ok=false + the error) — NEVER a blank result.
func TestStaleDataSurvivesTrackerOutage(t *testing.T) {
	d := testDeps(t)

	tr := models.Tracker{
		ID:      "t1",
		Name:    "Dead Tracker",
		URL:     "http://127.0.0.1:1", // nothing listens here — connection refused
		Type:    "unit3d",
		APIKey:  "irrelevant",
		Enabled: true,
	}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}

	// Seed the stats engine as if a successful fetch + scrape happened earlier.
	if err := d.Stats.SaveAPI("t1", map[string]any{
		"uploaded": "5.00 TiB", "ratio": 2.5, "bonus_points": "12345",
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.Stats.SaveScrape("t1", map[string]any{
		"seed_size": "3.21 TiB", "join_date": "2025-01-01",
	}); err != nil {
		t.Fatal(err)
	}

	resp := refreshTracker(d, tr, true)

	if resp.OK {
		t.Fatal("expected ok=false for unreachable tracker")
	}
	if resp.Error == "" || resp.ErrorKind == "" {
		t.Fatalf("expected error to be reported, got %+v", resp)
	}
	// The whole point: last known data is still there.
	checks := map[string]any{
		"uploaded":     "5.00 TiB",
		"bonus_points": "12345",
		"seed_size":    "3.21 TiB",
		"join_date":    "2025-01-01",
	}
	for field, want := range checks {
		got, ok := resp.Fields[field]
		if !ok {
			t.Errorf("field %s missing from response after outage", field)
			continue
		}
		if got.Value != want {
			t.Errorf("field %s: got %v, want %v", field, got.Value, want)
		}
	}
	// Sources must be preserved too (api layer vs scrape layer).
	if resp.Fields["uploaded"].Source != models.SourceAPI {
		t.Errorf("uploaded source = %s, want api", resp.Fields["uploaded"].Source)
	}
	if resp.Fields["seed_size"].Source != models.SourceScrape {
		t.Errorf("seed_size source = %s, want scrape", resp.Fields["seed_size"].Source)
	}
}

// TestAPIWinsOverScrape verifies the merge priority rule end-to-end:
// when both layers carry a field, the API value is served; scrape only
// fills fields the API lacks (or where the API value is zero-ish).
func TestAPIWinsOverScrape(t *testing.T) {
	d := testDeps(t)

	if err := d.Stats.SaveAPI("t1", map[string]any{
		"bonus_points": "593626.75", // authoritative
		"ratio":        1.05,
		"fl_tokens":    "0", // zero-ish — scrape may fill
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.Stats.SaveScrape("t1", map[string]any{
		"bonus_points": "111111", // stale scrape — must lose
		"seed_size":    "9.37 TiB",
		"fl_tokens":    "6",
	}); err != nil {
		t.Fatal(err)
	}

	merged, err := d.Stats.Merged("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got := merged["bonus_points"]; got.Value != "593626.75" || got.Source != models.SourceAPI {
		t.Errorf("bonus_points = %v (%s), want API value 593626.75", got.Value, got.Source)
	}
	if got := merged["seed_size"]; got.Value != "9.37 TiB" || got.Source != models.SourceScrape {
		t.Errorf("seed_size = %v (%s), want scrape fill 9.37 TiB", got.Value, got.Source)
	}
	if got := merged["fl_tokens"]; got.Value != "6" || got.Source != models.SourceScrape {
		t.Errorf("fl_tokens = %v (%s), want scrape 6 over zero-ish API value", got.Value, got.Source)
	}
}

// TestManualLayerFillsGaps: a user-entered join date (manual layer) fills
// the field when neither API nor scrape provides it, but a real API/scrape
// value always wins.
func TestManualLayerFillsGaps(t *testing.T) {
	d := testDeps(t)

	// Manual join date only — no API/scrape join date.
	if err := d.Stats.SaveManual("t1", map[string]any{"join_date": "2024-01-15"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Stats.SaveAPI("t1", map[string]any{"ratio": 1.2}); err != nil {
		t.Fatal(err)
	}
	merged, _ := d.Stats.Merged("t1")
	if got := merged["join_date"]; got.Value != "2024-01-15" || got.Source != models.SourceManual {
		t.Errorf("join_date = %v (%s), want manual 2024-01-15", got.Value, got.Source)
	}

	// Now the API reports a join date — it must win over the manual one.
	if err := d.Stats.SaveAPI("t1", map[string]any{"ratio": 1.2, "join_date": "2023-06-01"}); err != nil {
		t.Fatal(err)
	}
	merged, _ = d.Stats.Merged("t1")
	if got := merged["join_date"]; got.Value != "2023-06-01" || got.Source != models.SourceAPI {
		t.Errorf("join_date = %v (%s), want API 2023-06-01 to win over manual", got.Value, got.Source)
	}
}

// TestConcurrentScrapesNeverDoubleHit guards the rate-limit lock: 8
// simultaneous scrape triggers for one tracker must result in exactly ONE
// recorded attempt — the rest must see the cooldown inside the lock and
// back off. This is what keeps users from getting banned when multiple
// tabs / auto-sync / API-fallback fire at once.
func TestConcurrentScrapesNeverDoubleHit(t *testing.T) {
	d := testDeps(t)

	tr := models.Tracker{
		ID:            "race1",
		Name:          "Race Tracker",
		URL:           "http://127.0.0.1:1", // unreachable — attempt still recorded
		Type:          "unit3d",
		Username:      "someone",
		SessionCookie: "session=abc",
		Enabled:       true,
	}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tryScrapeFallback(d, tr)
		}()
	}
	wg.Wait()

	n, err := d.DB.ScrapesSince("race1", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("8 concurrent scrape triggers recorded %d attempts, want exactly 1", n)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// TestManualTypeTrackerKeepsTypedStats is the whole manual-entry feature end
// to end: a tracker of type "manual" is never contacted, so its numbers must
// come from what the user typed and must survive a refresh.
//
// The refresh is the part worth guarding. A "none" API kind still goes through
// the normal fetch path — it just returns nothing — and that success writes an
// EMPTY api layer. If an empty layer ever counted as an answer, or if the write
// disturbed the manual layer, every stat on the tracker would vanish on the
// first poll after the user entered them.
func TestManualTypeTrackerKeepsTypedStats(t *testing.T) {
	d := testDeps(t)
	if _, ok := d.Reg.Type(models.TypeManual); !ok {
		t.Fatal("the manual type def is missing from defs/types")
	}
	if kind := d.Reg.APIKind("https://manual.example", models.TypeManual); kind != "none" {
		t.Fatalf("manual API kind = %q, want none — it must never make a request", kind)
	}

	tr := models.Tracker{
		ID: "m1", Name: "TorrentLeech", URL: "https://manual.example",
		Type: models.TypeManual, Enabled: true,
		JoinDate:    "2021-03-04",
		ManualStats: map[string]string{"uploaded": "5.50 TiB", "ratio": "4.58", "group": "Power User"},
	}
	syncManualLayer(d, tr)

	merged, err := d.Stats.Merged(tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"uploaded": "5.50 TiB", "ratio": "4.58",
		"group": "Power User", "join_date": "2021-03-04",
	}
	for field, w := range want {
		got := merged[field]
		if got.Value != w {
			t.Errorf("%s = %v, want %q", field, got.Value, w)
		}
		if got.Source != models.SourceManual {
			t.Errorf("%s source = %s, want manual", field, got.Source)
		}
	}

	// A refresh pass: fetches nothing, writes an empty API layer, and must
	// leave every typed value exactly where it was.
	data, ferr := d.Fetch.Fetch(tr)
	if ferr != nil {
		t.Fatalf("a manual tracker must never fail a fetch, got %v", ferr)
	}
	if len(data) != 0 {
		t.Errorf("fetch returned %#v, want nothing", data)
	}
	if err := d.Stats.SaveAPI(tr.ID, data); err != nil {
		t.Fatal(err)
	}
	merged, err = d.Stats.Merged(tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	for field, w := range want {
		if got := merged[field]; got.Value != w {
			t.Errorf("after refresh, %s = %v, want %q", field, got.Value, w)
		}
	}
}

// TestManualStatsClearedWhenRemoved: emptying the editor removes the stats
// rather than leaving the last saved values standing. The manual layer is
// replaced wholesale on every save, and this is what that buys.
func TestManualStatsClearedWhenRemoved(t *testing.T) {
	d := testDeps(t)
	tr := models.Tracker{ID: "m2", Type: models.TypeManual,
		ManualStats: map[string]string{"ratio": "4.58"}}
	syncManualLayer(d, tr)

	tr.ManualStats = map[string]string{}
	syncManualLayer(d, tr)

	merged, _ := d.Stats.Merged(tr.ID)
	if _, ok := merged["ratio"]; ok {
		t.Errorf("ratio survived removal: %#v", merged["ratio"])
	}
}

// TestRetiredTrackerIsNeverContacted covers the whole retirement path.
//
// The trap it guards is why the flag exists at all: api.kind lives on the
// TYPE, which is stored on the USER'S tracker — so deleting a shut-down
// tracker's def does NOT stop the requests. It only strips the name and the
// group ladder that the stored history still refers to, leaving the user worse
// off AND still hammering a dead host. Keeping the def and marking it retired
// is what actually stops the traffic.
func TestRetiredTrackerIsNeverContacted(t *testing.T) {
	d := testDeps(t)

	const url = "https://aura4k.net"
	if kind := d.Reg.APIKind(url, "unit3d"); kind != "none" {
		t.Errorf("APIKind = %q, want none — a retired tracker must never be fetched", kind)
	}
	rs := d.Reg.ResolveScrape(url, "unit3d")
	if !rs.Retired || !rs.DisableScraping {
		t.Errorf("ResolveScrape: retired=%v disableScraping=%v, want both true", rs.Retired, rs.DisableScraping)
	}
	spec, retired := d.Reg.Retired(url)
	if !retired || spec.Date == "" {
		t.Fatalf("Retired() = %+v, %v — want the shutdown recorded with a date", spec, retired)
	}

	// Stored stats survive and are served back; the response says why.
	tr := models.Tracker{ID: "a4k", Name: "Aura4K", URL: url, Type: "unit3d", Enabled: true}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	if err := d.Stats.SaveAPI(tr.ID, map[string]any{"uploaded": "12.00 TiB", "ratio": 2.5}); err != nil {
		t.Fatal(err)
	}
	resp := refreshTracker(d, tr, true) // forced: even this must not reach out
	if !resp.Retired {
		t.Error("response should be flagged retired")
	}
	if resp.ErrorKind != "" {
		t.Errorf("error kind = %q — a shut-down site is not an outage", resp.ErrorKind)
	}
	if got := resp.Fields["uploaded"]; got.Value != "12.00 TiB" {
		t.Errorf("stored history must survive, got %#v", got.Value)
	}
	if resp.RetiredDate == "" {
		t.Error("the shutdown date should reach the UI")
	}
}

// TestRetiredTrackerStatus: the summary must say "retired", not "error" — a
// tracker that is gone has not failed.
func TestRetiredTrackerStatus(t *testing.T) {
	d := testDeps(t)
	if status, _ := trackerStatus(d, "x", true, "https://aura4k.net"); status != "retired" {
		t.Errorf("status = %q, want retired", status)
	}
}
