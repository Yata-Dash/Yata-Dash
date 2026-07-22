package store

import "time"

// Connection health — the record of whether Yata could actually REACH each
// tracker, as opposed to whether the numbers it brought back look good.
//
// Two things are stored, for two different questions:
//
//   - connection_daily (here): "how did contact go, day by day" — the uptime
//     strip, the Health card's number and sparkline.
//   - tracker_events kind "connection" (events.go): "when did it break and
//     when did it come back" — the Detail timeline and History markers.
//
// Both are fed from the same two call sites (the API fetch in
// internal/api/stats.go and the profile scrape's recordScrapeAttempt), so a
// tracker that is unreachable by BOTH routes counts once per attempt of each.
//
// scrape_log is deliberately NOT the source here. It is the rate-limit ledger:
// its retention is tuned for cooldown arithmetic, and its pre-migration rows
// default to ok=1 because their outcome was never recorded — replaying those
// as successes would silently inflate uptime.

// ConnectionDay is one tracker's contact outcomes for one UTC day.
type ConnectionDay struct {
	TrackerID string `json:"tracker_id"`
	Day       int64  `json:"day"` // unix seconds at UTC midnight
	OKCount   int    `json:"ok_count"`
	FailCount int    `json:"fail_count"`
	LastKind  string `json:"last_kind,omitempty"` // most recent failure kind that day
	// The API channel alone. Needed because a dead API hides behind a working
	// scrape fallback in the combined counts above: every refresh records one
	// API failure AND one scrape success, so the day looks half-healthy and
	// the tracker reports as reachable while its API has been down for days.
	APIOKCount   int `json:"api_ok_count"`
	APIFailCount int `json:"api_fail_count"`
}

// APIDown reports whether every API attempt recorded that day failed. False
// when the API was never tried (a scrape-only tracker must not read as broken).
func (c ConnectionDay) APIDown() bool {
	return c.APIFailCount > 0 && c.APIOKCount == 0
}

// APIAttempts is how many times the API channel was tried that day.
func (c ConnectionDay) APIAttempts() int { return c.APIOKCount + c.APIFailCount }

// Attempts is the total number of contacts recorded for the day.
func (c ConnectionDay) Attempts() int { return c.OKCount + c.FailCount }

// Uptime is the fraction of the day's contacts that succeeded (0..1), or -1
// when nothing was attempted — callers must render "no data" differently from
// a genuine 0% day, otherwise a paused or newly-added tracker reads as down.
func (c ConnectionDay) Uptime() float64 {
	n := c.Attempts()
	if n == 0 {
		return -1
	}
	return float64(c.OKCount) / float64(n)
}

// RecordConnection folds one contact attempt into its UTC day's rollup.
// source is "api" or "scrape"; API attempts are additionally tallied on their
// own so a dead API is still visible behind a working scrape fallback.
// last_kind is only overwritten by failures, so a day that ends with a
// success still reports what went wrong earlier in it.
func (d *DB) RecordConnection(trackerID string, at time.Time, ok bool, errorKind, source string) error {
	day := utcDay(at)
	okInc, failInc := 1, 0
	if !ok {
		okInc, failInc = 0, 1
	}
	apiOK, apiFail := 0, 0
	if source == "api" {
		apiOK, apiFail = okInc, failInc
	}
	_, err := d.sql.Exec(
		`INSERT INTO connection_daily
		   (tracker_id, day, ok_count, fail_count, last_kind, api_ok_count, api_fail_count)
		   VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tracker_id, day) DO UPDATE SET
		   ok_count       = ok_count       + excluded.ok_count,
		   fail_count     = fail_count     + excluded.fail_count,
		   api_ok_count   = api_ok_count   + excluded.api_ok_count,
		   api_fail_count = api_fail_count + excluded.api_fail_count,
		   last_kind      = CASE WHEN excluded.last_kind <> '' THEN excluded.last_kind ELSE last_kind END`,
		trackerID, day, okInc, failInc, failKind(ok, errorKind), apiOK, apiFail)
	return err
}

// failKind returns the error kind to store — empty on success, and a
// placeholder when a failure arrived without one, so "something failed" is
// never mistaken for "nothing failed" by the CASE above.
func failKind(ok bool, errorKind string) string {
	if ok {
		return ""
	}
	if errorKind == "" {
		return "error"
	}
	return errorKind
}

// ConnectionDaily returns daily rollups at or after `since`, oldest first,
// optionally filtered to the given trackers (empty = all). Mirrors
// EventsSince/SeriesDaily so the same window can be requested consistently.
func (d *DB) ConnectionDaily(trackerIDs []string, since time.Time) ([]ConnectionDay, error) {
	q := `SELECT tracker_id, day, ok_count, fail_count, last_kind, api_ok_count, api_fail_count
	        FROM connection_daily WHERE day >= ?`
	args := []any{utcDay(since)}
	if len(trackerIDs) > 0 {
		q += ` AND tracker_id IN (` + placeholders(len(trackerIDs)) + `)`
		for _, id := range trackerIDs {
			args = append(args, id)
		}
	}
	q += ` ORDER BY day ASC, tracker_id ASC`
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConnectionDay
	for rows.Next() {
		var c ConnectionDay
		if err := rows.Scan(&c.TrackerID, &c.Day, &c.OKCount, &c.FailCount, &c.LastKind,
			&c.APIOKCount, &c.APIFailCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PruneConnectionDaily deletes rollups older than `before`, kept in step with
// the daily history retention so the strip covers the same window the charts do.
func (d *DB) PruneConnectionDaily(before time.Time) error {
	_, err := d.sql.Exec(`DELETE FROM connection_daily WHERE day < ?`, utcDay(before))
	return err
}
