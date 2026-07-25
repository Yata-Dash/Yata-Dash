package store

import "time"

// TrackerEvent is a point-in-time annotation for the History view and the
// Tracker Detail timelines (`kind` leaves room for freeleech windows etc.).
type TrackerEvent struct {
	TrackerID string `json:"tracker_id"`
	At   int64  `json:"at"`
	Kind string `json:"kind"`
	// group_change:        "Seeker→PowerPool"
	// connection_api,
	// connection_scrape:   "up" | "down:<errorKind>" — written on state CHANGES
	//                      only, per channel, so consecutive identical readings
	//                      collapse to one event and a failing API doesn't
	//                      flap against a working scrape.
	// connection:          the pre-split kind, no channel. Still read, never
	//                      written; see connectionKind in internal/api/stats.go.
	Detail string `json:"detail"`
}

// AddEvent records one tracker event.
func (d *DB) AddEvent(trackerID string, at time.Time, kind, detail string) error {
	_, err := d.sql.Exec(
		`INSERT INTO tracker_events (tracker_id, at, kind, detail) VALUES (?, ?, ?, ?)`,
		trackerID, at.Unix(), kind, detail)
	return err
}

// EventsSince returns events at/after `since`, oldest first, optionally
// filtered to the given trackers (empty = all). Mirrors the series filters so
// the History feed can fetch events for exactly the charted trackers/window.
func (d *DB) EventsSince(trackerIDs []string, since time.Time) ([]TrackerEvent, error) {
	q := `SELECT tracker_id, at, kind, detail FROM tracker_events WHERE at >= ?`
	args := []any{since.Unix()}
	if len(trackerIDs) > 0 {
		q += ` AND tracker_id IN (` + placeholders(len(trackerIDs)) + `)`
		for _, id := range trackerIDs {
			args = append(args, id)
		}
	}
	q += ` ORDER BY at ASC, tracker_id ASC`
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrackerEvent
	for rows.Next() {
		var e TrackerEvent
		if err := rows.Scan(&e.TrackerID, &e.At, &e.Kind, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LatestEventDetail returns the detail of the most recent event of a kind for a
// tracker (empty when none). Used to de-dupe repeated group readings.
func (d *DB) LatestEventDetail(trackerID, kind string) (string, error) {
	var detail string
	err := d.sql.QueryRow(
		`SELECT detail FROM tracker_events WHERE tracker_id = ? AND kind = ? ORDER BY at DESC LIMIT 1`,
		trackerID, kind).Scan(&detail)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return "", nil
		}
		return "", err
	}
	return detail, nil
}

// PruneEvents deletes events older than `before` (kept in step with the daily
// history retention so the timeline covers the same window the chart can show).
func (d *DB) PruneEvents(before time.Time) error {
	_, err := d.sql.Exec(`DELETE FROM tracker_events WHERE at < ?`, before.Unix())
	return err
}
