package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The credential values below are invented. A real key must never appear in a
// test fixture — that would put it in the repository permanently.

// Redaction lives in logf so that it applies to callers that do not know they
// are handling a secret. This test drives the logger the way those callers do:
// a bare "%v" on an error value.
func TestLogfRedactsCredentialsOnEverySink(t *testing.T) {
	var stdout bytes.Buffer
	path := filepath.Join(t.TempDir(), "yata.log")
	lg, err := New(path, Trace, 100, &stdout, 1<<20, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer lg.Close()

	lg.Warnf("test: Anthelion (abc123) — api fetch detail: %v",
		errBody(`Get "https://tracker.example/api/user?api_token=abc123secret": i/o timeout`))

	if got := stdout.String(); strings.Contains(got, "abc123secret") {
		t.Errorf("secret reached stdout: %q", got)
	}
	if entries := lg.Recent(0, Trace); len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	} else if strings.Contains(entries[0].Msg, "abc123secret") {
		t.Errorf("secret reached the Logs tab ring buffer: %q", entries[0].Msg)
	}
	lg.Close()
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(onDisk), "abc123secret") {
		t.Errorf("secret reached the log file: %q", onDisk)
	}
	// The line still has to be useful for diagnosis.
	if !strings.Contains(string(onDisk), "tracker.example/api/user") {
		t.Errorf("redaction destroyed the diagnostic value: %q", onDisk)
	}
}

type errBody string

func (e errBody) Error() string { return string(e) }

func TestLogFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes do not apply on Windows")
	}
	path := filepath.Join(t.TempDir(), "yata.log")
	lg, err := New(path, Info, 10, nil, 1<<20, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer lg.Close()
	lg.Infof("hello")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("log mode = %04o, want 0600", mode)
	}
}

// A log written before this change is the one with history in it, so opening
// an existing world-readable log must tighten it rather than leave it.
func TestExistingLogIsHardenedOnOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes do not apply on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "yata.log")
	if err := os.WriteFile(path, []byte("old line\n"), 0o644); err != nil {
		t.Fatalf("seed log: %v", err)
	}
	if err := os.WriteFile(path+".1", []byte("older line\n"), 0o644); err != nil {
		t.Fatalf("seed backup: %v", err)
	}

	lg, err := New(path, Info, 10, nil, 1<<20, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer lg.Close()

	for _, p := range []string{path, path + ".1"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %04o, want 0600", filepath.Base(p), mode)
		}
	}
}
