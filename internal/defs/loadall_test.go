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

// TestShippedDefsResolveTheirFetcher checks that each def dispatches to the
// fetcher it was written for. This is deliberately the ONLY per-tracker
// assertion here: it tests the type→kind WIRING (which breaks silently and
// sends real requests to the wrong endpoint), not the defs' data. Restating
// group counts, colours or ladder order in Go would just mean every routine
// def edit also breaks a test, without catching anything a load issue or a
// fetch test wouldn't.
func TestShippedDefsResolveTheirFetcher(t *testing.T) {
	r, err := Load("../../defs")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, tc := range []struct{ url, want string }{
		{"https://redacted.sh", "gazelle_json"},
		{"https://orpheus.network", "gazelle_json"},
		{"https://gazellegames.net", "gazelle_games"},
		{"https://animebytes.tv", "custom"},
		{"https://broadcasthe.net", "custom"},
		{"https://nebulance.io", "custom"},
		{"https://blutopia.cc", "unit3d"},
		{"https://reelflix.cc", "unit3d"},
		{"https://upload.cx", "unit3d"},
	} {
		td, ok := r.TrackerByURL(tc.url)
		if !ok {
			t.Errorf("%s: def not found", tc.url)
			continue
		}
		if kind := r.APIKind(td.URL, td.Type); kind != tc.want {
			t.Errorf("%s APIKind = %q, want %q", tc.url, kind, tc.want)
		}
	}
}

// TestHHDDefResolves pins the HHD def's wiring. It is API-only, so a silent
// regression in any of these leaves the tracker reporting less than it can
// with nothing to notice — there is no scrape to fall back on.
func TestHHDDefResolves(t *testing.T) {
	r, err := Load("../../defs")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	const url = "https://homiehelpdesk.net"

	// Stock /api/user, so the plain UNIT3D fetcher must handle it — an api
	// block would divert it to the custom fetcher and lose convertCoreBytes.
	if kind := r.APIKind(url, ""); kind != "unit3d" {
		t.Errorf("APIKind = %q, want unit3d", kind)
	}

	fm := r.ResolveAPIFieldMap(url, "unit3d")
	want := map[string]string{
		"joined_at":      "join_date",
		"seedtime_total": "total_seedtime",
		"uploads_count":  "uploads_approved",
		"seedbonus":      "bonus_points", // inherited from the type
	}
	for from, to := range want {
		if got := fm[from]; got != to {
			t.Errorf("field map %q = %q, want %q", from, got, to)
		}
	}

	// Scraping off: the class ladder is fully covered by the API, and the def
	// promises no HTTP beyond /api/user.
	if sp := r.ResolveScrape(url, "unit3d"); !sp.DisableScraping {
		t.Errorf("scraping should be disabled: %+v", sp)
	}

	// Every stat the ladder needs must be declared as an API capability, or
	// the UI will show requirements it can never evaluate.
	caps := r.ResolveCapabilities(url, "unit3d")
	api := map[string]bool{}
	for _, f := range caps.APIStats {
		api[f] = true
	}
	for _, f := range []string{
		"username", "group", "join_date", "uploaded", "ratio",
		"seed_size", "avg_seed_time", "uploads_approved", "last_login",
	} {
		if !api[f] {
			t.Errorf("capability %q missing; have %v", f, caps.APIStats)
		}
	}
}
