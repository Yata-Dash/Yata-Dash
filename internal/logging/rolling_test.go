package logging

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A rotation whose reopen fails (disk full, permissions) used to leave the
// file sink nil for the rest of the process's life: every later Write
// returned early and nothing reached disk again until a restart. Write now
// retries the open, rate-limited by reopenInterval.
func TestRollingFileRecoversAfterFailedReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yata.log")
	// No backups, so rotate() reopens the same path without renaming it.
	r, err := newRollingFile(path, 16, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()

	// Make the reopen fail: a directory where the log file should be.
	r.f.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	old := reopenInterval
	t.Cleanup(func() { reopenInterval = old })
	reopenInterval = time.Hour

	// Over maxBytes → rotate → open fails → the sink is down.
	if _, err := r.Write([]byte("this line forces a rotation\n")); err != nil {
		t.Fatalf("write during failure: %v", err)
	}
	if r.f != nil {
		t.Fatal("expected the reopen to fail")
	}

	// The cause clears, but within reopenInterval no retry is made.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, _ = r.Write([]byte("dropped\n"))
	if r.f != nil {
		t.Fatal("reopen was retried inside reopenInterval")
	}

	// Once the interval has passed, the next Write reopens and lands.
	reopenInterval = 0
	if _, err := r.Write([]byte("back\n")); err != nil {
		t.Fatalf("write after recovery: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log file not recreated: %v", err)
	}
	if !strings.Contains(string(got), "back") {
		t.Fatalf("log file = %q, want the post-recovery line", got)
	}
	// Both lines lost while the sink was down are accounted for.
	if !strings.Contains(string(got), "2 log line(s) were dropped") {
		t.Fatalf("log file = %q, want a note of the 2 dropped lines", got)
	}
}

// A reopen can land on a file that already exists — a log directory
// remounted with an older, world-readable yata.log in it. It must be
// tightened to 0600 before anything is appended, as at start-up.
func TestRollingFileReopenHardensExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	path := filepath.Join(t.TempDir(), "yata.log")
	r, err := newRollingFile(path, 1<<20, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	old := reopenInterval
	t.Cleanup(func() { reopenInterval = old })
	reopenInterval = 0

	// The sink went down, and the file reappears world-readable.
	r.f.Close()
	r.f = nil
	if err := os.WriteFile(path, []byte("older content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]byte("after reopen\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("reopened log mode = %o, want 600", perm)
	}
}

// A deliberate close must stay closed: the retry is for failures only.
func TestRollingFileDoesNotReopenAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yata.log")
	r, err := newRollingFile(path, 1<<20, 1)
	if err != nil {
		t.Fatal(err)
	}
	old := reopenInterval
	t.Cleanup(func() { reopenInterval = old })
	reopenInterval = 0

	r.close()
	_, _ = r.Write([]byte("after close\n"))
	if r.f != nil {
		r.f.Close()
		t.Fatal("Write reopened a deliberately closed log")
	}
}
