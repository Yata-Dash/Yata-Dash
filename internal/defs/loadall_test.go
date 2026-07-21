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

	nbl, ok := r.TrackerByURL("https://nebulance.io")
	if !ok {
		t.Fatal("nebulance.io def not found")
	}
	if nbl.API == nil || nbl.API.AuthMethod != "api_key_query" || nbl.API.APIKeyParam != "api_key" {
		t.Fatalf("unexpected Nebulance API block: %+v", nbl.API)
	}
	if nbl.API.SuccessField != "status" || nbl.API.SuccessValue != "success" {
		t.Fatalf("unexpected Nebulance success envelope: %+v", nbl.API)
	}
	if nbl.Rules == nil || nbl.Rules.MinSeedDaysEpisode != 1 || nbl.Rules.MinSeedDaysSeason != 5 {
		t.Fatalf("unexpected Nebulance seed rules: %+v", nbl.Rules)
	}
	if !nbl.Scrape.DisableScraping || nbl.ApprovalStatus() != ApprovalUnknown {
		t.Fatalf("Nebulance must be API-only and unapproved: scrape=%+v approval=%q", nbl.Scrape, nbl.ApprovalStatus())
	}
	if len(nbl.Groups) != 14 {
		t.Fatalf("Nebulance groups = %d, want 14", len(nbl.Groups))
	}
	wantColors := map[string]string{
		"Colonial": "#8ba8c1", "Ensign": "#4fc986", "Flattop": "#4fc986",
		"Nugget": "#4fc986", "Raptor": "#33cc33", "Viper": "#01c3b7",
		"Orion": "#1990ff", "Valkyrie": "#1990ff", "Torrent Celebrity": "#9933ff",
		"Cylon": "#40bfff", "Legend": "#d59017", "Moderator": "#c63526",
		"Administrator": "#bf5fff", "SysOp": "#33cc33",
	}
	for _, group := range nbl.Groups {
		if group.Style.Color != wantColors[group.Name] || group.Style.Icon != "" {
			t.Errorf("unexpected %s style: %+v", group.Name, group.Style)
		}
		if group.Name == "RAS" || group.Name == "Donor" || group.Name == "Customised title" {
			t.Errorf("non-class group included: %s", group.Name)
		}
	}
}
