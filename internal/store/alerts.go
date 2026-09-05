package store

import (
	"strings"
	"time"
)

// Alert retention. Whichever bites first — independent of the history
// retention setting, which is chosen for charts and would otherwise decide how
// long a worklist survives.
const (
	alertKeepNewest = 500
	alertKeepDays   = 90
)

// Alert is one alert the rule engine raised.
//
// RuleName and TrackerName are stored, not looked up. An alert records
// something that happened: renaming the rule or removing the tracker later
// must not rewrite it or leave a blank row. The ids are kept so the UI can
// still link through while the target exists.
type Alert struct {
	ID          int64  `json:"id"`
	At          int64  `json:"at"`
	RuleID      string `json:"rule_id"`
	RuleName    string `json:"rule_name"`
	TrackerID   string `json:"tracker_id"`
	TrackerName string `json:"tracker_name"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	ReadAt      int64  `json:"read_at"`
}

// AddAlert records one alert, unless an unread one for the same rule and
// tracker is already waiting.
//
// That guard exists because the engine's edge state is in memory: a restart
// re-primes, so a condition that is still true fires again. For a webhook that
// is one extra message; for a list that accumulates it is a duplicate row per
// restart. It also happens to be honest — you have not read the first one, so
// a second says nothing new.
func (d *DB) AddAlert(a Alert) error {
	var n int
	if err := d.sql.QueryRow(
		`SELECT COUNT(*) FROM alerts WHERE read_at = 0 AND rule_id = ? AND tracker_id = ?`,
		a.RuleID, a.TrackerID).Scan(&n); err == nil && n > 0 {
		return nil
	}
	_, err := d.sql.Exec(
		`INSERT INTO alerts (at, rule_id, rule_name, tracker_id, tracker_name, title, body, read_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
		a.At, a.RuleID, a.RuleName, a.TrackerID, a.TrackerName, a.Title, a.Body)
	return err
}

// AlertQuery filters a listing. The zero value returns the most recent page.
type AlertQuery struct {
	// Search matches the rule name, tracker name, title or body,
	// case-insensitively.
	Search string
	// TrackerID limits to one tracker. The sentinel "app" selects alerts with
	// no tracker — the global signals.
	TrackerID string
	Limit     int
	Offset    int
}

// Alerts returns alerts newest first, with the total matching the same filter
// so the UI can page without a second call.
func (d *DB) Alerts(q AlertQuery) ([]Alert, int, error) {
	where, args := alertFilter(q)

	var total int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM alerts`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := q.Limit
	if limit <= 0 || limit > alertKeepNewest {
		limit = 50
	}
	rows, err := d.sql.Query(
		`SELECT id, at, rule_id, rule_name, tracker_id, tracker_name, title, body, read_at
		 FROM alerts`+where+` ORDER BY at DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.At, &a.RuleID, &a.RuleName,
			&a.TrackerID, &a.TrackerName, &a.Title, &a.Body, &a.ReadAt); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// alertFilter builds the shared WHERE clause, so the count and the page can
// never disagree about what is being listed.
func alertFilter(q AlertQuery) (string, []any) {
	var clauses []string
	var args []any
	switch {
	case q.TrackerID == "app":
		clauses = append(clauses, `tracker_id = ''`)
	case q.TrackerID != "":
		clauses = append(clauses, `tracker_id = ?`)
		args = append(args, q.TrackerID)
	}
	if s := strings.TrimSpace(q.Search); s != "" {
		clauses = append(clauses,
			`(rule_name LIKE ? ESCAPE '\' OR tracker_name LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\' OR body LIKE ? ESCAPE '\')`)
		like := "%" + escapeLike(s) + "%"
		args = append(args, like, like, like, like)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// escapeLike neutralises the wildcards in a user's search text, so typing "%"
// looks for a percent sign rather than matching everything.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// AlertSource is one distinct origin of an alert, for the panel's filter.
type AlertSource struct {
	// TrackerID is "" for the signals that belong to no tracker.
	TrackerID   string `json:"tracker_id"`
	TrackerName string `json:"tracker_name"`
}

// AlertSources lists every origin that has ever raised an alert.
//
// Deliberately unfiltered and unpaged: the filter dropdown has to describe the
// whole set. Built from the visible page instead, it collapsed to the one
// tracker already selected — leaving no way back to the others — and even
// unfiltered it would have missed any tracker whose alerts had scrolled past
// the first page.
//
// The name is each tracker's most recently RECORDED one, so a renamed tracker
// reads as it does now while older rows keep the name they were filed under.
func (d *DB) AlertSources() ([]AlertSource, error) {
	rows, err := d.sql.Query(
		`SELECT a.tracker_id, a.tracker_name FROM alerts a
		 WHERE a.id = (SELECT MAX(b.id) FROM alerts b WHERE b.tracker_id = a.tracker_id)
		 ORDER BY a.tracker_id = '' DESC, LOWER(a.tracker_name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertSource{}
	for rows.Next() {
		var s AlertSource
		if err := rows.Scan(&s.TrackerID, &s.TrackerName); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UnreadAlerts counts what the header bubble shows.
func (d *DB) UnreadAlerts() (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM alerts WHERE read_at = 0`).Scan(&n)
	return n, err
}

// MarkAlertsRead marks the given alerts read, or every unread one when ids is
// empty. Already-read rows keep their original timestamp.
func (d *DB) MarkAlertsRead(ids []int64, now time.Time) error {
	if len(ids) == 0 {
		_, err := d.sql.Exec(`UPDATE alerts SET read_at = ? WHERE read_at = 0`, now.Unix())
		return err
	}
	args := []any{now.Unix()}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := d.sql.Exec(
		`UPDATE alerts SET read_at = ? WHERE read_at = 0 AND id IN (`+placeholders(len(ids))+`)`, args...)
	return err
}

// DeleteAlert removes one alert outright — the UI's per-row clear, for a row
// the user is done with rather than merely done reading.
func (d *DB) DeleteAlert(id int64) error {
	_, err := d.sql.Exec(`DELETE FROM alerts WHERE id = ?`, id)
	return err
}

// PruneAlerts enforces both retention limits. Read rows go first: an unread
// alert is outstanding work and survives the age cut, so a deadline raised
// four months ago and never looked at is still there to look at.
func (d *DB) PruneAlerts(now time.Time) error {
	if _, err := d.sql.Exec(
		`DELETE FROM alerts WHERE read_at != 0 AND at < ?`,
		now.AddDate(0, 0, -alertKeepDays).Unix()); err != nil {
		return err
	}
	_, err := d.sql.Exec(
		`DELETE FROM alerts WHERE id NOT IN (
			SELECT id FROM alerts ORDER BY at DESC, id DESC LIMIT ?
		 )`, alertKeepNewest)
	return err
}
