package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// reopenInterval rate-limits attempts to reopen the active log after an open
// has failed. A var so tests can shorten it.
var reopenInterval = 5 * time.Second

// rollingFile is a minimal size-based rotating file writer (no external deps).
// When the active file would exceed maxBytes it is rotated to "<path>.1",
// shifting older backups up to maxBackups; the oldest is discarded.
type rollingFile struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	f          *os.File
	size       int64
	// lastOpen is when open() was last attempted, for reopenInterval.
	lastOpen time.Time
	// closed marks a deliberate close(), after which Write must not reopen.
	closed bool
	// dropped counts lines lost while the file could not be opened; the
	// count is written into the log once it reopens, so the gap is visible
	// in a downloaded log rather than silent.
	dropped int
}

func newRollingFile(path string, maxBytes int64, maxBackups int) (*rollingFile, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	r := &rollingFile{path: path, maxBytes: maxBytes, maxBackups: maxBackups}
	if err := r.open(); err != nil {
		return nil, err
	}
	// Existing logs from before this became private stay world-readable
	// otherwise, and on a shared seedbox those are the ones with history in
	// them. Rotated backups are tightened too — they hold the same content.
	r.hardenExisting()
	return r, nil
}

// hardenExisting tightens the active log and its rotated backups to 0600.
// Failures are ignored: a log that cannot be chmod'd is not a reason to refuse
// to start, and on Windows the call is a no-op the OS reports as success.
func (r *rollingFile) hardenExisting() {
	_ = os.Chmod(r.path, 0o600)
	for i := 1; i <= r.maxBackups; i++ {
		_ = os.Chmod(fmt.Sprintf("%s.%d", r.path, i), 0o600)
	}
}

func (r *rollingFile) open() error {
	r.lastOpen = time.Now()
	// 0600, not 0644: the log records tracker names, usernames and error
	// details, and on a shared host (seedboxes especially) every other local
	// account could read it.
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f = f
	r.size = info.Size()
	return nil
}

func (r *rollingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		// A failed open (disk full, permissions, a log directory briefly
		// unmounted — usually right after a rotation) used to leave the file
		// sink dead until restart, silently. Retry, but at most once per
		// reopenInterval: logging must stay cheap while the problem lasts.
		if r.closed {
			return len(p), nil
		}
		if time.Since(r.lastOpen) < reopenInterval || r.open() != nil {
			r.dropped++
			return len(p), nil
		}
		// The file came back — possibly a remounted directory holding an
		// older, looser-permissioned log — so tighten it like at start-up
		// before anything is appended, then mark the gap.
		r.hardenExisting()
		r.noteDropped()
	}
	if r.size+int64(len(p)) > r.maxBytes {
		if err := r.rotate(); err != nil && r.f == nil {
			// The file sink is down until a later Write reopens it. Say so
			// once on stderr — the one sink that does not depend on it —
			// so an operator watching the console can see the gap begin.
			fmt.Fprintf(os.Stderr, "yata: cannot reopen log file %s after rotation: %v; retrying every %s\n",
				r.path, err, reopenInterval)
			r.dropped++
			return len(p), nil
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// noteDropped records, in the freshly reopened file, how many lines were lost
// while it was unavailable. Written straight to the file (not through Write)
// so it cannot itself trigger a rotation or recurse.
func (r *rollingFile) noteDropped() {
	if r.dropped == 0 || r.f == nil {
		return
	}
	line := fmt.Sprintf("%s WRN logging: %d log line(s) were dropped while %s could not be opened\n",
		time.Now().Format("2006-01-02 15:04:05"), r.dropped, r.path)
	if n, err := r.f.WriteString(line); err == nil {
		r.size += int64(n)
		r.dropped = 0
	}
}

// rotate closes the active file, shifts backups (.1→.2, …), moves the current
// file to ".1", and opens a fresh active file.
func (r *rollingFile) rotate() error {
	if r.f != nil {
		r.f.Close()
		r.f = nil
	}
	// Drop the oldest, then shift each backup up by one.
	oldest := fmt.Sprintf("%s.%d", r.path, r.maxBackups)
	_ = os.Remove(oldest)
	for i := r.maxBackups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	if r.maxBackups >= 1 {
		_ = os.Rename(r.path, r.path+".1")
	}
	return r.open()
}

// truncate empties the active log file (and removes backups).
func (r *rollingFile) truncate() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil {
		r.f.Close()
		r.f = nil
	}
	for i := 1; i <= r.maxBackups; i++ {
		_ = os.Remove(fmt.Sprintf("%s.%d", r.path, i))
	}
	if err := os.Truncate(r.path, 0); err != nil && !os.IsNotExist(err) {
		// fall through to re-open; a fresh file is created if missing
	}
	return r.open()
}

func (r *rollingFile) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.f != nil {
		r.f.Close()
		r.f = nil
	}
}
