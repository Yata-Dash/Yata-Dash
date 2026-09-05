package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// groupLadderRevisions is how many revisions of one tracker's ladder are kept.
// Generous because the rows are tiny and rare — a ladder changes a couple of
// times in a week and then not for a year — and because the value of a
// revision log is entirely in how far back it goes.
const groupLadderRevisions = 20

// GroupLadder is one stored revision of a tracker's group ladder, as the
// tracker's own API reported it.
type GroupLadder struct {
	// Payload is the API's response projected onto the ladder fields Yata
	// models (defs.CanonicalLadder), so it records the SITE's rules and nothing
	// about the account that fetched them.
	Payload []byte
	// FirstSeen is when this revision first appeared — i.e. when the tracker
	// last changed its requirements, as far as Yata can tell.
	FirstSeen time.Time
	// CheckedAt is when Yata last confirmed this revision is still current.
	CheckedAt time.Time
}

// SaveGroupLadder records a freshly fetched ladder.
//
// An unchanged ladder bumps checked_at on the existing revision; a changed one
// opens a new revision. The caller must pass a payload already through
// defs.CanonicalLadder — hashing a raw response would file a new revision every
// time the user crossed a threshold, recording their progress in the one table
// that is meant to be about everything except that.
func (d *DB) SaveGroupLadder(trackerID string, payload []byte, now time.Time) error {
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])

	var id int64
	var prev string
	err := d.sql.QueryRow(
		`SELECT id, hash FROM group_ladders WHERE tracker_id = ? ORDER BY id DESC LIMIT 1`,
		trackerID).Scan(&id, &prev)
	switch {
	case err == nil && prev == hash:
		_, err = d.sql.Exec(`UPDATE group_ladders SET checked_at = ? WHERE id = ?`, now.Unix(), id)
		return err
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}

	if _, err := d.sql.Exec(
		`INSERT INTO group_ladders (tracker_id, first_seen, checked_at, hash, payload)
		 VALUES (?, ?, ?, ?, ?)`,
		trackerID, now.Unix(), now.Unix(), hash, string(payload)); err != nil {
		return err
	}
	// Trim to the newest N. Deleting by id keeps this correct even if two
	// revisions land in the same second.
	_, err = d.sql.Exec(
		`DELETE FROM group_ladders WHERE tracker_id = ? AND id NOT IN (
			SELECT id FROM group_ladders WHERE tracker_id = ? ORDER BY id DESC LIMIT ?
		 )`, trackerID, trackerID, groupLadderRevisions)
	return err
}

// LatestGroupLadder returns the current revision of a tracker's ladder.
// ok is false when nothing has ever been stored, which callers must treat as
// "unknown" rather than "this tracker has no ranks".
func (d *DB) LatestGroupLadder(trackerID string) (GroupLadder, bool) {
	var (
		payload   string
		firstSeen int64
		checkedAt int64
	)
	err := d.sql.QueryRow(
		`SELECT payload, first_seen, checked_at FROM group_ladders
		 WHERE tracker_id = ? ORDER BY id DESC LIMIT 1`,
		trackerID).Scan(&payload, &firstSeen, &checkedAt)
	if err != nil {
		return GroupLadder{}, false
	}
	return GroupLadder{
		Payload:   []byte(payload),
		FirstSeen: time.Unix(firstSeen, 0).UTC(),
		CheckedAt: time.Unix(checkedAt, 0).UTC(),
	}, true
}
