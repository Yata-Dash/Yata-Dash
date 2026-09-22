package store

import "time"

// Last known outcome per contact channel — what the Trackers table's status
// column shows. One row per tracker per channel ("api" / "scrape"), replaced
// on every attempt, so it is a snapshot rather than a log: scrape_log keeps
// the attempts for rate-limit arithmetic, connection_daily the tallies for
// uptime, and this holds the one fact the column needs — what happened last
// time and when.
//
// Written by the Test button AND by ordinary refreshes, so the column no
// longer reads "Not tested" until someone presses Test: a refresh that just
// failed with an expired cookie is the same news, and it survives a restart.

// Check is one channel's last outcome. Status/Detail/Fields carry the same
// values as api.CheckResult (ok, fail, not_configured, …); Source says
// whether a Test produced it or a refresh did.
type Check struct {
	TrackerID string
	Channel   string // "api" | "scrape"
	At        int64  // unix seconds
	Status    string
	Detail    string
	Fields    int
	Source    string // "test" | "refresh"
}

// RecordCheck replaces the tracker's last outcome for that channel.
func (d *DB) RecordCheck(c Check) error {
	if c.At == 0 {
		c.At = time.Now().Unix()
	}
	_, err := d.sql.Exec(
		`INSERT INTO tracker_checks (tracker_id, channel, at, status, detail, fields, source)
		   VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tracker_id, channel) DO UPDATE SET
		   at = excluded.at, status = excluded.status, detail = excluded.detail,
		   fields = excluded.fields, source = excluded.source`,
		c.TrackerID, c.Channel, c.At, c.Status, c.Detail, c.Fields, c.Source)
	return err
}

// Checks returns every tracker's last outcomes, keyed by tracker ID then
// channel.
func (d *DB) Checks() (map[string]map[string]Check, error) {
	rows, err := d.sql.Query(
		`SELECT tracker_id, channel, at, status, detail, fields, source FROM tracker_checks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]Check{}
	for rows.Next() {
		var c Check
		if err := rows.Scan(&c.TrackerID, &c.Channel, &c.At, &c.Status, &c.Detail, &c.Fields, &c.Source); err != nil {
			return nil, err
		}
		if out[c.TrackerID] == nil {
			out[c.TrackerID] = map[string]Check{}
		}
		out[c.TrackerID][c.Channel] = c
	}
	return out, rows.Err()
}
