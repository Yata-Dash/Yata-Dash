package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A backup is a byte-for-byte copy of config.json, so it carries every tracker
// API key and session cookie. config.json itself is already 0600 (saveLocked
// writes through os.CreateTemp), which is precisely why a loose backup matters:
// it routes around the protection the main file already has.

func newManagerForPerm(t *testing.T) *Manager {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return m
}

func TestBackupIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes do not apply on Windows")
	}
	m := newManagerForPerm(t)
	dst, err := m.Backup()
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("backup mode = %04o, want 0600", mode)
	}
	dirInfo, err := os.Stat(m.BackupDir())
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("backup dir mode = %04o, want 0700", mode)
	}
}

// The upgrade path: backups written by an earlier version are the ones with a
// history of credentials in them, so startup has to tighten them too.
func TestHardenBackupsFixesExisting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes do not apply on Windows")
	}
	m := newManagerForPerm(t)
	if err := os.MkdirAll(m.BackupDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(m.BackupDir(), "config-backup-20250101-000000.json")
	if err := os.WriteFile(old, []byte(`{"settings":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.HardenBackups(); err != nil {
		t.Fatalf("HardenBackups: %v", err)
	}

	info, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("pre-existing backup mode = %04o, want 0600", mode)
	}
	dirInfo, err := os.Stat(m.BackupDir())
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("backup dir mode = %04o, want 0700", mode)
	}
}

func TestHardenBackupsWithNoBackupDir(t *testing.T) {
	m := newManagerForPerm(t)
	if err := m.HardenBackups(); err != nil {
		t.Errorf("HardenBackups with no backups = %v, want nil", err)
	}
}
