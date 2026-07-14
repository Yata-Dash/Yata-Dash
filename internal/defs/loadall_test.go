package defs

import "testing"

// TestShippedDefsLoadClean loads the real defs/ directory that ships with the
// app and fails on ANY load issue — a malformed tracker/type def should never
// reach a release. Also spot-checks the HUNO def's custom API + min_counts
// wiring end-to-end through the registry.
func TestShippedDefsLoadClean(t *testing.T) {
	r, err := Load("../../defs")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if issues := r.Issues(); len(issues) > 0 {
		t.Fatalf("defs load issues: %+v", issues)
	}

	td, ok := r.TrackerByURL("https://hawke.uno")
	if !ok {
		t.Fatal("hawke.uno def not found")
	}
	// The def's base type may change (custom ↔ unit3d); what matters is that
	// the custom API override is loaded and wired.
	if td.API == nil || td.API.Path != "/api/profile" || td.API.AuthMethod != "api_key_header" {
		t.Fatalf("unexpected HUNO api block: %+v", td.API)
	}
	// HUNO is typed unit3d (it IS a UNIT3D tracker) but its api block must
	// still win the fetch dispatch — the standard /api/user path would lose
	// the seed divisions, hunos→bonus and member_since→join_date mappings.
	if kind := r.APIKind("https://hawke.uno", ""); kind != "custom" {
		t.Fatalf("HUNO APIKind = %q, want custom (def api block must override the unit3d type)", kind)
	}
	// Same rule, def already typed custom (MAM) — and a plain unit3d def
	// without an api block still resolves to unit3d.
	if kind := r.APIKind("https://www.myanonamouse.net", ""); kind != "custom" {
		t.Errorf("MAM APIKind = %q, want custom", kind)
	}
	if kind := r.APIKind("https://seedpool.org", ""); kind != "unit3d" {
		t.Errorf("seedpool APIKind = %q, want unit3d", kind)
	}
	if td.API.FieldMap["data.seed_divisions.vanguard"] != "vanguard_seeds" {
		t.Error("seed division field_map missing")
	}
	if got := len(td.Groups); got != 6 {
		t.Fatalf("HUNO groups = %d, want 6", got)
	}
	// Targaryen (top tier) carries ordered min_counts; first entry is squire.
	top := td.Groups[len(td.Groups)-1]
	if top.Name != "Targaryen" || len(top.Requirements.MinCounts) != 5 {
		t.Fatalf("Targaryen min_counts = %+v", top.Requirements.MinCounts)
	}
	if mc := top.Requirements.MinCounts[0]; mc.Field != "squire_seeds" || mc.Count != 100 {
		t.Errorf("min_counts order/values wrong: %+v", mc)
	}
	// The custom type requires a manual join_date, but HUNO's API provides
	// one — the fetch path maps member_since → join_date.
	if td.API.FieldMap["data.member_since"] != "join_date" {
		t.Error("join_date mapping missing")
	}
}
