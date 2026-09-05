package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func ladderDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Nothing stored must read as "unknown", not as "this tracker has no ranks".
func TestLatestGroupLadderEmpty(t *testing.T) {
	if _, ok := ladderDB(t).LatestGroupLadder("t1"); ok {
		t.Error("reported a ladder for a tracker that has never been fetched")
	}
}

// An unchanged ladder confirms the existing revision rather than opening a new
// one — otherwise a daily check would file a revision a day and bury the two or
// three that mean something.
func TestSaveGroupLadderUnchangedBumpsCheckedAt(t *testing.T) {
	db := ladderDB(t)
	day1 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	day2 := day1.Add(24 * time.Hour)
	payload := []byte(`{"auto":[{"title":"Seed"}]}`)

	if err := db.SaveGroupLadder("t1", payload, day1); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveGroupLadder("t1", payload, day2); err != nil {
		t.Fatal(err)
	}
	got, ok := db.LatestGroupLadder("t1")
	if !ok {
		t.Fatal("no ladder stored")
	}
	if !got.FirstSeen.Equal(day1) {
		t.Errorf("first_seen = %v, want %v — an unchanged ladder must not look new", got.FirstSeen, day1)
	}
	if !got.CheckedAt.Equal(day2) {
		t.Errorf("checked_at = %v, want %v", got.CheckedAt, day2)
	}
	if n := revisionCount(t, db, "t1"); n != 1 {
		t.Errorf("revisions = %d, want 1", n)
	}
}

// A changed ladder opens a revision, and first_seen then dates the change.
func TestSaveGroupLadderChangeOpensRevision(t *testing.T) {
	db := ladderDB(t)
	day1 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	day2 := day1.Add(72 * time.Hour)

	if err := db.SaveGroupLadder("t1", []byte(`{"auto":[{"title":"Seed"}]}`), day1); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveGroupLadder("t1", []byte(`{"auto":[{"title":"Sprout"}]}`), day2); err != nil {
		t.Fatal(err)
	}
	got, _ := db.LatestGroupLadder("t1")
	if !got.FirstSeen.Equal(day2) {
		t.Errorf("first_seen = %v, want %v", got.FirstSeen, day2)
	}
	if string(got.Payload) != `{"auto":[{"title":"Sprout"}]}` {
		t.Errorf("payload = %s, want the newest revision", got.Payload)
	}
	if n := revisionCount(t, db, "t1"); n != 2 {
		t.Errorf("revisions = %d, want 2 — the old one is the history", n)
	}
}

func TestSaveGroupLadderTrimsHistory(t *testing.T) {
	db := ladderDB(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < groupLadderRevisions+5; i++ {
		payload := []byte(fmt.Sprintf(`{"auto":[{"title":"Rank %d"}]}`, i))
		if err := db.SaveGroupLadder("t1", payload, now.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if n := revisionCount(t, db, "t1"); n != groupLadderRevisions {
		t.Errorf("revisions = %d, want %d", n, groupLadderRevisions)
	}
	// Trimming must drop the OLDEST, never the current one.
	got, _ := db.LatestGroupLadder("t1")
	want := fmt.Sprintf(`{"auto":[{"title":"Rank %d"}]}`, groupLadderRevisions+4)
	if string(got.Payload) != want {
		t.Errorf("payload = %s, want %s", got.Payload, want)
	}
}

// Ladders are per tracker, and removing a tracker takes its ladder with it —
// otherwise a cached ladder would resurrect on re-add looking freshly fetched.
func TestGroupLaddersScopedAndDeletedWithTracker(t *testing.T) {
	db := ladderDB(t)
	now := time.Now().UTC()
	if err := db.SaveGroupLadder("t1", []byte(`{"auto":[{"title":"A"}]}`), now); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveGroupLadder("t2", []byte(`{"auto":[{"title":"B"}]}`), now); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteTracker("t1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := db.LatestGroupLadder("t1"); ok {
		t.Error("ladder survived its tracker's deletion")
	}
	if _, ok := db.LatestGroupLadder("t2"); !ok {
		t.Error("deleting one tracker's ladder removed another's")
	}
}

func revisionCount(t *testing.T, db *DB, trackerID string) int {
	t.Helper()
	var n int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM group_ladders WHERE tracker_id = ?`, trackerID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
