package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedSeries writes three days of fine + daily points for two trackers and
// two fields, returning the base time (t0 = three days ago).
func seedSeries(t *testing.T, db *DB) time.Time {
	t.Helper()
	t0 := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Hour)
	for day := range 3 {
		at := t0.Add(time.Duration(day) * 24 * time.Hour)
		for _, tr := range []string{"tr-a", "tr-b"} {
			base := float64(day * 100)
			if tr == "tr-b" {
				base += 1000
			}
			fields := map[string]float64{"uploaded": base + 1, "ratio": base + 2}
			if err := db.AddHistory(tr, at, fields); err != nil {
				t.Fatalf("AddHistory: %v", err)
			}
			if err := db.RecordDaily(tr, at, fields); err != nil {
				t.Fatalf("RecordDaily: %v", err)
			}
		}
	}
	return t0
}

func TestSeriesFineFilters(t *testing.T) {
	db := testDB(t)
	t0 := seedSeries(t, db)

	// No filters: everything since t0 (3 days × 2 trackers × 2 fields).
	all, err := db.SeriesFine(nil, nil, t0)
	if err != nil {
		t.Fatalf("SeriesFine: %v", err)
	}
	if len(all) != 12 {
		t.Fatalf("unfiltered = %d points, want 12", len(all))
	}

	// Tracker + field filter.
	got, err := db.SeriesFine([]string{"tr-a"}, []string{"uploaded"}, t0)
	if err != nil {
		t.Fatalf("SeriesFine filtered: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("filtered = %d points, want 3", len(got))
	}
	for i, p := range got {
		if p.TrackerID != "tr-a" || p.Field != "uploaded" {
			t.Errorf("point %d = %s/%s, want tr-a/uploaded", i, p.TrackerID, p.Field)
		}
		if i > 0 && p.RecordedAt < got[i-1].RecordedAt {
			t.Error("points not oldest-first")
		}
	}

	// Since filter cuts older points.
	recent, err := db.SeriesFine([]string{"tr-a"}, []string{"uploaded"}, t0.Add(36*time.Hour))
	if err != nil {
		t.Fatalf("SeriesFine since: %v", err)
	}
	if len(recent) != 1 {
		t.Errorf("since-filtered = %d points, want 1", len(recent))
	}
}

func TestSeriesDailyFilters(t *testing.T) {
	db := testDB(t)
	t0 := seedSeries(t, db)

	got, err := db.SeriesDaily([]string{"tr-b"}, []string{"ratio"}, t0)
	if err != nil {
		t.Fatalf("SeriesDaily: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("daily filtered = %d points, want 3", len(got))
	}
	for _, p := range got {
		if p.TrackerID != "tr-b" || p.Field != "ratio" {
			t.Errorf("point = %s/%s, want tr-b/ratio", p.TrackerID, p.Field)
		}
		// Daily timestamps sit on UTC midnights.
		if p.RecordedAt%86400 != 0 {
			t.Errorf("daily point not at UTC midnight: %d", p.RecordedAt)
		}
	}

	// Multi-tracker filter.
	both, err := db.SeriesDaily([]string{"tr-a", "tr-b"}, nil, t0)
	if err != nil {
		t.Fatalf("SeriesDaily multi: %v", err)
	}
	if len(both) != 12 {
		t.Errorf("multi-tracker = %d points, want 12", len(both))
	}
}

// TestDeleteTrackerPurgesDaily: deleting a tracker must remove its daily
// rollups too — with ~2-year retention they'd otherwise linger forever.
func TestDeleteTrackerPurgesDaily(t *testing.T) {
	db := testDB(t)
	t0 := seedSeries(t, db)

	if err := db.DeleteTracker("tr-a"); err != nil {
		t.Fatalf("DeleteTracker: %v", err)
	}
	daily, err := db.SeriesDaily(nil, nil, t0)
	if err != nil {
		t.Fatalf("SeriesDaily: %v", err)
	}
	fine, err := db.SeriesFine(nil, nil, t0)
	if err != nil {
		t.Fatalf("SeriesFine: %v", err)
	}
	for _, p := range append(daily, fine...) {
		if p.TrackerID == "tr-a" {
			t.Fatalf("tr-a data survived deletion: %+v", p)
		}
	}
	if len(daily) != 6 || len(fine) != 6 {
		t.Errorf("tr-b data = %d daily / %d fine points, want 6 / 6", len(daily), len(fine))
	}
}

// TestLayerUpdatedAt: the timestamp tracks each source's last SUCCESSFUL write
// independently, and reads as 0 for a source that has never produced data.
// This is what the dashboard's Last API Update column shows — a failing API
// leaves its layer untouched, so the number correctly stops advancing while
// the tracker is still being polled.
func TestLayerUpdatedAt(t *testing.T) {
	db := testDB(t)
	t0 := time.Now().UTC().Add(-7 * 24 * time.Hour).Truncate(time.Second)

	if ts, err := db.LayerUpdatedAt("tr-a", "api"); err != nil || ts != 0 {
		t.Fatalf("never-fetched = %d (err %v), want 0", ts, err)
	}

	// A week-old API success, then a scrape a minute ago — the Unwalled shape.
	if err := db.ReplaceLayer("tr-a", "api", map[string]any{"ratio": "2.0"}, t0); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.ReplaceLayer("tr-a", "scrape", map[string]any{"ratio": "2.5"}, now); err != nil {
		t.Fatal(err)
	}

	api, err := db.LayerUpdatedAt("tr-a", "api")
	if err != nil {
		t.Fatal(err)
	}
	if api != t0.Unix() {
		t.Errorf("api = %d, want %d — a fresh scrape must not advance the API's timestamp", api, t0.Unix())
	}
	scrape, _ := db.LayerUpdatedAt("tr-a", "scrape")
	if scrape != now.Unix() {
		t.Errorf("scrape = %d, want %d", scrape, now.Unix())
	}
	// Another tracker's layers don't leak in.
	if ts, _ := db.LayerUpdatedAt("tr-b", "api"); ts != 0 {
		t.Errorf("tr-b api = %d, want 0", ts)
	}
}

// TestFieldEndpointsSinceMatchesFullSeries: the ends match those of the full
// series grouped by field.
func TestFieldEndpointsSinceMatchesFullSeries(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	for i := range 40 {
		at := now.Add(time.Duration(i-39) * 15 * time.Minute)
		fields := map[string]float64{"uploaded": float64(100 + i), "ratio": float64(i % 7)}
		if i%3 == 0 {
			fields["seeding"] = float64(i) // a sparser field
		}
		if err := db.AddHistory("tr-a", at, fields); err != nil {
			t.Fatal(err)
		}
		// Another tracker's rows interleave in the index and must not leak in.
		if err := db.AddHistory("tr-b", at, map[string]float64{"uploaded": -1}); err != nil {
			t.Fatal(err)
		}
	}
	// A lone point (one sample in the window) collapses to a single point.
	if err := db.AddHistory("tr-a", now.Add(-time.Hour), map[string]float64{"buffer": 5}); err != nil {
		t.Fatal(err)
	}
	since := now.Add(-5 * time.Hour)

	got, err := db.FieldEndpointsSince("tr-a", []string{"uploaded", "ratio", "seeding", "buffer", "absent"}, since)
	if err != nil {
		t.Fatal(err)
	}
	full, err := db.TrackerHistorySince("tr-a", since)
	if err != nil {
		t.Fatal(err)
	}
	byField := map[string][]HistoryPoint{}
	for _, p := range full {
		byField[p.Field] = append(byField[p.Field], p)
	}
	for _, f := range []string{"uploaded", "ratio", "seeding"} {
		want := byField[f]
		g := got[f]
		if len(g) != 2 || g[0] != want[0] || g[1] != want[len(want)-1] {
			t.Errorf("%s endpoints = %+v, want %+v … %+v", f, g, want[0], want[len(want)-1])
		}
	}
	if b := got["buffer"]; len(b) != 1 || b[0].Value != 5 {
		t.Errorf("single-sample buffer = %+v, want one point", b)
	}
	if _, ok := got["absent"]; ok {
		t.Error("a field with no points must be absent")
	}
}

// TestDailyRecordsSampleTime: a later write that day moves the sample time
// along with the value.
func TestDailyRecordsSampleTime(t *testing.T) {
	db := testDB(t)
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	for _, h := range []int{9, 23} {
		at := day.Add(time.Duration(h) * time.Hour)
		if err := db.RecordDaily("tr-a", at, map[string]float64{"uploaded": float64(h)}); err != nil {
			t.Fatal(err)
		}
	}
	pts, err := db.DailySamplesSince("tr-a", day)
	if err != nil {
		t.Fatal(err)
	}
	if want := day.Add(23 * time.Hour).Unix(); len(pts) != 1 || pts[0].RecordedAt != want || pts[0].Value != 23 {
		t.Fatalf("DailySamplesSince = %+v, want one point at %d", pts, want)
	}
	// The label-stamped view is unchanged for its existing callers.
	if lbl, _ := db.DailySince("tr-a", day); len(lbl) != 1 || lbl[0].RecordedAt != day.Unix() {
		t.Errorf("DailySince = %+v, want the midnight label %d", lbl, day.Unix())
	}
}

// TestDailySampleTimeBackfill: legacy rows get a sample time from fine
// history, else the end of that day.
func TestDailySampleTimeBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3).Unix()
	sample := day + 22*3600 + 15*60
	// Recreate the pre-migration table shape with legacy rows.
	for _, q := range []string{
		`DROP TABLE history_daily`,
		`CREATE TABLE history_daily (tracker_id TEXT NOT NULL, day INTEGER NOT NULL,
			field TEXT NOT NULL, value REAL NOT NULL, PRIMARY KEY (tracker_id, day, field))`,
		fmt.Sprintf(`INSERT INTO history_daily VALUES ('x', %d, 'uploaded', 1), ('x', %d, 'ratio', 2)`, day, day),
		// uploaded's fine samples, plus the next day's first (must not count);
		// ratio's are pruned.
		fmt.Sprintf(`INSERT INTO history VALUES ('x', %d, 'uploaded', 0.5), ('x', %d, 'uploaded', 1),
			('x', %d, 'uploaded', 9)`, day+3600, sample, day+86400),
	} {
		if _, err := db.sql.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	db, err = Open(path) // migrate() must add and backfill recorded_at
	if err != nil {
		t.Fatalf("reopen after downgrade: %v", err)
	}
	defer db.Close()
	pts, err := db.DailySamplesSince("x", time.Unix(day, 0))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, p := range pts {
		got[p.Field] = p.RecordedAt
	}
	if got["uploaded"] != sample {
		t.Errorf("uploaded sample time = %d, want the day's last fine point %d", got["uploaded"], sample)
	}
	if got["ratio"] != day+86399 {
		t.Errorf("ratio sample time = %d, want end of day %d", got["ratio"], day+86399)
	}
}
