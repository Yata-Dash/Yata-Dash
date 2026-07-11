package store

import (
	"database/sql"
	"time"
)

// APIToken is one Yata-issued integration token. Only the SHA-256 hash of the
// token is stored — the plaintext is shown once at creation and never again.
// Tokens are read-only by construction: the router only accepts them on the
// read-only integration endpoints (see internal/api/tokens.go).
type APIToken struct {
	ID         string // random id, used for revocation
	Name       string // user label ("Homepage widget")
	Prefix     string // display hint: the first characters of the token
	Hash       string // sha256 hex of the full token
	CreatedAt  int64  // unix seconds
	LastUsedAt int64  // unix seconds; 0 = never used
}

// CreateAPIToken stores a new token record.
func (d *DB) CreateAPIToken(t APIToken) error {
	_, err := d.sql.Exec(
		`INSERT INTO api_tokens (id, name, prefix, hash, created_at, last_used_at)
		 VALUES (?, ?, ?, ?, ?, 0)`,
		t.ID, t.Name, t.Prefix, t.Hash, t.CreatedAt)
	return err
}

// ListAPITokens returns every token record, newest first.
func (d *DB) ListAPITokens() ([]APIToken, error) {
	rows, err := d.sql.Query(
		`SELECT id, name, prefix, hash, created_at, last_used_at
		 FROM api_tokens ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.Hash, &t.CreatedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// APITokenByHash resolves a presented token's hash to its record.
func (d *DB) APITokenByHash(hash string) (APIToken, bool, error) {
	var t APIToken
	err := d.sql.QueryRow(
		`SELECT id, name, prefix, hash, created_at, last_used_at
		 FROM api_tokens WHERE hash = ?`, hash).
		Scan(&t.ID, &t.Name, &t.Prefix, &t.Hash, &t.CreatedAt, &t.LastUsedAt)
	if err == sql.ErrNoRows {
		return APIToken{}, false, nil
	}
	if err != nil {
		return APIToken{}, false, err
	}
	return t, true, nil
}

// TouchAPIToken records that a token was just used (drives the "last used"
// column in Settings). Callers throttle this — no need to write on every hit.
func (d *DB) TouchAPIToken(id string, at time.Time) error {
	_, err := d.sql.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, at.Unix(), id)
	return err
}

// DeleteAPIToken revokes a token. Returns whether it existed.
func (d *DB) DeleteAPIToken(id string) (bool, error) {
	res, err := d.sql.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
