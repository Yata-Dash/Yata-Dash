package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The database holds live bearer session tokens, the TOTP shared secret and
// the password hash. modernc/sqlite creates it 0644.

func TestDatabaseAndSidecarsArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes do not apply on Windows")
	}
	path := filepath.Join(t.TempDir(), "yata.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Force a write so the WAL sidecars definitely exist.
	if err := db.RecordScrape("t1", time.Now(), true, ""); err != nil {
		t.Fatalf("RecordScrape: %v", err)
	}
	if err := Harden(path); err != nil {
		t.Fatalf("Harden: %v", err)
	}

	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := path + suffix
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue // sidecar not materialised on this platform/journal mode
		}
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %04o, want 0600", filepath.Base(p), mode)
		}
	}
}

// The upgrade path: a database created by an earlier version is world-readable
// and startup must tighten it.
func TestHardenFixesExistingDatabase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes do not apply on Windows")
	}
	path := filepath.Join(t.TempDir(), "yata.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.Close()

	if err := os.Chmod(path, 0o644); err != nil { // simulate the old default
		t.Fatal(err)
	}
	if err := Harden(path); err != nil {
		t.Fatalf("Harden: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %04o, want 0600", mode)
	}
}

// Sidecars come and go with the connection, so hardening a path whose -wal is
// absent must not report an error.
func TestHardenIsFineWithMissingSidecars(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never-opened.db")
	if err := Harden(path); err != nil {
		t.Errorf("Harden on a nonexistent database = %v, want nil", err)
	}
}
