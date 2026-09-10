package store

import (
	"path/filepath"
	"testing"
)

// Upgrading must not brick an install that already hit the bug being fixed.
//
// The unread guard used to be a SELECT COUNT followed by an INSERT, so two
// passes could both read zero and both write. The fix is a partial unique
// index — but CREATE UNIQUE INDEX over existing duplicates fails, and a failed
// migration aborts Open. Without the DELETE that runs first, this upgrade
// would refuse to start precisely the databases that needed it.
func TestUpgradeSurvivesExistingDuplicateUnreadAlerts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Put the database back into the shape an older build left it in: no
	// partial unique index, and duplicate unread rows for one rule/tracker.
	if _, err := db.sql.Exec(`DROP INDEX IF EXISTS idx_alerts_unread_one`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.sql.Exec(
			`INSERT INTO alerts (at, rule_id, rule_name, tracker_id, tracker_name, title, body, read_at)
			 VALUES (?, 'r1', 'Ratio', 't1', 'Aither', 'Yata alert: Ratio', ?, 0)`,
			1700000000+i, "body "+string(rune('a'+i))); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	// A read one must survive the cleanup: the constraint is only about unread.
	if _, err := db.sql.Exec(
		`INSERT INTO alerts (at, rule_id, rule_name, tracker_id, tracker_name, title, body, read_at)
		 VALUES (1699999999, 'r1', 'Ratio', 't1', 'Aither', 'Yata alert: Ratio', 'older, read', 1700000500)`); err != nil {
		t.Fatalf("read-alert insert: %v", err)
	}
	db.Close()

	// Reopening runs the migration over that database.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after upgrade: %v", err)
	}
	defer db2.Close()

	var unread, total int
	if err := db2.sql.QueryRow(`SELECT COUNT(*) FROM alerts WHERE read_at = 0`).Scan(&unread); err != nil {
		t.Fatal(err)
	}
	if err := db2.sql.QueryRow(`SELECT COUNT(*) FROM alerts`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if unread != 1 {
		t.Errorf("unread rows = %d, want 1", unread)
	}
	if total != 2 {
		t.Errorf("total rows = %d, want 2 — the read alert must not be swept up", total)
	}
}

// The guard itself, now that it is a constraint rather than a hope.
func TestAddAlertKeepsOneUnreadPerRuleAndTracker(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	a := Alert{At: 1700000000, RuleID: "r1", RuleName: "Ratio", TrackerID: "t1",
		TrackerName: "Aither", Title: "Yata alert: Ratio", Body: "first"}
	for i := 0; i < 3; i++ {
		if err := db.AddAlert(a); err != nil {
			t.Fatalf("AddAlert %d: %v", i, err)
		}
	}
	_, total, err := db.Alerts(AlertQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}

	// A different tracker is a different alert.
	b := a
	b.TrackerID, b.TrackerName = "t2", "Zenith"
	if err := db.AddAlert(b); err != nil {
		t.Fatal(err)
	}
	if _, total, _ = db.Alerts(AlertQuery{}); total != 2 {
		t.Errorf("total after a second tracker = %d, want 2", total)
	}
}
