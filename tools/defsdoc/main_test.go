package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
)

// TestParseNotesSurvivesColumnChanges is the regression guard for how this
// tool first went wrong: the generated table gained a column, the parser
// counted columns to find the Notes cell, matched nothing, and silently wiped
// every hand-written note in the README. Keys come from the first cell and
// notes from the last, so widening the table again can't repeat it.
func TestParseNotesSurvivesColumnChanges(t *testing.T) {
	layouts := map[string]string{
		"five columns (the original layout)": `
| Tracker | Platform | Approved by tracker | Limit | Notes |
|---|---|---|---|---|
| Aither | Unit3D | Yes | 180min | Monthly Uploads not currently retrievable |
| Aura4K | Unit3D | Yes | 180min |  |
`,
		"six columns (after the Stats column)": `
| Tracker | Platform | Approved | Stats | Limit | Notes |
|---|---|---|---|---|---|
| Aither | UNIT3D | Yes | 3/6 | 180min | Monthly Uploads not currently retrievable |
| Aura4K | UNIT3D | Yes | 2/6 | 180min |  |
`,
		"seven columns (a future one)": `
| Tracker | Platform | Approved | Stats | Events | Limit | Notes |
|---|---|---|---|---|---|---|
| Aither | UNIT3D | Yes | 3/6 | No | 180min | Monthly Uploads not currently retrievable |
`,
	}
	for name, table := range layouts {
		t.Run(name, func(t *testing.T) {
			notes := parseNotes(beginMarker + table + endMarker)
			if got := notes["Aither"]; got != "Monthly Uploads not currently retrievable" {
				t.Errorf("note not recovered: %q", got)
			}
			// A blank note must not be stored — it would overwrite nothing,
			// but it would also mask a genuinely missing key.
			if _, present := notes["Aura4K"]; present && notes["Aura4K"] == "" {
				t.Error("an empty note should not be recorded")
			}
			for _, junk := range []string{"Tracker", "---"} {
				if _, present := notes[junk]; present {
					t.Errorf("header/separator row %q parsed as a tracker", junk)
				}
			}
		})
	}
}

// TestReplaceSectionRequiresMarkers: without the markers the tool must refuse
// rather than guess where the table is — a wrong guess rewrites prose.
func TestReplaceSectionRequiresMarkers(t *testing.T) {
	if _, err := replaceSection("# README\n\nno markers here\n", "| a |\n"); err == nil {
		t.Error("missing markers should be an error, not a silent no-op")
	}
	if _, err := replaceSection(endMarker+"\n"+beginMarker, "| a |\n"); err == nil {
		t.Error("markers in the wrong order should be an error")
	}
	out, err := replaceSection("before\n"+beginMarker+"\nOLD\n"+endMarker+"\nafter\n", "NEW\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "OLD") || !strings.Contains(out, "NEW") {
		t.Errorf("section not replaced: %q", out)
	}
	for _, keep := range []string{"before", "after"} {
		if !strings.Contains(out, keep) {
			t.Errorf("text outside the markers was lost: %q", keep)
		}
	}
}

// TestReplaceSectionMatchesLineEndings: the repo stores LF but checks out CRLF
// on Windows, so writing LF unconditionally left one block of README.md in the
// other convention and made -check flip between "up to date" and "stale"
// depending on who last saved the file.
func TestReplaceSectionMatchesLineEndings(t *testing.T) {
	table := "| A | B |\n| C | D |\n"

	crlfDoc := "intro\r\n" + beginMarker + "\r\nOLD\r\n" + endMarker + "\r\nrest\r\n"
	out, err := replaceSection(crlfDoc, table)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "\n") != strings.Count(out, "\r\n") {
		t.Errorf("CRLF file gained bare LF lines: %q", out)
	}

	lfDoc := "intro\n" + beginMarker + "\nOLD\n" + endMarker + "\nrest\n"
	out, err = replaceSection(lfDoc, table)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\r") {
		t.Errorf("LF file gained carriage returns: %q", out)
	}

	// Rewriting an already-current file must be a no-op in both conventions,
	// or -check can never be trusted.
	for _, doc := range []string{crlfDoc, lfDoc} {
		once, _ := replaceSection(doc, table)
		twice, _ := replaceSection(once, table)
		if once != twice {
			t.Error("regeneration is not idempotent")
		}
	}
}

// TestOrphanedNotesAreDetected: notes are keyed on the tracker's name, so
// renaming a def orphans its note and the next run would drop it silently.
// Three real notes were lost that way (Oldtoons → OldToonsWorld) before this
// check existed. Losing hand-written editorial content is a worse failure
// than the drift this tool exists to fix, so it must refuse rather than write.
func TestOrphanedNotesAreDetected(t *testing.T) {
	reg, err := defs.Load("../../defs")
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	orphans := orphanedNotes(reg, map[string]string{
		"Zenith":      "a note for a tracker that exists",
		"Oldtoons":    "a note left behind by a rename",
		"GoneForever": "a note for a def that was deleted",
	})
	if len(orphans) != 2 {
		t.Fatalf("expected the two unmatched notes, got %v", orphans)
	}
	joined := strings.Join(orphans, " ")
	for _, want := range []string{"Oldtoons", "GoneForever"} {
		if !strings.Contains(joined, want) {
			t.Errorf("orphan %q not reported: %v", want, orphans)
		}
	}
	if strings.Contains(joined, "Zenith") {
		t.Errorf("a matched note was reported as orphaned: %v", orphans)
	}
}

// TestShippedReadmeHasNoOrphanedNotes: the real README must stay clean, or
// the next regeneration refuses to run.
func TestShippedReadmeHasNoOrphanedNotes(t *testing.T) {
	reg, err := defs.Load("../../defs")
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if orphans := orphanedNotes(reg, parseNotes(string(readme))); len(orphans) > 0 {
		t.Errorf("README notes match no tracker def:\n  %s", strings.Join(orphans, "\n  "))
	}
}
