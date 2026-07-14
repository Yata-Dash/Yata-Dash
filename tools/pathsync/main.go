// pathsync converts the community tracker-pathways dataset
// (github.com/handokota/trackerpathways, MIT) into Yata's
// defs/pathways/routes.json. Run it to refresh the bundled snapshot:
//
//	go run ./tools/pathsync                  # fetch from GitHub
//	go run ./tools/pathsync -local file.json # convert a local copy
//
// The output is pure data — the app maps tracker names to its own defs at
// load time, so this file needs no hand edits.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const upstreamURL = "https://raw.githubusercontent.com/handokota/trackerpathways/main/src/data/trackers.json"

// ── Upstream shapes ──────────────────────────────────────────────────────────

type upstreamRoute struct {
	Days    *float64 `json:"days"`
	Reqs    string   `json:"reqs"`
	Active  string   `json:"active"`
	Updated string   `json:"updated"`
}

type upstream struct {
	RouteInfo         map[string]map[string]upstreamRoute `json:"routeInfo"`
	UnlockInviteClass map[string][]any                    `json:"unlockInviteClass"`
	AbbrList          map[string]string                   `json:"abbrList"`
}

// ── Yata shapes (must match internal/pathways/types.go) ──────────────────

type route struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Days    int    `json:"days"` // min account age on source (-1 = unknown)
	Reqs    string `json:"reqs"` // free-text requirements from the community data
	Active  bool   `json:"active"`
	Updated string `json:"updated,omitempty"`
}

type unlockClass struct {
	Days int    `json:"days"` // days until invites unlock (-1 = unknown)
	Text string `json:"text"` // "Class: req, req; Class2: ..." free text
}

type output struct {
	SchemaVersion int `json:"schema_version"`
	Source        struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		License string `json:"license"`
		Fetched string `json:"fetched"` // when we pulled the snapshot (metadata)
		Updated string `json:"updated"` // upstream DATA freshness (YYYY-MM) = the real version
	} `json:"source"`
	Abbr    map[string]string      `json:"abbr,omitempty"`
	Routes  []route                `json:"routes"`
	Unlocks map[string]unlockClass `json:"unlocks"`
}

func main() {
	local := flag.String("local", "", "convert a local trackers.json instead of fetching")
	out := flag.String("out", filepath.Join("defs", "pathways", "routes.json"), "output path")
	check := flag.Bool("check", false, "report whether upstream is newer than the current snapshot, then exit without writing")
	flag.Parse()

	var raw []byte
	var err error
	if *local != "" {
		raw, err = os.ReadFile(*local)
	} else {
		raw, err = fetch(upstreamURL)
	}
	if err != nil {
		log.Fatal(err)
	}

	var up upstream
	if err := json.Unmarshal(raw, &up); err != nil {
		log.Fatalf("parse upstream: %v", err)
	}

	var o output
	o.SchemaVersion = 1
	o.Source.Name = "trackerpathways"
	o.Source.URL = "https://github.com/handokota/trackerpathways"
	o.Source.License = "MIT"
	o.Source.Fetched = time.Now().UTC().Format("2006-01-02")
	o.Abbr = up.AbbrList
	o.Unlocks = map[string]unlockClass{}

	for src, targets := range up.RouteInfo {
		for dst, r := range targets {
			days := -1
			if r.Days != nil {
				days = int(*r.Days)
			}
			o.Routes = append(o.Routes, route{
				From:    src,
				To:      dst,
				Days:    days,
				Reqs:    r.Reqs,
				Active:  r.Active == "Yes",
				Updated: r.Updated,
			})
		}
	}
	sort.Slice(o.Routes, func(i, j int) bool {
		if o.Routes[i].From != o.Routes[j].From {
			return o.Routes[i].From < o.Routes[j].From
		}
		return o.Routes[i].To < o.Routes[j].To
	})

	for name, uc := range up.UnlockInviteClass {
		entry := unlockClass{Days: -1}
		if len(uc) > 0 {
			if f, ok := uc[0].(float64); ok {
				entry.Days = int(f)
			}
		}
		if len(uc) > 1 {
			if s, ok := uc[1].(string); ok {
				entry.Text = s
			}
		}
		o.Unlocks[name] = entry
	}

	// The real "version" of the dataset: the newest per-route date, NOT today.
	// Upstream stamps each route "Mon YYYY" (e.g. "Jun 2026"); the most recent
	// is the dataset's freshness.
	o.Source.Updated = deriveUpdated(o.Routes)

	// Compare against the snapshot we already have so it's clear whether this
	// run actually pulled anything newer.
	prevUpdated, prevRoutes := readSnapshot(*out)
	fmt.Printf("upstream data date: %s (%d routes)\n", dispDate(o.Source.Updated), len(o.Routes))
	switch {
	case prevUpdated == "":
		fmt.Println("  no existing snapshot — this will create one.")
	case o.Source.Updated > prevUpdated:
		fmt.Printf("  NEWER than current snapshot (%s, %d routes) — worth updating.\n", dispDate(prevUpdated), prevRoutes)
	case o.Source.Updated == prevUpdated && len(o.Routes) == prevRoutes:
		fmt.Printf("  up to date — matches current snapshot (%s).\n", dispDate(prevUpdated))
	case o.Source.Updated == prevUpdated:
		fmt.Printf("  same data date (%s) but route count changed (%d → %d).\n", dispDate(prevUpdated), prevRoutes, len(o.Routes))
	default:
		fmt.Printf("  current snapshot (%s) is newer than upstream (%s)?? leaving as-is is safe.\n", dispDate(prevUpdated), dispDate(o.Source.Updated))
	}

	if *check {
		fmt.Println("(-check: nothing written)")
		return
	}

	data, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s: %d routes, %d unlock entries — data %s, fetched %s\n",
		*out, len(o.Routes), len(o.Unlocks), dispDate(o.Source.Updated), o.Source.Fetched)
	fmt.Println("note: run genversions next so versions.json reflects the new pathways date.")
}

// deriveUpdated returns the newest route date as YYYY-MM — the true freshness of
// the upstream dataset. Upstream stamps each route "Mon YYYY" (e.g. "Jun 2026");
// unparseable/blank entries are ignored.
func deriveUpdated(routes []route) string {
	var newest time.Time
	for _, r := range routes {
		s := strings.TrimSpace(r.Updated)
		if s == "" {
			continue
		}
		if t, err := time.Parse("Jan 2006", s); err == nil && t.After(newest) {
			newest = t
		}
	}
	if newest.IsZero() {
		return ""
	}
	return newest.Format("2006-01")
}

// readSnapshot returns the current routes.json's source.updated and route count
// (both zero-valued if it doesn't exist yet), for the newer-than comparison.
func readSnapshot(path string) (updated string, routes int) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", 0
	}
	var s struct {
		Source struct {
			Updated string `json:"updated"`
			Fetched string `json:"fetched"`
		} `json:"source"`
		Routes []json.RawMessage `json:"routes"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return "", 0
	}
	u := s.Source.Updated
	if u == "" {
		u = s.Source.Fetched // pre-`updated` snapshot: best available
	}
	return u, len(s.Routes)
}

// dispDate renders a YYYY-MM version as "Jun 2026" for humans; passes anything
// else (or "") through unchanged.
func dispDate(ym string) string {
	if ym == "" {
		return "(unknown)"
	}
	if t, err := time.Parse("2006-01", ym); err == nil {
		return t.Format("Jan 2006")
	}
	return ym
}

func fetch(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d fetching %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}
