package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func alertDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mkAlert(rule, tracker string, at time.Time) Alert {
	return Alert{
		At: at.Unix(), RuleID: rule, RuleName: rule + " rule",
		TrackerID: tracker, TrackerName: tracker + " tracker",
		Title: "Yata alert: " + rule, Body: tracker + " — something happened",
	}
}

func TestAddAndListAlerts(t *testing.T) {
	db := alertDB(t)
	now := time.Now().UTC()
	for i, r := range []string{"r1", "r2", "r3"} {
		if err := db.AddAlert(mkAlert(r, "t1", now.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	got, total, err := db.Alerts(AlertQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(got) != 3 {
		t.Fatalf("total=%d len=%d, want 3/3", total, len(got))
	}
	if got[0].RuleID != "r3" {
		t.Errorf("first = %q, want r3 — newest first", got[0].RuleID)
	}
	if n, _ := db.UnreadAlerts(); n != 3 {
		t.Errorf("unread = %d, want 3", n)
	}
}

// The engine's edge state is in memory, so a restart re-fires a condition that
// is still true. One unread row for it is enough.
func TestAddAlertSkipsDuplicateWhileUnread(t *testing.T) {
	db := alertDB(t)
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if err := db.AddAlert(mkAlert("r1", "t1", now)); err != nil {
			t.Fatal(err)
		}
	}
	_, total, _ := db.Alerts(AlertQuery{})
	if total != 1 {
		t.Fatalf("total = %d, want 1 — restarts must not stack rows", total)
	}

	// Different tracker, same rule: a separate condition, so a separate row.
	if err := db.AddAlert(mkAlert("r1", "t2", now)); err != nil {
		t.Fatal(err)
	}
	if _, total, _ = db.Alerts(AlertQuery{}); total != 2 {
		t.Errorf("total = %d, want 2 — the guard is per rule AND tracker", total)
	}

	// Once read, the same condition firing again is news.
	if err := db.MarkAlertsRead(nil, now); err != nil {
		t.Fatal(err)
	}
	if err := db.AddAlert(mkAlert("r1", "t1", now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, total, _ = db.Alerts(AlertQuery{}); total != 3 {
		t.Errorf("total = %d, want 3 — a re-fire after reading is a new row", total)
	}
}

func TestMarkAlertsRead(t *testing.T) {
	db := alertDB(t)
	now := time.Now().UTC()
	for _, r := range []string{"r1", "r2", "r3"} {
		if err := db.AddAlert(mkAlert(r, "t1", now)); err != nil {
			t.Fatal(err)
		}
	}
	all, _, _ := db.Alerts(AlertQuery{})
	if err := db.MarkAlertsRead([]int64{all[0].ID}, now); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.UnreadAlerts(); n != 2 {
		t.Errorf("unread = %d, want 2", n)
	}

	// Marking everything read must not rewrite the row already read.
	later := now.Add(time.Hour)
	if err := db.MarkAlertsRead(nil, later); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.UnreadAlerts(); n != 0 {
		t.Errorf("unread = %d, want 0", n)
	}
	after, _, _ := db.Alerts(AlertQuery{})
	for _, a := range after {
		if a.ID == all[0].ID && a.ReadAt != now.Unix() {
			t.Errorf("read_at = %d, want the original %d", a.ReadAt, now.Unix())
		}
	}
}

func TestAlertsFilter(t *testing.T) {
	db := alertDB(t)
	now := time.Now().UTC()
	if err := db.AddAlert(mkAlert("ratio", "aither", now)); err != nil {
		t.Fatal(err)
	}
	if err := db.AddAlert(mkAlert("login", "zenith", now)); err != nil {
		t.Fatal(err)
	}
	global := mkAlert("client", "", now)
	global.TrackerName = ""
	if err := db.AddAlert(global); err != nil {
		t.Fatal(err)
	}

	got, total, _ := db.Alerts(AlertQuery{TrackerID: "zenith"})
	if total != 1 || got[0].RuleID != "login" {
		t.Errorf("tracker filter = %+v (total %d)", got, total)
	}
	// "app" selects the signals that belong to no tracker.
	got, total, _ = db.Alerts(AlertQuery{TrackerID: "app"})
	if total != 1 || got[0].RuleID != "client" {
		t.Errorf("app filter = %+v (total %d)", got, total)
	}
	if _, total, _ = db.Alerts(AlertQuery{Search: "AITHER"}); total != 1 {
		t.Errorf("search total = %d, want 1 (case-insensitive)", total)
	}
	if _, total, _ = db.Alerts(AlertQuery{Search: "nothing here"}); total != 0 {
		t.Errorf("search total = %d, want 0", total)
	}
}

// A search for "%" looks for a percent sign; it must not match everything.
func TestAlertsSearchEscapesWildcards(t *testing.T) {
	db := alertDB(t)
	now := time.Now().UTC()
	if err := db.AddAlert(mkAlert("ratio", "aither", now)); err != nil {
		t.Fatal(err)
	}
	pct := mkAlert("seedsize", "lst", now)
	pct.Body = "seed size dropped 40% this week"
	if err := db.AddAlert(pct); err != nil {
		t.Fatal(err)
	}
	got, total, _ := db.Alerts(AlertQuery{Search: "%"})
	if total != 1 || got[0].RuleID != "seedsize" {
		t.Errorf("search %%: total=%d got=%+v, want only the row containing a percent sign", total, got)
	}
	if _, total, _ = db.Alerts(AlertQuery{Search: "_"}); total != 0 {
		t.Errorf("search _: total=%d, want 0 — underscore is a literal, not a wildcard", total)
	}
}

// The count must describe the same set as the page, or paging lies.
func TestAlertsPagingCountMatchesFilter(t *testing.T) {
	db := alertDB(t)
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if err := db.AddAlert(mkAlert(fmt.Sprintf("r%d", i), "aither", now.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AddAlert(mkAlert("other", "zenith", now)); err != nil {
		t.Fatal(err)
	}
	got, total, _ := db.Alerts(AlertQuery{TrackerID: "aither", Limit: 2})
	if total != 5 {
		t.Errorf("total = %d, want 5 — the count follows the filter, not the page", total)
	}
	if len(got) != 2 {
		t.Errorf("page = %d rows, want 2", len(got))
	}
	next, _, _ := db.Alerts(AlertQuery{TrackerID: "aither", Limit: 2, Offset: 2})
	if len(next) != 2 || next[0].ID == got[0].ID {
		t.Errorf("offset returned the same page: %+v", next)
	}
}

// Unread alerts are outstanding work and survive the age cut; the newest-N cap
// still applies to everything.
func TestPruneAlerts(t *testing.T) {
	db := alertDB(t)
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -120)

	if err := db.AddAlert(mkAlert("oldread", "t1", old)); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkAlertsRead(nil, old); err != nil {
		t.Fatal(err)
	}
	if err := db.AddAlert(mkAlert("oldunread", "t2", old)); err != nil {
		t.Fatal(err)
	}
	if err := db.PruneAlerts(now); err != nil {
		t.Fatal(err)
	}
	got, total, _ := db.Alerts(AlertQuery{})
	if total != 1 || got[0].RuleID != "oldunread" {
		t.Fatalf("after prune = %+v (total %d), want the unread one kept", got, total)
	}
}

func TestPruneAlertsCapsCount(t *testing.T) {
	db := alertDB(t)
	now := time.Now().UTC()
	for i := 0; i < alertKeepNewest+10; i++ {
		a := mkAlert(fmt.Sprintf("r%d", i), "t1", now.Add(time.Duration(i)*time.Second))
		if err := db.AddAlert(a); err != nil {
			t.Fatal(err)
		}
		if err := db.MarkAlertsRead(nil, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.PruneAlerts(now); err != nil {
		t.Fatal(err)
	}
	_, total, _ := db.Alerts(AlertQuery{})
	if total != alertKeepNewest {
		t.Errorf("total = %d, want %d", total, alertKeepNewest)
	}
}

// An alert records something that happened, so removing its tracker must not
// erase or blank it — unlike every other tracker-scoped table.
func TestAlertsSurviveTrackerDeletion(t *testing.T) {
	db := alertDB(t)
	if err := db.AddAlert(mkAlert("r1", "t1", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteTracker("t1"); err != nil {
		t.Fatal(err)
	}
	got, total, _ := db.Alerts(AlertQuery{})
	if total != 1 {
		t.Fatalf("total = %d, want 1 — history must outlive the tracker", total)
	}
	if got[0].TrackerName != "t1 tracker" {
		t.Errorf("tracker_name = %q, want the name as it was", got[0].TrackerName)
	}
}

func TestDeleteAlert(t *testing.T) {
	db := alertDB(t)
	now := time.Now().UTC()
	if err := db.AddAlert(mkAlert("r1", "t1", now)); err != nil {
		t.Fatal(err)
	}
	got, _, _ := db.Alerts(AlertQuery{})
	if err := db.DeleteAlert(got[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := db.Alerts(AlertQuery{}); total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	if n, _ := db.UnreadAlerts(); n != 0 {
		t.Errorf("unread = %d, want 0 — a cleared row must not still count", n)
	}
}

// The filter's options must describe every source, not the current view — the
// list is built from this, and from the visible page it collapsed to whichever
// tracker was already selected, with no way back to the others.
func TestAlertSources(t *testing.T) {
	db := alertDB(t)
	now := time.Now().UTC()
	for _, tr := range []string{"aither", "zenith", "aither"} {
		if err := db.AddAlert(mkAlert("r"+tr, tr, now)); err != nil {
			t.Fatal(err)
		}
	}
	global := mkAlert("client", "", now)
	global.TrackerName = ""
	if err := db.AddAlert(global); err != nil {
		t.Fatal(err)
	}

	got, err := db.AlertSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("sources = %+v, want 3 distinct", got)
	}
	// The no-tracker signal sorts first; trackers follow by name.
	if got[0].TrackerID != "" {
		t.Errorf("first source = %+v, want the global one", got[0])
	}
	if got[1].TrackerID != "aither" || got[2].TrackerID != "zenith" {
		t.Errorf("order = %+v, want aither then zenith", got[1:])
	}
}

// A tracker renamed between alerts reports its most recent name, while the
// older rows keep the name they were filed under.
func TestAlertSourcesUsesLatestName(t *testing.T) {
	db := alertDB(t)
	now := time.Now().UTC()
	first := mkAlert("r1", "t1", now)
	first.TrackerName = "Old Name"
	if err := db.AddAlert(first); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkAlertsRead(nil, now); err != nil {
		t.Fatal(err)
	}
	second := mkAlert("r2", "t1", now.Add(time.Minute))
	second.TrackerName = "New Name"
	if err := db.AddAlert(second); err != nil {
		t.Fatal(err)
	}

	got, _ := db.AlertSources()
	if len(got) != 1 || got[0].TrackerName != "New Name" {
		t.Errorf("sources = %+v, want the latest name", got)
	}
	rows, _, _ := db.Alerts(AlertQuery{})
	if rows[1].TrackerName != "Old Name" {
		t.Errorf("older row = %q, want the name it was filed under", rows[1].TrackerName)
	}
}
