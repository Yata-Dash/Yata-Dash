// Package logging provides Yata's rolling logger: a level-filtered logger
// that tees every line to stdout, a size-rotated log file (for sharing in
// GitHub issues), and an in-memory ring buffer (for the live Logs settings
// tab). It also implements io.Writer so the standard library `log` package can
// be redirected through it, capturing existing startup/diagnostic output.
package logging

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/redact"
)

// Level is a log severity. Lower levels are more verbose.
type Level int

const (
	Trace Level = iota
	Debug
	Info
	Warn
	Error
)

// ParseLevel maps a name to a Level, defaulting to Info for unknown input.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return Trace
	case "debug":
		return Debug
	case "warn", "warning":
		return Warn
	case "error":
		return Error
	default:
		return Info
	}
}

func (l Level) String() string {
	switch l {
	case Trace:
		return "trace"
	case Debug:
		return "debug"
	case Warn:
		return "warn"
	case Error:
		return "error"
	default:
		return "info"
	}
}

func (l Level) short() string {
	switch l {
	case Trace:
		return "TRC"
	case Debug:
		return "DBG"
	case Warn:
		return "WRN"
	case Error:
		return "ERR"
	default:
		return "INF"
	}
}

// Entry is one captured log line (JSON-friendly for the API).
type Entry struct {
	Time  int64  `json:"time"`  // unix milliseconds
	Level string `json:"level"` // "trace".."error"
	Msg   string `json:"msg"`
}

type record struct {
	t   time.Time
	lvl Level
	msg string
}

// Logger is the application logger. Safe for concurrent use.
type Logger struct {
	mu    sync.Mutex
	level Level
	out   io.Writer // stdout (or any console writer)
	rf    *rollingFile

	ring  []record
	cap   int
	start int // index of the oldest entry
	count int
}

// New creates a logger writing to `filePath` (rotated at maxBytes, keeping
// maxBackups), echoing to `stdout`, and retaining the last `ringCap` lines in
// memory. A zero/empty filePath disables the file sink.
func New(filePath string, level Level, ringCap int, stdout io.Writer, maxBytes int64, maxBackups int) (*Logger, error) {
	if ringCap <= 0 {
		ringCap = 1000
	}
	lg := &Logger{level: level, out: stdout, cap: ringCap, ring: make([]record, ringCap)}
	if filePath != "" {
		rf, err := newRollingFile(filePath, maxBytes, maxBackups)
		if err != nil {
			return nil, err
		}
		lg.rf = rf
	}
	return lg, nil
}

// GetLevel returns the active threshold.
func (l *Logger) GetLevel() Level {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.level
}

func (l *Logger) logf(lvl Level, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lvl < l.level {
		return
	}
	now := time.Now()
	// Redact centrally rather than at each caller. Credentials reach the log
	// through error values that render a full request URL, which no call site
	// can see when it writes "%v" — and this file is meant to be attached to
	// GitHub issues, so a miss is a published secret.
	msg := redact.String(fmt.Sprintf(format, args...))
	line := fmt.Sprintf("%s %s %s\n", now.Format("2006-01-02 15:04:05"), lvl.short(), msg)
	if l.out != nil {
		_, _ = io.WriteString(l.out, line)
	}
	if l.rf != nil {
		_, _ = l.rf.Write([]byte(line))
	}
	// Append to the ring buffer.
	idx := (l.start + l.count) % l.cap
	if l.count == l.cap {
		l.start = (l.start + 1) % l.cap // overwrite oldest
		idx = (l.start + l.count - 1) % l.cap
	} else {
		l.count++
	}
	l.ring[idx] = record{t: now, lvl: lvl, msg: msg}
}

func (l *Logger) Tracef(format string, args ...any) { l.logf(Trace, format, args...) }
func (l *Logger) Debugf(format string, args ...any) { l.logf(Debug, format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.logf(Info, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.logf(Warn, format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.logf(Error, format, args...) }

// Write implements io.Writer so the standard `log` package can be redirected
// here. Each written line is captured at Info level (timestamps are added by
// this logger, so callers should clear the std logger's own flags).
func (l *Logger) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg != "" {
		l.logf(Info, "%s", msg)
	}
	return len(p), nil
}

// Recent returns up to `limit` of the most recent entries with level >=
// minLevel, oldest first. limit <= 0 returns all retained entries.
func (l *Logger) Recent(limit int, minLevel Level) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, 0, l.count)
	for i := 0; i < l.count; i++ {
		r := l.ring[(l.start+i)%l.cap]
		if r.lvl < minLevel {
			continue
		}
		out = append(out, Entry{Time: r.t.UnixMilli(), Level: r.lvl.String(), Msg: r.msg})
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// Clear empties the in-memory buffer and truncates the log file.
func (l *Logger) Clear() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.start, l.count = 0, 0
	if l.rf != nil {
		return l.rf.truncate()
	}
	return nil
}

// FilePath returns the active log file path ("" when no file sink).
func (l *Logger) FilePath() string {
	if l.rf == nil {
		return ""
	}
	return l.rf.path
}

// Close flushes and closes the file sink.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rf != nil {
		l.rf.close()
	}
}
