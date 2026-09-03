// Package version is the single source of truth for the application build
// version. Bump Version on every change: format "Beta-YYYYMMDD", adding a
// trailing letter for multiple builds in one day (e.g. "Beta-20260618b").
// Keep it sortable/comparable for the future GitHub update check.
package version

import (
	"runtime/debug"
	"strings"
	"time"
)

// Version is the current build identifier.
const Version = "Beta-20260903"

// Commit and BuildDate identify the exact build, which Version alone cannot:
// the :dev container image moves with every merge, so "Beta-20260730" stops
// telling a tester which build they are actually running. They are stamped at
// link time — see Commit()/BuildDate() for where the values come from.
//
// These are vars, not consts, because -ldflags -X can only write to variables.
// Never read them directly; the accessors below add the fallbacks.
var (
	commit    string
	buildDate string
)

// Commit returns the short git SHA this binary was built from, or "" when it
// was built without stamping.
//
// Two sources, because neither covers every build. -ldflags -X is set by
// scripts/package.sh, build.ps1 and the Dockerfile, and is the only option
// inside Docker, where the build context has no .git directory. Everything
// else falls back to the VCS information the Go toolchain embeds by itself
// when compiling inside a git checkout, which means a plain `go build` during
// development still reports something useful without any flags.
func Commit() string {
	if commit != "" {
		return shortSHA(commit)
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return ""
	}
	// A build from a working tree with uncommitted changes is NOT that commit,
	// and saying so plainly stops a bug report citing a SHA whose code never
	// produced the binary.
	if dirty {
		return shortSHA(rev) + "-dirty"
	}
	return shortSHA(rev)
}

// BuildDate returns when this binary was built, RFC 3339 in UTC, or "" if it
// was not recorded.
func BuildDate() string {
	if buildDate != "" {
		return buildDate
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.time" {
			// vcs.time is the COMMIT time, not the build time — close enough to
			// be useful and honest, since a stamped build overrides it anyway.
			if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
				return t.UTC().Format(time.RFC3339)
			}
			return s.Value
		}
	}
	return ""
}

// shortSHA trims a full 40-character hash to the 7 characters people actually
// quote, leaving anything already short alone.
func shortSHA(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
