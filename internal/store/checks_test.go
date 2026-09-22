package store

import (
	"path/filepath"
	"testing"
)

// A recorded outcome is one row per channel, replaced on the next attempt,
// and it is still there after the database is closed and reopened — which is
// the whole reason it is in the store rather than in memory.
func TestChecksReplaceAndSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	must := func(c Check) {
		t.Helper()
		if err := db.RecordCheck(c); err != nil {
			t.Fatal(err)
		}
	}
	must(Check{TrackerID: "a", Channel: "api", At: 100, Status: "ok", Fields: 12, Source: "refresh"})
	must(Check{TrackerID: "a", Channel: "scrape", At: 101, Status: "fail", Detail: "session_expired", Source: "refresh"})
	must(Check{TrackerID: "a", Channel: "api", At: 200, Status: "fail", Detail: "http_500", Source: "test"})
	db.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	all, err := db.Checks()
	if err != nil {
		t.Fatal(err)
	}
	api := all["a"]["api"]
	if api.Status != "fail" || api.Detail != "http_500" || api.At != 200 || api.Source != "test" {
		t.Errorf("api row not replaced by the later attempt: %+v", api)
	}
	if scr := all["a"]["scrape"]; scr.Status != "fail" || scr.Detail != "session_expired" || scr.At != 101 {
		t.Errorf("scrape row = %+v", scr)
	}
	if err := db.DeleteTracker("a"); err != nil {
		t.Fatal(err)
	}
	if all, _ = db.Checks(); len(all["a"]) != 0 {
		t.Errorf("rows survived DeleteTracker: %+v", all["a"])
	}
}
