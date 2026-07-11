package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestEventsCRUD covers add, filtered read, latest-detail de-dupe helper, and
// retention prune + DeleteTracker purge.
func TestEventsCRUD(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.AddEvent("tr-a", now.Add(-40*24*time.Hour), "group_change", "User→Seeker"))
	must(db.AddEvent("tr-a", now.Add(-5*24*time.Hour), "group_change", "Seeker→PowerPool"))
	must(db.AddEvent("tr-b", now.Add(-2*24*time.Hour), "group_change", "Member→Elite"))

	// Filtered read for tr-a within 30 days → only the recent one.
	evs, err := db.EventsSince([]string{"tr-a"}, now.Add(-30*24*time.Hour))
	must(err)
	if len(evs) != 1 || evs[0].Detail != "Seeker→PowerPool" {
		t.Fatalf("tr-a 30d events = %+v", evs)
	}

	// All trackers, 90 days → three events, oldest first.
	all, err := db.EventsSince(nil, now.Add(-90*24*time.Hour))
	must(err)
	if len(all) != 3 || all[0].Detail != "User→Seeker" {
		t.Fatalf("all events = %+v", all)
	}

	// LatestEventDetail powers the de-dupe guard.
	last, err := db.LatestEventDetail("tr-a", "group_change")
	must(err)
	if last != "Seeker→PowerPool" {
		t.Errorf("latest tr-a = %q", last)
	}
	if none, _ := db.LatestEventDetail("tr-x", "group_change"); none != "" {
		t.Errorf("latest for unknown tracker = %q, want empty", none)
	}

	// Prune older than 30 days removes the 40-day-old row.
	must(db.PruneEvents(now.Add(-30 * 24 * time.Hour)))
	all, err = db.EventsSince(nil, now.Add(-90*24*time.Hour))
	must(err)
	if len(all) != 2 {
		t.Errorf("after prune = %d events, want 2", len(all))
	}

	// DeleteTracker purges a tracker's events.
	must(db.DeleteTracker("tr-a"))
	aEvents, err := db.EventsSince([]string{"tr-a"}, now.Add(-90*24*time.Hour))
	must(err)
	if len(aEvents) != 0 {
		t.Errorf("tr-a events after delete = %d, want 0", len(aEvents))
	}
}
