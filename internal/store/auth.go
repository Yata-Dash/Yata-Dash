package store

import (
	"database/sql"
	"time"
)

// User is the single application account (basic auth). There is at most one row.
type User struct {
	Username     string
	PasswordHash string
	CreatedAt    int64
	// TOTPSecret is set as soon as enrolment starts, but is only in force once
	// TOTPEnabled is true — see the schema comment in store.go.
	TOTPSecret   string
	TOTPEnabled  bool
	TOTPLastStep int64
	// WeakPassword marks an account whose password predates the current length
	// floor. Set on login (the only moment the plaintext is available), cleared
	// whenever the password is set anew.
	WeakPassword bool
}

// GetUser returns the configured account, or ok=false when none exists.
func (d *DB) GetUser() (User, bool, error) {
	var u User
	var totpEnabled, weak int
	err := d.sql.QueryRow(
		`SELECT username, password_hash, created_at, totp_secret, totp_enabled, totp_last_step, weak_password
		   FROM auth WHERE id = 1`).
		Scan(&u.Username, &u.PasswordHash, &u.CreatedAt, &u.TOTPSecret, &totpEnabled, &u.TOTPLastStep, &weak)
	if err == sql.ErrNoRows {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	u.TOTPEnabled, u.WeakPassword = totpEnabled == 1, weak == 1
	return u, true, nil
}

// SetUser creates or replaces the single account. Setting a password always
// clears the weak-password flag: every write goes through the current policy.
func (d *DB) SetUser(username, passwordHash string) error {
	_, err := d.sql.Exec(
		`INSERT INTO auth (id, username, password_hash, created_at) VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET username = excluded.username,
		                               password_hash = excluded.password_hash,
		                               weak_password = 0`,
		username, passwordHash, time.Now().Unix())
	return err
}

// SetWeakPassword records whether the account's current password meets the
// length floor. Called on successful login, where the plaintext is in hand.
func (d *DB) SetWeakPassword(weak bool) error {
	v := 0
	if weak {
		v = 1
	}
	_, err := d.sql.Exec(`UPDATE auth SET weak_password = ? WHERE id = 1`, v)
	return err
}

// SetTOTPSecret stores a (possibly pending) secret and its enabled state,
// resetting the replay counter — a new secret has consumed no steps.
func (d *DB) SetTOTPSecret(secret string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := d.sql.Exec(
		`UPDATE auth SET totp_secret = ?, totp_enabled = ?, totp_last_step = 0 WHERE id = 1`,
		secret, v)
	return err
}

// SetTOTPLastStep records the most recently consumed time step, so a code
// observed in transit can't be replayed for the rest of its 30-second window.
func (d *DB) SetTOTPLastStep(step int64) error {
	_, err := d.sql.Exec(`UPDATE auth SET totp_last_step = ? WHERE id = 1`, step)
	return err
}

// DisableTOTP clears 2FA entirely, including any unused recovery codes.
func (d *DB) DisableTOTP() error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE auth SET totp_secret = '', totp_enabled = 0, totp_last_step = 0 WHERE id = 1`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM recovery_codes`); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplaceRecoveryCodes swaps the whole set for a freshly generated one. Codes
// are stored hashed; the caller shows the plaintext once and then forgets it.
func (d *DB) ReplaceRecoveryCodes(hashes []string, now time.Time) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM recovery_codes`); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.Exec(
			`INSERT INTO recovery_codes (hash, created_at) VALUES (?, ?)`, h, now.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UseRecoveryCode consumes an unused code by hash, reporting whether it matched.
// The UPDATE's own affected-row count is the check, so two concurrent logins
// can't both spend the same code.
func (d *DB) UseRecoveryCode(hash string, now time.Time) (bool, error) {
	res, err := d.sql.Exec(
		`UPDATE recovery_codes SET used_at = ? WHERE hash = ? AND used_at = 0`, now.Unix(), hash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RecoveryCodesLeft counts codes still available to spend.
func (d *DB) RecoveryCodesLeft() (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE used_at = 0`).Scan(&n)
	return n, err
}

// DeleteUser removes the account and invalidates every session.
func (d *DB) DeleteUser() error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM auth WHERE id = 1`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM recovery_codes`); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateSession stores a session token valid until expiresAt.
func (d *DB) CreateSession(token string, expiresAt time.Time) error {
	_, err := d.sql.Exec(
		`INSERT INTO sessions (token, created_at, expires_at) VALUES (?, ?, ?)`,
		token, time.Now().Unix(), expiresAt.Unix())
	return err
}

// SessionValid reports whether the token exists and has not expired.
func (d *DB) SessionValid(token string, now time.Time) (bool, error) {
	if token == "" {
		return false, nil
	}
	var expiresAt int64
	err := d.sql.QueryRow(`SELECT expires_at FROM sessions WHERE token = ?`, token).Scan(&expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return expiresAt > now.Unix(), nil
}

// DeleteSession removes a single session (logout).
func (d *DB) DeleteSession(token string) error {
	_, err := d.sql.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// ClearSessions removes every session (e.g. after a password change).
func (d *DB) ClearSessions() error {
	_, err := d.sql.Exec(`DELETE FROM sessions`)
	return err
}

// PruneSessions deletes expired sessions (housekeeping).
func (d *DB) PruneSessions(now time.Time) error {
	_, err := d.sql.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now.Unix())
	return err
}
