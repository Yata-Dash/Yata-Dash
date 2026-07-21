package api

import (
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

func statField(v any) models.StatField { return models.StatField{Value: v} }

// TestEvaluateTargetRowsBaseTargets covers the base-target boundary rules:
// >= is met, a missing current stat is UNMET (never skipped), and ratio
// "Infinity" clears any finite target.
func TestEvaluateTargetRowsBaseTargets(t *testing.T) {
	joinDate := time.Now().AddDate(-1, -1, 0).Format("2006-01-02") // well over 30 days old
	tr := models.Tracker{
		ID: "t1",
		Targets: map[string]string{
			"uploaded":  "1 TiB",
			"ratio":     "2.0",
			"days":      "30",
			"fl_tokens": "10", // custom key — current stat never arrives below
		},
	}
	m := models.MergedStats{
		"uploaded":  statField("2 TiB"),    // met
		"ratio":     statField("Infinity"), // met — ∞ always clears a finite target
		"join_date": statField(joinDate),   // met — well past 30 days
		// fl_tokens absent entirely.
	}
	rows, met, total := evaluateTargetRows(tr, m, nil)
	if total != 4 {
		t.Fatalf("total = %d, want 4", total)
	}
	if met != 3 {
		t.Fatalf("met = %d, want 3 (fl_tokens must stay unmet)", met)
	}
	for _, r := range rows {
		switch r.Key {
		case "uploaded", "ratio", "days":
			if !r.Met {
				t.Errorf("%s: expected met", r.Key)
			}
		case "fl_tokens":
			if r.Met {
				t.Error("fl_tokens: a missing current stat must be UNMET, not skipped")
			}
		}
	}
}

// TestEvaluateTargetRowsBoundariesAndCustomFallback checks the >= boundary at
// exact equality, GiB size parsing, and the unknown-key numeric fallback
// (mirrors grid.ts's generic loop: size parse first, then plain numbers).
func TestEvaluateTargetRowsBoundariesAndCustomFallback(t *testing.T) {
	tr := models.Tracker{
		ID: "t1",
		Targets: map[string]string{
			"seed_size":       "1 TiB", // current below in raw units, equal in GiB
			"ratio":           "2.0",
			"upload_snatches": "50", // unknown key, plain numeric fallback
		},
	}
	m := models.MergedStats{
		"seed_size":       statField("1024 GiB"), // exactly 1 TiB
		"ratio":           statField("1.99"),     // just under target
		"upload_snatches": statField("100"),
	}
	rows, met, total := evaluateTargetRows(tr, m, nil)
	if total != 3 || met != 2 {
		t.Fatalf("met/total = %d/%d, want 2/3", met, total)
	}
	for _, r := range rows {
		switch r.Key {
		case "seed_size":
			if !r.Met {
				t.Error("1024 GiB current vs 1 TiB target must be MET (>= at exact equality)")
			}
		case "ratio":
			if r.Met {
				t.Error("ratio 1.99 < target 2.0 must be UNMET")
			}
		case "upload_snatches":
			if !r.Met {
				t.Error("unknown key with numeric current/target must fall back to plain numeric compare")
			}
			if r.Label != "Upload Snatches" {
				t.Errorf("label = %q, want title-cased \"Upload Snatches\"", r.Label)
			}
		}
	}
}

// TestEvaluateTargetRowsEmpty: a tracker with no targets and no target group
// yields nothing to track — the caller (evaluateTrackerTargets) relies on
// this to skip calling into the alert engine at all.
func TestEvaluateTargetRowsEmpty(t *testing.T) {
	rows, met, total := evaluateTargetRows(models.Tracker{ID: "t1"}, models.MergedStats{}, nil)
	if len(rows) != 0 || met != 0 || total != 0 {
		t.Fatalf("expected an empty result, got rows=%d met=%d total=%d", len(rows), met, total)
	}
}

func TestEvaluateTargetRowsCombinedTransferRequirement(t *testing.T) {
	req := defs.GroupRequirements{MinTotalTransfer: "500 GB"}
	tr := models.Tracker{ID: "t1", TargetGroup: "Member", Targets: groupRequirementsToTargets(req)}
	groups := []defs.GroupDef{{Name: "Member", Requirements: req}}
	merged := models.MergedStats{"total_transfer": statField("600 GB")}
	rows, met, total := evaluateTargetRows(tr, merged, groups)
	if len(rows) != 1 || met != 1 || total != 1 {
		t.Fatalf("combined transfer rows=%+v met=%d total=%d", rows, met, total)
	}
	if rows[0].Key != "total_transfer" || rows[0].Label != "Total Transfer" || !rows[0].Met {
		t.Fatalf("combined transfer row = %+v", rows[0])
	}
}

// TestEvaluateTargetRowsMinCountsAndAnyOf covers a target group's min_counts
// (one EDGE row per count, label falls back to title-case) and any_of (one
// EDGE row PER ALTERNATIVE, but the two alternatives collapse to a single
// logical row for the m/T count).
func TestEvaluateTargetRowsMinCountsAndAnyOf(t *testing.T) {
	tr := models.Tracker{ID: "t1", TargetGroup: "Elite"}
	groups := []defs.GroupDef{
		{
			Name: "Elite",
			Requirements: defs.GroupRequirements{
				MinCounts: []defs.MinCountReq{
					{Field: "vanguard_seeds", Count: 5, Label: "Vanguard"},
					{Field: "champion_seeds", Count: 10}, // no label → title-cased field
				},
				AnyOf: []defs.GroupRequirements{
					{MinUploaded: "1 TiB"},
					{MinSeedSize: "500 GiB"},
				},
			},
		},
	}
	m := models.MergedStats{
		"vanguard_seeds": statField("5"),       // met (>= 5)
		"champion_seeds": statField("3"),       // unmet (< 10)
		"uploaded":       statField("2 TiB"),   // meets any_of alt 0
		"seed_size":      statField("100 GiB"), // does not meet any_of alt 1
	}
	rows, met, total := evaluateTargetRows(tr, m, groups)

	// Logical total: 2 min_counts rows + 1 (any_of collapses to ONE logical row).
	if total != 3 {
		t.Fatalf("logical total = %d, want 3", total)
	}
	// Logical met: vanguard (1) + any_of met via alt 0 (1) = 2; champion unmet.
	if met != 2 {
		t.Fatalf("logical met = %d, want 2", met)
	}
	// EDGE rows: 2 min_counts + 2 any_of alternatives = 4 separate rows.
	if len(rows) != 4 {
		t.Fatalf("edge rows = %d, want 4", len(rows))
	}

	found := map[string]bool{}
	for _, r := range rows {
		found[r.Key] = true
		switch r.Key {
		case "min_counts.vanguard_seeds":
			if !r.Met || r.Label != "Vanguard" {
				t.Errorf("vanguard_seeds row = %+v, want met=true label=Vanguard", r)
			}
		case "min_counts.champion_seeds":
			if r.Met || r.Label != "Champion Seeds" {
				t.Errorf("champion_seeds row = %+v, want met=false label=Champion Seeds", r)
			}
		case "any_of.0":
			if !r.Met {
				t.Errorf("any_of.0 (uploaded alt) row = %+v, want met=true", r)
			}
		case "any_of.1":
			if r.Met {
				t.Errorf("any_of.1 (seed_size alt) row = %+v, want met=false", r)
			}
		}
	}
	for _, k := range []string{"min_counts.vanguard_seeds", "min_counts.champion_seeds", "any_of.0", "any_of.1"} {
		if !found[k] {
			t.Errorf("expected row %q, not found", k)
		}
	}
}
