package store

import (
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
