// Package store owns the SQLite database: stat layers (the unified stats
// engine's persistence), numeric history for sparklines, and the scrape log
// used for persistent rate limiting.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite handle.
type DB struct {
	sql *sql.DB
}

// Open opens (creating if needed) the database at path and migrates the schema.
func Open(path string) (*DB, error) {
	h, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// modernc/sqlite is happiest with a single writer connection.
	h.SetMaxOpenConns(1)
	db := &DB{sql: h}
	if err := db.migrate(); err != nil {
		h.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.sql.Close() }

func (d *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS stat_layers (
			tracker_id TEXT NOT NULL,
			source     TEXT NOT NULL,             -- 'api' | 'scrape'
			field      TEXT NOT NULL,             -- canonical field name
			value      TEXT NOT NULL,             -- JSON-encoded value
			updated_at INTEGER NOT NULL,          -- unix seconds
			PRIMARY KEY (tracker_id, source, field)
		)`,
		`CREATE TABLE IF NOT EXISTS history (
			tracker_id  TEXT NOT NULL,
			recorded_at INTEGER NOT NULL,         -- unix seconds
			field       TEXT NOT NULL,            -- canonical numeric field
			value       REAL NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_history_tracker ON history (tracker_id, field, recorded_at)`,
		`CREATE INDEX IF NOT EXISTS idx_history_time ON history (recorded_at)`,
		// One value per tracker/field/UTC-day (the day's latest). Drives the
		// stable long-term growth rate used for trend projections — a single
		// slow day can't skew it, and it survives restarts so projections are
		// available from setup. Tiny: ~1 row/field/day.
		`CREATE TABLE IF NOT EXISTS history_daily (
			tracker_id TEXT NOT NULL,
			day        INTEGER NOT NULL,         -- unix seconds at UTC midnight
			field      TEXT NOT NULL,
			value      REAL NOT NULL,
			PRIMARY KEY (tracker_id, day, field)
		)`,
		// One row per scrape ATTEMPT (success or failure) — the persistent
		// rate-limit ledger, and since the outcome columns were added, the
		// scrape-health record (failure streaks / dead-cookie detection).
		`CREATE TABLE IF NOT EXISTS scrape_log (
			tracker_id TEXT NOT NULL,
			scraped_at INTEGER NOT NULL,          -- unix seconds
			ok         INTEGER NOT NULL DEFAULT 1,
			error_kind TEXT NOT NULL DEFAULT ''   -- scrape.Error.Kind when ok=0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_scrape_log ON scrape_log (tracker_id, scraped_at)`,
		// Single-user basic auth (id is pinned to 1 — at most one account).
		`CREATE TABLE IF NOT EXISTS auth (
			id            INTEGER PRIMARY KEY CHECK (id = 1),
			username      TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			created_at    INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token      TEXT PRIMARY KEY,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON sessions (expires_at)`,
		// Tracker events — point-in-time annotations for the History view
		// (group promotions/demotions; extensible via `kind`). Written
		// opportunistically by the refresh path; tiny, one row per change.
		`CREATE TABLE IF NOT EXISTS tracker_events (
			tracker_id TEXT NOT NULL,
			at         INTEGER NOT NULL,         -- unix seconds
			kind       TEXT NOT NULL,            -- 'group_change' (more later)
			detail     TEXT NOT NULL             -- e.g. "Seeker→PowerPool"
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_tracker ON tracker_events (tracker_id, at)`,
		// Read-only integration tokens (Settings → Integrations → API Tokens).
		// Only the SHA-256 hash is stored; the plaintext token is shown once.
		`CREATE TABLE IF NOT EXISTS api_tokens (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL,
			prefix       TEXT NOT NULL,             -- display hint (yata_xxxx…)
			hash         TEXT NOT NULL UNIQUE,      -- sha256 hex of the token
			created_at   INTEGER NOT NULL,
			last_used_at INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, s := range stmts {
		if _, err := d.sql.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// Column additions to pre-existing tables (CREATE IF NOT EXISTS skips them
	// on old databases). Pre-migration scrape_log rows default to ok=1: they
	// were rate-ledger entries whose outcome was never recorded.
	if err := d.addColumns("scrape_log", map[string]string{
		"ok":         "INTEGER NOT NULL DEFAULT 1",
		"error_kind": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return err
	}
	return nil
}

// addColumns adds any of the given columns missing from table (name → type
// clause). SQLite has no ADD COLUMN IF NOT EXISTS, so existing columns are
// read from table_info first.
func (d *DB) addColumns(table string, cols map[string]string) error {
	rows, err := d.sql.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("migrate: %w", err)
		}
		have[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for name, clause := range cols {
		if have[name] {
			continue
		}
		if _, err := d.sql.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, name, clause)); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Stat layers
// ─────────────────────────────────────────────────────────────────────────────

// FieldValue is one stored field within a layer.
type FieldValue struct {
	Value     any
	UpdatedAt int64
}

// ReplaceLayer atomically replaces an entire source layer for a tracker.
// A fetch/scrape always produces a full snapshot of what that source knows,
// so stale fields from previous runs must not linger.
func (d *DB) ReplaceLayer(trackerID, source string, fields map[string]any, at time.Time) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM stat_layers WHERE tracker_id = ? AND source = ?`, trackerID, source); err != nil {
		return err
	}
	ins, err := tx.Prepare(`INSERT INTO stat_layers (tracker_id, source, field, value, updated_at) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	ts := at.Unix()
	for field, val := range fields {
		enc, err := json.Marshal(val)
		if err != nil {
			continue // unserialisable value — skip rather than fail the layer
		}
		if _, err := ins.Exec(trackerID, source, field, string(enc), ts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Layer returns one source layer for a tracker.
func (d *DB) Layer(trackerID, source string) (map[string]FieldValue, error) {
	rows, err := d.sql.Query(
		`SELECT field, value, updated_at FROM stat_layers WHERE tracker_id = ? AND source = ?`,
		trackerID, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]FieldValue{}
	for rows.Next() {
		var field, raw string
		var ts int64
		if err := rows.Scan(&field, &raw, &ts); err != nil {
			return nil, err
		}
		var val any
		if err := json.Unmarshal([]byte(raw), &val); err != nil {
			continue
		}
		out[field] = FieldValue{Value: val, UpdatedAt: ts}
	}
	return out, rows.Err()
}

// Layers returns both layers for a tracker keyed by source.
func (d *DB) Layers(trackerID string) (map[string]map[string]FieldValue, error) {
	rows, err := d.sql.Query(
		`SELECT source, field, value, updated_at FROM stat_layers WHERE tracker_id = ?`, trackerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]FieldValue{}
	for rows.Next() {
		var source, field, raw string
		var ts int64
		if err := rows.Scan(&source, &field, &raw, &ts); err != nil {
			return nil, err
		}
		var val any
		if err := json.Unmarshal([]byte(raw), &val); err != nil {
			continue
		}
		if out[source] == nil {
			out[source] = map[string]FieldValue{}
		}
		out[source][field] = FieldValue{Value: val, UpdatedAt: ts}
	}
	return out, rows.Err()
}

// WipeData clears all per-tracker data (stat layers, history, scrape log). Used
// by the login-reset recovery flow. Auth + sessions are cleared separately.
func (d *DB) WipeData() error {
	for _, q := range []string{
		`DELETE FROM stat_layers`,
		`DELETE FROM history`,
		`DELETE FROM history_daily`,
		`DELETE FROM scrape_log`,
		`DELETE FROM tracker_events`,
		`DELETE FROM api_tokens`, // a recovery reset revokes integrations too
	} {
		if _, err := d.sql.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// DeleteTracker removes all stored data for a tracker (layers, history, log).
func (d *DB) DeleteTracker(trackerID string) error {
	for _, q := range []string{
		`DELETE FROM stat_layers WHERE tracker_id = ?`,
		`DELETE FROM history WHERE tracker_id = ?`,
		`DELETE FROM history_daily WHERE tracker_id = ?`,
		`DELETE FROM scrape_log WHERE tracker_id = ?`,
		`DELETE FROM tracker_events WHERE tracker_id = ?`,
	} {
		if _, err := d.sql.Exec(q, trackerID); err != nil {
			return err
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// History (sparklines)
// ─────────────────────────────────────────────────────────────────────────────

// HistoryPoint is one recorded numeric value.
type HistoryPoint struct {
	TrackerID  string  `json:"tracker_id"`
	RecordedAt int64   `json:"recorded_at"`
	Field      string  `json:"field"`
	Value      float64 `json:"value"`
}

// AddHistory records numeric fields for a tracker at time at.
func (d *DB) AddHistory(trackerID string, at time.Time, fields map[string]float64) error {
	if len(fields) == 0 {
		return nil
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ins, err := tx.Prepare(`INSERT INTO history (tracker_id, recorded_at, field, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	ts := at.Unix()
	for f, v := range fields {
		if _, err := ins.Exec(trackerID, ts, f, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneHistory deletes points older than before.
func (d *DB) PruneHistory(before time.Time) error {
	_, err := d.sql.Exec(`DELETE FROM history WHERE recorded_at < ?`, before.Unix())
	return err
}

// TrackerHistorySince returns one tracker's fine-grained points since `since`,
// oldest first (used for early growth rates before daily rollups exist).
func (d *DB) TrackerHistorySince(trackerID string, since time.Time) ([]HistoryPoint, error) {
	rows, err := d.sql.Query(
		`SELECT recorded_at, field, value FROM history WHERE tracker_id = ? AND recorded_at >= ? ORDER BY recorded_at ASC`,
		trackerID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryPoint
	for rows.Next() {
		p := HistoryPoint{TrackerID: trackerID}
		if err := rows.Scan(&p.RecordedAt, &p.Field, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────────
// Daily rollups (stable long-term trends)
// ─────────────────────────────────────────────────────────────────────────────

func utcDay(at time.Time) int64 {
	u := at.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).Unix()
}

// RecordDaily upserts the latest value of each field for the UTC day of `at`.
// The last write of the day wins, so each day ends with that day's final value.
func (d *DB) RecordDaily(trackerID string, at time.Time, fields map[string]float64) error {
	if len(fields) == 0 {
		return nil
	}
	day := utcDay(at)
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	up, err := tx.Prepare(`INSERT INTO history_daily (tracker_id, day, field, value) VALUES (?, ?, ?, ?)
		ON CONFLICT(tracker_id, day, field) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return err
	}
	defer up.Close()
	for f, v := range fields {
		if _, err := up.Exec(trackerID, day, f, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DailySince returns one tracker's daily rollup points since `since`, oldest
// first. RecordedAt carries the day's UTC-midnight timestamp.
func (d *DB) DailySince(trackerID string, since time.Time) ([]HistoryPoint, error) {
	rows, err := d.sql.Query(
		`SELECT day, field, value FROM history_daily WHERE tracker_id = ? AND day >= ? ORDER BY day ASC`,
		trackerID, utcDay(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryPoint
	for rows.Next() {
		p := HistoryPoint{TrackerID: trackerID}
		if err := rows.Scan(&p.RecordedAt, &p.Field, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AllDailySince returns daily rollup points for ALL trackers since `since`,
// oldest first (used for the history CSV export). RecordedAt carries the day's
// UTC-midnight timestamp.
func (d *DB) AllDailySince(since time.Time) ([]HistoryPoint, error) {
	rows, err := d.sql.Query(
		`SELECT tracker_id, day, field, value FROM history_daily WHERE day >= ? ORDER BY day ASC, tracker_id ASC`,
		utcDay(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryPoint
	for rows.Next() {
		var p HistoryPoint
		if err := rows.Scan(&p.TrackerID, &p.RecordedAt, &p.Field, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PruneDaily deletes daily rollups older than before.
func (d *DB) PruneDaily(before time.Time) error {
	_, err := d.sql.Exec(`DELETE FROM history_daily WHERE day < ?`, utcDay(before))
	return err
}

// ─────────────────────────────────────────────────────────────────────────────
// Filtered series (History view) — generalized DailySince with optional
// tracker/field filters so payloads stay bounded server-side. The unfiltered
// original above stays intact for its existing callers.
// ─────────────────────────────────────────────────────────────────────────────

// SeriesFine returns fine-grained history points at or after since, oldest
// first, filtered to the given tracker IDs and fields (empty slice = all).
func (d *DB) SeriesFine(trackerIDs, fields []string, since time.Time) ([]HistoryPoint, error) {
	q, args := seriesQuery(
		`SELECT tracker_id, recorded_at, field, value FROM history`,
		"recorded_at", trackerIDs, fields, since.Unix())
	return d.queryPoints(q, args)
}

// SeriesDaily returns daily rollup points at or after since, oldest first,
// filtered like SeriesFine. RecordedAt carries the day's UTC midnight.
func (d *DB) SeriesDaily(trackerIDs, fields []string, since time.Time) ([]HistoryPoint, error) {
	q, args := seriesQuery(
		`SELECT tracker_id, day, field, value FROM history_daily`,
		"day", trackerIDs, fields, utcDay(since))
	return d.queryPoints(q, args)
}

// seriesQuery builds the filtered WHERE clause shared by both series tables.
func seriesQuery(sel, timeCol string, trackerIDs, fields []string, sinceUnix int64) (string, []any) {
	q := sel + ` WHERE ` + timeCol + ` >= ?`
	args := []any{sinceUnix}
	if len(trackerIDs) > 0 {
		q += ` AND tracker_id IN (` + placeholders(len(trackerIDs)) + `)`
		for _, id := range trackerIDs {
			args = append(args, id)
		}
	}
	if len(fields) > 0 {
		q += ` AND field IN (` + placeholders(len(fields)) + `)`
		for _, f := range fields {
			args = append(args, f)
		}
	}
	q += ` ORDER BY ` + timeCol + ` ASC, tracker_id ASC`
	return q, args
}

func placeholders(n int) string {
	s := make([]byte, 0, n*2)
	for i := range n {
		if i > 0 {
			s = append(s, ',')
		}
		s = append(s, '?')
	}
	return string(s)
}

func (d *DB) queryPoints(q string, args []any) ([]HistoryPoint, error) {
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryPoint
	for rows.Next() {
		var p HistoryPoint
		if err := rows.Scan(&p.TrackerID, &p.RecordedAt, &p.Field, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────────
// Scrape log (persistent rate limiting + scrape health)
// ─────────────────────────────────────────────────────────────────────────────

// RecordScrape logs one scrape attempt — success or failure — at time at.
// Every attempt counts toward the rate-limit ledger; the outcome feeds the
// scrape-health view (failure streaks, dead-cookie detection).
func (d *DB) RecordScrape(trackerID string, at time.Time, ok bool, errorKind string) error {
	if ok {
		errorKind = ""
	}
	_, err := d.sql.Exec(
		`INSERT INTO scrape_log (tracker_id, scraped_at, ok, error_kind) VALUES (?, ?, ?, ?)`,
		trackerID, at.Unix(), ok, errorKind)
	return err
}

// ScrapeHealth is the outcome summary of a tracker's recent scrape attempts.
type ScrapeHealth struct {
	LastOK              bool   // latest attempt succeeded (true when no attempts)
	LastKind            string // error kind of the latest attempt ("" when ok)
	LastAt              int64  // unix time of the latest attempt (0 = none)
	PrevFailKind        string // error kind of the attempt before it ("" if ok/none)
	ConsecutiveFailures int    // failures since the last success (log retention caps it)
}

// GetScrapeHealth summarises the tail of the scrape log for one tracker.
func (d *DB) GetScrapeHealth(trackerID string) (ScrapeHealth, error) {
	h := ScrapeHealth{LastOK: true}
	rows, err := d.sql.Query(
		`SELECT ok, error_kind, scraped_at FROM scrape_log
		 WHERE tracker_id = ? ORDER BY scraped_at DESC, rowid DESC LIMIT 2`, trackerID)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for i := 0; rows.Next(); i++ {
		var ok bool
		var kind string
		var at int64
		if err := rows.Scan(&ok, &kind, &at); err != nil {
			return h, err
		}
		if i == 0 {
			h.LastOK, h.LastKind, h.LastAt = ok, kind, at
		} else if !ok {
			h.PrevFailKind = kind
		}
	}
	if err := rows.Err(); err != nil {
		return h, err
	}
	if h.LastOK {
		return h, nil // streak is 0 by definition; skip the count query
	}
	err = d.sql.QueryRow(
		`SELECT COUNT(*) FROM scrape_log WHERE tracker_id = ? AND scraped_at >
		   COALESCE((SELECT MAX(scraped_at) FROM scrape_log WHERE tracker_id = ? AND ok = 1), 0)`,
		trackerID, trackerID).Scan(&h.ConsecutiveFailures)
	return h, err
}

// LastScrape returns the unix time of the most recent scrape (0 if none).
func (d *DB) LastScrape(trackerID string) (int64, error) {
	var ts sql.NullInt64
	err := d.sql.QueryRow(`SELECT MAX(scraped_at) FROM scrape_log WHERE tracker_id = ?`, trackerID).Scan(&ts)
	if err != nil {
		return 0, err
	}
	return ts.Int64, nil
}

// ScrapesSince counts scrapes at or after since (used for the UTC daily cap).
func (d *DB) ScrapesSince(trackerID string, since time.Time) (int, error) {
	var n int
	err := d.sql.QueryRow(
		`SELECT COUNT(*) FROM scrape_log WHERE tracker_id = ? AND scraped_at >= ?`,
		trackerID, since.Unix()).Scan(&n)
	return n, err
}

// PruneScrapeLog deletes log entries older than before (housekeeping).
func (d *DB) PruneScrapeLog(before time.Time) error {
	_, err := d.sql.Exec(`DELETE FROM scrape_log WHERE scraped_at < ?`, before.Unix())
	return err
}
