package fsperm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes do not apply on Windows")
	}
}

func TestFileTightensExisting(t *testing.T) {
	skipOnWindows(t)
	p := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := File(p); err != nil {
		t.Fatalf("File: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %04o, want 0600", mode)
	}
}

// Sidecars are hardened by path before they necessarily exist (a SQLite -wal
// appears only once something is written), so a missing file must be a no-op
// rather than an error the caller has to special-case.
func TestFileIgnoresMissing(t *testing.T) {
	if err := File(filepath.Join(t.TempDir(), "never-created")); err != nil {
		t.Errorf("File on a missing path = %v, want nil", err)
	}
}

func TestFilesHardensAllEvenWhenOneFails(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "good.db")
	if err := os.WriteFile(good, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A path under a file (not a directory) cannot be chmod'd.
	bad := filepath.Join(good, "impossible")

	_ = Files(bad, good) // bad first: `good` must still be hardened

	info, err := os.Stat(good)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("a failure on one path left another world-readable: %04o", mode)
	}
}

// MkdirAll's mode is masked by umask and does nothing at all when the
// directory already exists, which is exactly the upgrade case.
func TestDirTightensExisting(t *testing.T) {
	skipOnWindows(t)
	p := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Dir(p); err != nil {
		t.Fatalf("Dir: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("mode = %04o, want 0700", mode)
	}
}

func TestDirCreates(t *testing.T) {
	skipOnWindows(t)
	p := filepath.Join(t.TempDir(), "nested", "backups")
	if err := Dir(p); err != nil {
		t.Fatalf("Dir: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("not a directory")
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("mode = %04o, want 0700", mode)
	}
}
