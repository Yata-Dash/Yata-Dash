package store

import (
	"testing"
	"time"
)

// TestConnectionDailyRollup: attempts fold into one row per tracker per UTC
// day, counts accumulate, and last_kind survives a later success in the same
// day (the day still "had a problem" even if it ended fine).
func TestConnectionDailyRollup(t *testing.T) {
	db := openTestDB(t)
	day := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return day.Add(time.Duration(h) * time.Hour) }

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.RecordConnection("x", at(1), true, "", "api"))
	must(db.RecordConnection("x", at(2), false, "timeout", "api"))
	must(db.RecordConnection("x", at(3), true, "", "api"))
	// Next day, a total outage.
	must(db.RecordConnection("x", at(25), false, "http_500", "api"))
	must(db.RecordConnection("x", at(26), false, "http_500", "api"))

	rows, err := db.ConnectionDaily([]string{"x"}, day)
	must(err)
	if len(rows) != 2 {
		t.Fatalf("want 2 daily rows, got %d (%+v)", len(rows), rows)
	}

	d0 := rows[0]
	if d0.OKCount != 2 || d0.FailCount != 1 {
		t.Errorf("day 0 counts = ok %d fail %d, want 2/1", d0.OKCount, d0.FailCount)
	}
	if d0.LastKind != "timeout" {
		t.Errorf("day 0 last_kind = %q, want the failure to survive the later success", d0.LastKind)
	}
	if got := d0.Uptime(); got < 0.66 || got > 0.67 {
		t.Errorf("day 0 uptime = %v, want ~0.667", got)
	}

	d1 := rows[1]
	if d1.OKCount != 0 || d1.FailCount != 2 {
		t.Errorf("day 1 counts = ok %d fail %d, want 0/2", d1.OKCount, d1.FailCount)
	}
	if got := d1.Uptime(); got != 0 {
		t.Errorf("day 1 uptime = %v, want 0", got)
	}
}

// TestConnectionUptimeNoDataIsNotAnOutage: a day with nothing attempted must
// report -1, not 0 — otherwise a paused or newly-added tracker renders as a
// week of red.
func TestConnectionUptimeNoDataIsNotAnOutage(t *testing.T) {
	var empty ConnectionDay
	if got := empty.Uptime(); got != -1 {
		t.Errorf("Uptime() with no attempts = %v, want -1", got)
	}
	if empty.Attempts() != 0 {
		t.Errorf("Attempts() = %d, want 0", empty.Attempts())
	}
}

// TestConnectionFailureAlwaysRecordsAKind: a failure with no error kind must
// still be distinguishable from a success, or the last_kind CASE would treat
// it as "nothing went wrong".
func TestConnectionFailureAlwaysRecordsAKind(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	if err := db.RecordConnection("y", now, false, "", "api"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ConnectionDaily([]string{"y"}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].LastKind == "" {
		t.Fatalf("kindless failure lost its marker: %+v", rows)
	}
}

// TestConnectionPruneAndPurge covers retention and the per-tracker delete.
func TestConnectionPruneAndPurge(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.RecordConnection("a", now.Add(-40*24*time.Hour), true, "", "api"))
	must(db.RecordConnection("a", now, true, "", "api"))
	must(db.RecordConnection("b", now, false, "timeout", "api"))

	must(db.PruneConnectionDaily(now.Add(-30 * 24 * time.Hour)))
	rows, err := db.ConnectionDaily(nil, now.Add(-90*24*time.Hour))
	must(err)
	if len(rows) != 2 {
		t.Errorf("after prune = %d rows, want 2 (%+v)", len(rows), rows)
	}

	must(db.DeleteTracker("a"))
	rows, err = db.ConnectionDaily(nil, now.Add(-90*24*time.Hour))
	must(err)
	if len(rows) != 1 || rows[0].TrackerID != "b" {
		t.Errorf("DeleteTracker left connection rows behind: %+v", rows)
	}
}
