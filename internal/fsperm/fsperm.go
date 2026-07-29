// Package fsperm keeps Yata's on-disk state private to the account running it.
//
// Everything Yata persists is sensitive: config.json and its backups hold
// tracker API keys and session cookies, the database holds live bearer session
// tokens and the TOTP secret, and the log holds usernames and error detail.
// The default modes the standard library and the SQLite driver produce (0644
// files, 0755 directories) are fine on a single-user desktop and wrong on the
// shared hosts a lot of this app's users are on — a seedbox has other people's
// accounts on it, and any of them can read a 0644 file.
//
// Hardening is best effort by design. A chmod that fails is not a reason to
// refuse to start: the likeliest causes are a filesystem without Unix modes
// (Windows, a bind-mounted share, some Docker volume drivers) where the call
// is either meaningless or already handled by the mount options. Refusing to
// boot there would turn a hardening measure into an outage. Callers that want
// to report the outcome can use the returned error; callers that just want the
// tightening can ignore it.
package fsperm

import (
	"fmt"
	"os"
)

// FileMode is the mode for a private file: owner read/write only.
const FileMode os.FileMode = 0o600

// DirMode is the mode for a private directory: owner access only. Directories
// need the execute bit to be traversable, so 0700 rather than 0600.
const DirMode os.FileMode = 0o700

// File tightens one existing file to 0600. A missing file is not an error —
// callers harden paths that may not have been created yet (a SQLite -wal
// sidecar only exists once something has been written).
func File(path string) error {
	if err := os.Chmod(path, FileMode); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("harden %s: %w", path, err)
	}
	return nil
}

// Files tightens several paths, returning the first error while still
// attempting every one — a failure on a sidecar should not leave the main file
// world-readable.
func Files(paths ...string) error {
	var first error
	for _, p := range paths {
		if err := File(p); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Dir creates path if needed and tightens it to 0700. Unlike MkdirAll's mode
// argument, which is masked by umask and only applies at creation, this also
// tightens a directory that already exists from an earlier version.
func Dir(path string) error {
	if err := os.MkdirAll(path, DirMode); err != nil {
		return err
	}
	if err := os.Chmod(path, DirMode); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("harden %s: %w", path, err)
	}
	return nil
}
