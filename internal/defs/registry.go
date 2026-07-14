package defs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// LoadIssue reports a definition file that failed to load. Bad user-supplied
// defs must never crash the app — they are skipped and surfaced via /api/defs.
type LoadIssue struct {
	File  string `json:"file"`
	Error string `json:"error"`
}

// OptOutEntry marks a tracker that has asked NOT to be supported by this
// app. Loaded from defs/optout.json — updated alongside def updates so
// users can see clearly that a tracker opted out, and adding it is blocked.
type OptOutEntry struct {
	Name string `json:"name"`
	// Host is the bare hostname matched against tracker URLs, e.g.
	// "sometracker.org" (also matches www. and any scheme).
	Host string `json:"host"`
	Date string `json:"date,omitempty"` // when they opted out
	Note string `json:"note,omitempty"` // optional public note
}

// Registry holds all loaded definitions and supports atomic reload.
type Registry struct {
	mu       sync.RWMutex
	dir      string
	types    map[string]TypeDef
	trackers map[string]TrackerDef
	order    []string // tracker keys in sorted order
	issues   []LoadIssue
	optout   []OptOutEntry
}

// Load creates a registry from the defs directory (expects types/ and
// trackers/ subdirectories).
func Load(dir string) (*Registry, error) {
	r := &Registry{dir: dir}
	if err := r.Reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// Reload re-reads every definition file. The registry is swapped atomically:
// a failed reload leaves the previous state untouched.
func (r *Registry) Reload() error {
	types := map[string]TypeDef{}
	trackers := map[string]TrackerDef{}
	var issues []LoadIssue

	typeFiles, err := filepath.Glob(filepath.Join(r.dir, "types", "*.json"))
	if err != nil {
		return err
	}
	for _, f := range typeFiles {
		var td TypeDef
		if err := readJSON(f, &td); err != nil {
			issues = append(issues, LoadIssue{File: filepath.Base(f), Error: err.Error()})
			continue
		}
		if err := validateType(td); err != nil {
			issues = append(issues, LoadIssue{File: filepath.Base(f), Error: err.Error()})
			continue
		}
		if _, dup := types[td.Key]; dup {
			issues = append(issues, LoadIssue{File: filepath.Base(f), Error: fmt.Sprintf("duplicate type key %q", td.Key)})
			continue
		}
		types[td.Key] = td
	}

	trackerFiles, err := filepath.Glob(filepath.Join(r.dir, "trackers", "*.json"))
	if err != nil {
		return err
	}
	for _, f := range trackerFiles {
		var td TrackerDef
		if err := readJSON(f, &td); err != nil {
			issues = append(issues, LoadIssue{File: filepath.Base(f), Error: err.Error()})
			continue
		}
		if err := validateTracker(td, types); err != nil {
			issues = append(issues, LoadIssue{File: filepath.Base(f), Error: err.Error()})
			continue
		}
		if _, dup := trackers[td.Key]; dup {
			issues = append(issues, LoadIssue{File: filepath.Base(f), Error: fmt.Sprintf("duplicate tracker key %q", td.Key)})
			continue
		}
		trackers[td.Key] = td
	}

	// Opt-out list (optional file). Trackers on it cannot be added.
	var optout []OptOutEntry
	if optPath := filepath.Join(r.dir, "optout.json"); fileExists(optPath) {
		if err := readJSON(optPath, &optout); err != nil {
			issues = append(issues, LoadIssue{File: "optout.json", Error: err.Error()})
		}
	}

	order := make([]string, 0, len(trackers))
	for k := range trackers {
		order = append(order, k)
	}
	sort.Strings(order)

	r.mu.Lock()
	r.types = types
	r.trackers = trackers
	r.order = order
	r.issues = issues
	r.optout = optout
	r.mu.Unlock()
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// OptOut returns the opt-out entry matching a tracker URL, if any.
func (r *Registry) OptOut(rawURL string) (OptOutEntry, bool) {
	host := strings.TrimPrefix(strings.TrimPrefix(normURL(rawURL), "https://"), "http://")
	host = strings.TrimPrefix(host, "www.")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.optout {
		h := strings.ToLower(strings.TrimPrefix(e.Host, "www."))
		if h != "" && (host == h || strings.HasSuffix(host, "."+h)) {
			return e, true
		}
	}
	return OptOutEntry{}, false
}

// OptOuts returns the full opt-out list.
func (r *Registry) OptOuts() []OptOutEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]OptOutEntry(nil), r.optout...)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		// Retry tolerantly: unknown fields are a warning-grade problem and
		// shouldn't reject a def written for a newer/older app version.
		if err2 := json.Unmarshal(data, v); err2 != nil {
			return err2
		}
	}
	return nil
}

func validateType(td TypeDef) error {
	if td.Key == "" {
		return fmt.Errorf("missing required field: key")
	}
	if td.Label == "" {
		return fmt.Errorf("missing required field: label")
	}
	switch td.API.Kind {
	case "unit3d", "gazelle", "custom", "demo", "none":
	default:
		return fmt.Errorf("api.kind must be one of unit3d|gazelle|custom|demo|none, got %q", td.API.Kind)
	}
	return nil
}

func validateTracker(td TrackerDef, types map[string]TypeDef) error {
	switch {
	case td.Key == "":
		return fmt.Errorf("missing required field: key")
	case td.Name == "":
		return fmt.Errorf("missing required field: name")
	case td.URL == "":
		return fmt.Errorf("missing required field: url")
	case td.Type == "":
		return fmt.Errorf("missing required field: type")
	}
	if _, ok := types[td.Type]; !ok {
		return fmt.Errorf("unknown tracker type %q (no defs/types/%s.json)", td.Type, td.Type)
	}
	if types[td.Type].API.Kind == "custom" && td.API == nil {
		return fmt.Errorf("type %q requires an \"api\" object in the tracker def", td.Type)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Lookups
// ─────────────────────────────────────────────────────────────────────────────

// Tracker returns the def for a tracker key.
func (r *Registry) Tracker(key string) (TrackerDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	td, ok := r.trackers[key]
	return td, ok
}

// Type returns the def for a type key.
func (r *Registry) Type(key string) (TypeDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	td, ok := r.types[key]
	return td, ok
}

// ExtendedStats returns the supplementary stats-endpoint spec for a tracker, or
// nil if its def doesn't declare one. Per-tracker (not on the type def) so an
// endpoint that only some UNIT3D trackers expose is never blindly requested.
func (r *Registry) ExtendedStats(trackerURL, typeKey string) *ExtendedStatsSpec {
	if td, ok := r.TrackerByURL(trackerURL); ok {
		return td.ExtendedStats
	}
	return nil
}

// TrackerByURL matches a tracker def by base URL (or alias), comparing
// case-insensitively with trailing slashes stripped.
func (r *Registry) TrackerByURL(rawURL string) (TrackerDef, bool) {
	needle := normURL(rawURL)
	if needle == "" {
		return TrackerDef{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, td := range r.trackers {
		if normURL(td.URL) == needle {
			return td, true
		}
		for _, a := range td.Aliases {
			if normURL(a) == needle {
				return td, true
			}
		}
	}
	return TrackerDef{}, false
}

func normURL(u string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(u), "/"))
}

// Trackers returns all tracker defs sorted by key.
func (r *Registry) Trackers() []TrackerDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]TrackerDef, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, r.trackers[k])
	}
	return out
}

// Types returns all type defs.
func (r *Registry) Types() []TypeDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]TypeDef, 0, len(r.types))
	for _, t := range r.types {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Issues returns load problems from the last (re)load.
func (r *Registry) Issues() []LoadIssue {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]LoadIssue(nil), r.issues...)
}
