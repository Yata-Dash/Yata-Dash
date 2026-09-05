// defsdoc regenerates the "Bundled tracker definitions" table in README.md
// from the definitions themselves:
//
//	go run ./tools/defsdoc          # rewrite the table
//	go run ./tools/defsdoc -check   # fail if it is out of date (no writes)
//
// The table was hand-maintained and drifted — Anthelion still advertised
// "possibly adding API stats in the future" after they had shipped them, and
// its Platform column still said Gazelle after the type was renamed. Every
// column except Notes now comes from the defs, so it cannot say something the
// app doesn't believe.
//
// The Notes column stays hand-written and is PRESERVED across regeneration:
// it carries editorial context ("Ratioless", "can't seek approval") that no
// def field captures, and losing it would be a worse outcome than the drift
// this fixes.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
)

const (
	beginMarker = "<!-- BEGIN GENERATED TRACKER TABLE (go run ./tools/defsdoc) -->"
	endMarker   = "<!-- END GENERATED TRACKER TABLE -->"
)

func main() {
	check := flag.Bool("check", false, "report whether README.md is up to date instead of rewriting it")
	readmePath := flag.String("readme", "README.md", "path to README.md")
	defsDir := flag.String("defs", "defs", "tracker definitions directory")
	flag.Parse()

	reg, err := defs.Load(*defsDir)
	if err != nil {
		log.Fatalf("defs: %v", err)
	}
	for _, iss := range reg.Issues() {
		// A def that failed to load would silently vanish from the table.
		log.Printf("defs: %s: %s", iss.File, iss.Error)
	}

	readme, err := os.ReadFile(*readmePath)
	if err != nil {
		log.Fatalf("read %s: %v", *readmePath, err)
	}
	existing := string(readme)

	notes := parseNotes(existing)
	table := buildTable(reg, notes)
	updated, err := replaceSection(existing, table)
	if err != nil {
		log.Fatalf("%v", err)
	}
	// A note whose key matches no def would simply vanish — which is how three
	// of them were lost when tracker names were tidied (Oldtoons →
	// OldToonsWorld). Losing hand-written editorial content silently is worse
	// than any drift this tool fixes, so refuse rather than write.
	if orphans := orphanedNotes(reg, notes); len(orphans) > 0 {
		log.Fatalf("these notes match no tracker definition and would be lost — "+
			"rename them to the def's `name`, or delete them deliberately:\n  %s",
			strings.Join(orphans, "\n  "))
	}

	if *check {
		if updated != existing {
			log.Fatalf("%s is out of date — run: go run ./tools/defsdoc", *readmePath)
		}
		fmt.Printf("%s tracker table is up to date\n", *readmePath)
		return
	}
	if updated == existing {
		fmt.Printf("%s already up to date\n", *readmePath)
		return
	}
	if err := os.WriteFile(*readmePath, []byte(updated), 0o644); err != nil {
		log.Fatalf("write %s: %v", *readmePath, err)
	}
	fmt.Printf("%s tracker table regenerated\n", *readmePath)
}

// rowRe matches any markdown table row. Notes are recovered by taking the
// FIRST cell as the key and the LAST as the note, rather than counting
// columns: the first run of this tool had to widen the table, and a
// column-counting parser silently matched nothing and wiped every
// hand-written note. Position-independent parsing survives the next widening.
var rowRe = regexp.MustCompile(`(?m)^\|(.+)\|\s*$`)

// parseNotes recovers the hand-written Notes column from the current table.
func parseNotes(readme string) map[string]string {
	notes := map[string]string{}
	start := strings.Index(readme, beginMarker)
	end := strings.Index(readme, endMarker)
	if start < 0 || end < 0 || end < start {
		return notes
	}
	for _, m := range rowRe.FindAllStringSubmatch(readme[start:end], -1) {
		cells := strings.Split(m[1], "|")
		if len(cells) < 3 {
			continue
		}
		name := strings.TrimSpace(cells[0])
		note := strings.TrimSpace(cells[len(cells)-1])
		// Skip the header and the |---|---| separator.
		if name == "" || name == "Tracker" || strings.HasPrefix(name, "-") || note == "" {
			continue
		}
		notes[name] = note
	}
	return notes
}

// orphanedNotes lists notes whose tracker name matches no definition, so the
// caller can refuse to write rather than drop them.
func orphanedNotes(reg *defs.Registry, notes map[string]string) []string {
	names := map[string]bool{}
	for _, td := range reg.Trackers() {
		names[td.Name] = true
	}
	var out []string
	for name, note := range notes {
		if !names[name] {
			out = append(out, fmt.Sprintf("%q: %s", name, note))
		}
	}
	sort.Strings(out)
	return out
}

func buildTable(reg *defs.Registry, notes map[string]string) string {
	var b strings.Builder
	b.WriteString("| Tracker | Platform | Approved | Stats | Limit | Notes |\n")
	b.WriteString("|---|---|---|---|---|---|\n")

	trackers := reg.Trackers()
	sort.Slice(trackers, func(i, j int) bool {
		return strings.ToLower(trackers[i].Name) < strings.ToLower(trackers[j].Name)
	})
	for _, td := range trackers {
		if td.Type == "test" {
			continue // the credential-free demo isn't a real tracker
		}
		if td.Retired != nil {
			// The def is kept so existing users keep their history readable,
			// but the table answers "which trackers does Yata support?" — and
			// a site that has shut down is not an answer to that.
			continue
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
			td.Name,
			typeLabel(reg, td.Type),
			approval(td),
			statsCell(reg, td),
			limit(reg, td),
			notes[td.Name],
		))
	}
	return b.String()
}

func typeLabel(reg *defs.Registry, key string) string {
	if tt, ok := reg.Type(key); ok && tt.Label != "" {
		return tt.Label
	}
	return key
}

func approval(td defs.TrackerDef) string {
	switch td.ApprovalStatus() {
	case "approved":
		return "Yes"
	case "informal":
		return "Informal"
	case "pending":
		return "Asked"
	default:
		return "No"
	}
}

// statsCell is the capability figure — how much of this tracker's own
// promotion ladder Yata can follow, which is the question the table never
// answered and the Notes column kept trying to.
func statsCell(reg *defs.Registry, td defs.TrackerDef) string {
	caps := reg.ResolveCapabilities(td.URL, td.Type)
	sum := caps.Summarise(td)
	// A tracker that serves its own ladder has one — this tool just cannot see
	// it, because the endpoint is authenticated and there is no account here.
	// Saying so beats falling through to the raw stat count below, which reads
	// as "no ladder published" and means the opposite of what is true.
	if len(td.Groups) == 0 && reg.GroupAPI(td.URL, td.Type) != nil {
		return "Tracker-served"
	}
	if len(sum.Required) == 0 {
		// No published ladder to measure against — say how many stats arrive
		// instead of printing a meaningless "0 of 0".
		if n := len(sum.APIStats); n > 0 {
			return fmt.Sprintf("%d stats", n)
		}
		return "—"
	}
	cell := fmt.Sprintf("%d/%d", len(sum.MetAPI), len(sum.Required))
	if sum.ScrapePossible && len(sum.MetScrape) > len(sum.MetAPI) {
		cell += fmt.Sprintf(" (%d scraped)", len(sum.MetScrape))
	}
	return cell
}

func limit(reg *defs.Registry, td defs.TrackerDef) string {
	rs := reg.ResolveScrape(td.URL, td.Type)
	switch {
	case rs.OptedOut:
		return "Opted out"
	case rs.DisableScraping || rs.SkipHTMLScrape:
		return "API only"
	case rs.MaxScrapesPerDay == 1:
		return "Once per day"
	case rs.MinIntervalMinutes > 0:
		return fmt.Sprintf("%dmin", rs.MinIntervalMinutes)
	case rs.MaxScrapesPerDay > 0:
		return fmt.Sprintf("%d/day", rs.MaxScrapesPerDay)
	default:
		return "Default"
	}
}

func replaceSection(readme, table string) (string, error) {
	start := strings.Index(readme, beginMarker)
	end := strings.Index(readme, endMarker)
	if start < 0 || end < 0 {
		return "", fmt.Errorf("markers not found in README — add these around the table:\n%s\n%s",
			beginMarker, endMarker)
	}
	if end < start {
		return "", fmt.Errorf("END marker appears before BEGIN in README")
	}
	// Match the file's own line endings. The repo stores LF but checks out
	// CRLF on Windows, so emitting LF unconditionally left one block of the
	// file in the other convention — which made -check flip between "up to
	// date" and "stale" depending on who last touched the file.
	section := "\n" + table
	if usesCRLF(readme) {
		section = strings.ReplaceAll(section, "\n", "\r\n")
	}
	return readme[:start+len(beginMarker)] + section + readme[end:], nil
}

// usesCRLF reports whether CRLF is the file's prevailing line ending.
func usesCRLF(s string) bool {
	crlf := strings.Count(s, "\r\n")
	return crlf*2 > strings.Count(s, "\n")
}
