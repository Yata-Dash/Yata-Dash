package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// queryPlan returns SQLite's EXPLAIN QUERY PLAN detail lines for q.
func queryPlan(t testing.TB, db *DB, q string, args ...any) string {
	t.Helper()
	rows, err := db.sql.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		lines = append(lines, detail)
	}
	return strings.Join(lines, "; ")
}

// TestSeriesDailyUnfilteredUsesDayIndex: without a tracker filter the
// history_daily primary key (tracker_id, day, field) can't serve a day range,
// so the History view's all-trackers query scanned the whole table. The day
// index lets it seek.
func TestSeriesDailyUnfilteredUsesDayIndex(t *testing.T) {
	db := testDB(t)
	q, args := seriesQuery(`SELECT tracker_id, day, field, value FROM history_daily`,
		"day", nil, []string{"uploaded"}, utcDay(time.Now().Add(-7*24*time.Hour)))
	plan := queryPlan(t, db, q, args...)
	if !strings.Contains(plan, "idx_history_daily_day") {
		t.Fatalf("unfiltered SeriesDaily plan = %q, want it to use idx_history_daily_day", plan)
	}
	// AllDailySince (the CSV export) and PruneDaily share the same shape.
	if plan := queryPlan(t, db, `SELECT tracker_id, day, field, value FROM history_daily WHERE day >= ? ORDER BY day ASC, tracker_id ASC`, 0); !strings.Contains(plan, "idx_history_daily_day") {
		t.Errorf("AllDailySince plan = %q, want idx_history_daily_day", plan)
	}
}

// TestMigrateIdempotent: reopening an existing database re-runs every
// migration, the new index included, without error.
func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for i := 0; i < 2; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		db.Close()
	}
}

// TestPragmas: WAL is on, and synchronous is still FULL — security state
// (sessions, passwords, API tokens) shares the connection, so a commit must
// survive a power cut (see Open).
func TestPragmas(t *testing.T) {
	db := testDB(t)
	var mode string
	if err := db.sql.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var sync int
	if err := db.sql.QueryRow(`PRAGMA synchronous`).Scan(&sync); err != nil {
		t.Fatal(err)
	}
	if sync != 2 { // 0 OFF, 1 NORMAL, 2 FULL, 3 EXTRA
		t.Errorf("synchronous = %d, want 2 (FULL)", sync)
	}
}

// benchDailyDB seeds 20 trackers × 5 fields × 2 years of daily rollups.
func benchDailyDB(b *testing.B) *DB {
	b.Helper()
	db, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	tx, err := db.sql.Begin()
	if err != nil {
		b.Fatal(err)
	}
	st, err := tx.Prepare(`INSERT INTO history_daily (tracker_id, day, field, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	today := utcDay(time.Now())
	for tr := 0; tr < 20; tr++ {
		for day := 0; day < 730; day++ {
			for _, f := range []string{"uploaded", "downloaded", "ratio", "seed_size", "bonus"} {
				if _, err := st.Exec(fmt.Sprintf("tr-%02d", tr), today-int64(day)*86400, f, float64(day)); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
	st.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return db
}

// BenchmarkSeriesDailyAllTrackers: the History view's all-trackers query over
// the last week, against two years of rollups.
func BenchmarkSeriesDailyAllTrackers(b *testing.B) {
	db := benchDailyDB(b)
	since := time.Now().Add(-7 * 24 * time.Hour)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.SeriesDaily(nil, nil, since); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAddHistory: one fine-history write (a transaction per call) — the
// commit cost that synchronous=NORMAL would cut, and why Open keeps FULL
// anyway.
func BenchmarkAddHistory(b *testing.B) {
	db, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	at := time.Now()
	fields := map[string]float64{"uploaded": 1, "downloaded": 2, "ratio": 3, "seed_size": 4, "bonus": 5}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.AddHistory("tr", at.Add(time.Duration(i)*time.Second), fields); err != nil {
			b.Fatal(err)
		}
	}
}
