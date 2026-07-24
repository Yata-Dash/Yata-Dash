package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestScrapeHealth: the streak counts consecutive failures since the last
// success, resets on success, and the two most recent kinds are reported
// (the empty_scrape ×2 rule needs the previous failure's kind).
func TestScrapeHealth(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	at := func(min int) time.Time { return now.Add(time.Duration(min) * time.Minute) }

	// No attempts yet: healthy by definition.
	h, err := db.GetScrapeHealth("x")
	if err != nil {
		t.Fatal(err)
	}
	if !h.LastOK || h.ConsecutiveFailures != 0 || h.LastAt != 0 {
		t.Errorf("empty log should be healthy: %+v", h)
	}

	// success, then three failures → streak 3, latest kind + previous kind.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.RecordScrape("x", at(0), true, "ignored-on-ok"))
	must(db.RecordScrape("x", at(1), false, "timeout"))
	must(db.RecordScrape("x", at(2), false, "empty_scrape"))
	must(db.RecordScrape("x", at(3), false, "session_expired"))
	h, err = db.GetScrapeHealth("x")
	if err != nil {
		t.Fatal(err)
	}
	if h.LastOK || h.LastKind != "session_expired" || h.PrevFailKind != "empty_scrape" ||
		h.ConsecutiveFailures != 3 || h.LastAt != at(3).Unix() {
		t.Errorf("after 3 failures: %+v", h)
	}

	// A success resets the streak.
	must(db.RecordScrape("x", at(4), true, ""))
	h, err = db.GetScrapeHealth("x")
	if err != nil {
		t.Fatal(err)
	}
	if !h.LastOK || h.LastKind != "" || h.ConsecutiveFailures != 0 {
		t.Errorf("after success: %+v", h)
	}

	// Other trackers are independent.
	if h, _ := db.GetScrapeHealth("y"); !h.LastOK {
		t.Errorf("tracker y should be untouched: %+v", h)
	}
}

// TestScrapeHealthOrdersSameSecondAttemptsByInsertion: scraped_at only has
// one-second resolution, so two attempts landing in the same second (a rapid
// manual retry, or a fallback scrape right after an API failure) must still
// resolve "latest" as the one actually recorded last, not an arbitrary one —
// GetScrapeHealth's ordering needs a tiebreaker beyond scraped_at.
func TestScrapeHealthOrdersSameSecondAttemptsByInsertion(t *testing.T) {
	db := testDB(t)
	same := time.Unix(5000, 0)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.RecordScrape("x", same, false, "timeout"))
	must(db.RecordScrape("x", same, true, ""))

	h, err := db.GetScrapeHealth("x")
	if err != nil {
		t.Fatal(err)
	}
	if !h.LastOK || h.ConsecutiveFailures != 0 {
		t.Errorf("latest same-second attempt (success, inserted last) should win: %+v", h)
	}
}

// TestScrapeLogMigration: a database created before the outcome columns
// existed gains them on Open, and its old rows read back as successes.
func TestScrapeLogMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-migration table shape with one legacy row.
	for _, q := range []string{
		`DROP TABLE scrape_log`,
		`CREATE TABLE scrape_log (tracker_id TEXT NOT NULL, scraped_at INTEGER NOT NULL)`,
		`INSERT INTO scrape_log (tracker_id, scraped_at) VALUES ('x', 1000)`,
	} {
		if _, err := db.sql.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	db, err = Open(path) // migrate() must add ok + error_kind
	if err != nil {
		t.Fatalf("reopen after downgrade: %v", err)
	}
	defer db.Close()
	h, err := db.GetScrapeHealth("x")
	if err != nil {
		t.Fatal(err)
	}
	if !h.LastOK || h.LastAt != 1000 || h.ConsecutiveFailures != 0 {
		t.Errorf("legacy row should read as a success: %+v", h)
	}
	// New-format writes work alongside the migrated row.
	if err := db.RecordScrape("x", time.Unix(2000, 0), false, "session_expired"); err != nil {
		t.Fatal(err)
	}
	if h, _ = db.GetScrapeHealth("x"); h.LastOK || h.LastKind != "session_expired" || h.ConsecutiveFailures != 1 {
		t.Errorf("post-migration failure row: %+v", h)
	}
}
