// genversions regenerates versions.json (repo root) — the manifest the
// in-app update check compares against. Run it whenever defs, pathways data,
// or the app version change, BEFORE committing:
//
//	go run ./tools/genversions
//
// Values are computed exactly the way the app computes its LOCAL versions
// (internal/api/updates.go localVersions), so the comparison is like-for-like:
//
//	app      — internal/version.Version
//	defs     — max last_updated across defs/trackers/*.json
//	pathways — defs/pathways/routes.json source.updated (the real upstream data
//	           date; falls back to source.fetched for older snapshots)
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/Yata-Dash/Yata-Dash/internal/version"
)

func main() {
	out := struct {
		App      string `json:"app"`
		Defs     string `json:"defs"`
		Pathways string `json:"pathways"`
	}{App: version.Version}

	files, err := filepath.Glob(filepath.Join("defs", "trackers", "*.json"))
	if err != nil || len(files) == 0 {
		log.Fatal("no defs found — run from the repo root")
	}
	typeFiles, _ := filepath.Glob(filepath.Join("defs", "types", "*.json"))
	files = append(files, typeFiles...)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			log.Fatalf("%s: %v", f, err)
		}
		var td struct {
			LastUpdated string `json:"last_updated"`
		}
		if err := json.Unmarshal(raw, &td); err != nil {
			log.Fatalf("%s: %v", f, err)
		}
		if td.LastUpdated > out.Defs {
			out.Defs = td.LastUpdated
		}
	}

	raw, err := os.ReadFile(filepath.Join("defs", "pathways", "routes.json"))
	if err != nil {
		log.Fatalf("routes.json: %v", err)
	}
	var routes struct {
		Source struct {
			Fetched string `json:"fetched"`
			Updated string `json:"updated"`
		} `json:"source"`
	}
	if err := json.Unmarshal(raw, &routes); err != nil {
		log.Fatalf("routes.json: %v", err)
	}
	// The pathways version is the upstream DATA date, not our fetch date.
	out.Pathways = routes.Source.Updated
	if out.Pathways == "" {
		out.Pathways = routes.Source.Fetched // pre-`updated` snapshots
	}

	enc, _ := json.MarshalIndent(out, "", "  ")
	if err := os.WriteFile("versions.json", append(enc, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("versions.json: app=%s defs=%s pathways=%s\n", out.App, out.Defs, out.Pathways)
}
