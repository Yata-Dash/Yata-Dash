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

	red, ok := r.TrackerByURL("https://redacted.sh")
	if !ok {
		t.Fatal("redacted.sh def not found")
	}
	if kind := r.APIKind(red.URL, red.Type); kind != "gazelle_json" {
		t.Fatalf("Redacted APIKind = %q, want gazelle_json", kind)
	}
	redType, ok := r.Type(red.Type)
	if !ok || len(redType.API.RequiredFields) != 0 {
		t.Fatalf("Redacted type must require only an API key: %+v", redType)
	}
	if !red.Scrape.DisableScraping || red.ApprovalStatus() != ApprovalUnknown {
		t.Fatalf("Redacted must be API-only and unapproved: scrape=%+v approval=%q", red.Scrape, red.ApprovalStatus())
	}
	if red.InviteRequirements == nil || red.InviteRequirements.MinClass != "Power User" {
		t.Fatalf("Redacted invite requirements = %+v", red.InviteRequirements)
	}
	wantPrimary := []string{"User", "Member", "Power User", "Elite", "Torrent Master", "Power TM", "Elite TM"}
	if len(red.Groups) != 15 {
		t.Fatalf("Redacted groups = %d, want 15", len(red.Groups))
	}
	for i, name := range wantPrimary {
		group := red.Groups[i]
		if group.Name != name {
			t.Errorf("Redacted group %d = %q, want %q", i, group.Name, name)
		}
	}
	for _, group := range red.Groups {
		if group.Style.Color != "" || group.Style.Icon != "" {
			t.Errorf("Redacted %s style must be empty: %+v", group.Name, group.Style)
		}
		switch group.Name {
		case "First Line Support", "Interviewer", "Torrent Celebrity", "Progress Team", "Design Team", "Beta Team", "Artist", "Alpha Team":
			t.Errorf("secondary Redacted class included in primary ladder: %s", group.Name)
		}
	}
	powerTM := red.Groups[5].Requirements
	if powerTM.MinUploads != 500 || len(powerTM.MinCounts) != 1 || powerTM.MinCounts[0].Field != "groups_uploaded" {
		t.Errorf("unexpected Power TM requirements: %+v", powerTM)
	}
	eliteTM := red.Groups[6].Requirements
	if len(eliteTM.MinCounts) != 2 || eliteTM.MinCounts[1].Field != "perfect_flacs" {
		t.Errorf("unexpected Elite TM requirements: %+v", eliteTM)
	}

	ops, ok := r.TrackerByURL("https://orpheus.network")
	if !ok {
		t.Fatal("orpheus.network def not found")
	}
	if kind := r.APIKind(ops.URL, ops.Type); kind != "gazelle_json" {
		t.Fatalf("Orpheus APIKind = %q, want gazelle_json", kind)
	}
	if !ops.Scrape.DisableScraping || ops.ApprovalStatus() != ApprovalUnknown {
		t.Fatalf("Orpheus must be API-only and unapproved: scrape=%+v approval=%q", ops.Scrape, ops.ApprovalStatus())
	}
	if ops.Rules == nil || ops.Rules.MinRatio != 0 || ops.Rules.Note == "" {
		t.Fatalf("Orpheus rules = %+v, want no fixed min_ratio and a dynamic-ratio note", ops.Rules)
	}
	if ops.InviteRequirements != nil {
		t.Fatalf("Orpheus invite requirements = %+v, want none (no site-wide policy documented)", ops.InviteRequirements)
	}
	wantOPSPrimary := []string{
		"User", "Member", "Power User", "Elite", "Torrent Master",
		"Power Torrent Master", "Elite Torrent Master", "Ultimate Torrent Master",
	}
	if len(ops.Groups) != 22 {
		t.Fatalf("Orpheus groups = %d, want 22", len(ops.Groups))
	}
	for i, name := range wantOPSPrimary {
		group := ops.Groups[i]
		if group.Name != name {
			t.Errorf("Orpheus group %d = %q, want %q", i, group.Name, name)
		}
	}
	for _, group := range ops.Groups {
		if group.Style.Color != "" || group.Style.Icon != "" {
			t.Errorf("Orpheus %s style must be empty: %+v", group.Name, group.Style)
		}
	}
	ptm := ops.Groups[5].Requirements
	if len(ptm.MinCounts) != 1 || ptm.MinCounts[0].Field != "groups_uploaded" || ptm.MinCounts[0].Count != 500 {
		t.Errorf("unexpected Orpheus Power Torrent Master requirements: %+v", ptm)
	}
	etm := ops.Groups[6].Requirements
	if len(etm.MinCounts) != 1 || etm.MinCounts[0].Field != "perfect_flacs" || etm.MinCounts[0].Count != 500 {
		t.Errorf("unexpected Orpheus Elite Torrent Master requirements: %+v", etm)
	}
	utm := ops.Groups[7].Requirements
	if len(utm.MinCounts) != 1 || utm.MinCounts[0].Field != "perfect_flacs" || utm.MinCounts[0].Count != 2000 {
		t.Errorf("unexpected Orpheus Ultimate Torrent Master requirements: %+v", utm)
	}

	ar, ok := r.TrackerByURL("https://alpharatio.cc")
	if !ok {
		t.Fatal("alpharatio.cc def not found")
	}
	if kind := r.APIKind(ar.URL, ar.Type); kind != "gazelle_json_cookie" {
		t.Fatalf("AlphaRatio APIKind = %q, want gazelle_json_cookie", kind)
	}
	arType, ok := r.Type(ar.Type)
	if !ok || len(arType.API.RequiredFields) != 1 || arType.API.RequiredFields[0] != "session_cookie" {
		t.Fatalf("AlphaRatio type must require a session cookie: %+v", arType)
	}
	if name := r.APICookieName(ar.URL, ar.Type); name != "session" {
		t.Fatalf("AlphaRatio cookie name = %q, want session", name)
	}
	if !ar.Scrape.DisableScraping || ar.ApprovalStatus() != ApprovalUnknown {
		t.Fatalf("AlphaRatio must be API-only and unapproved: scrape=%+v approval=%q", ar.Scrape, ar.ApprovalStatus())
	}
	if ar.Rules == nil || ar.Rules.MinRatio != 1.0 || ar.Rules.Note == "" {
		t.Fatalf("AlphaRatio rules = %+v, want min_ratio 1.0 and a note", ar.Rules)
	}
	if ar.InviteRequirements == nil || ar.InviteRequirements.MinClass != "Philosopher" {
		t.Fatalf("AlphaRatio invite requirements = %+v", ar.InviteRequirements)
	}
	wantARPrimary := []string{
		"Mortal", "Philosopher", "Gladiator", "Giant", "Centaur", "Sphinx",
		"Harpy", "Satyr", "Adonis", "Cyclops", "Chimera",
	}
	if len(ar.Groups) != 22 {
		t.Fatalf("AlphaRatio groups = %d, want 22", len(ar.Groups))
	}
	for i, name := range wantARPrimary {
		group := ar.Groups[i]
		if group.Name != name {
			t.Errorf("AlphaRatio group %d = %q, want %q", i, group.Name, name)
		}
	}
	for _, group := range ar.Groups {
		if group.Style.Color != "" || group.Style.Icon != "" {
			t.Errorf("AlphaRatio %s style must be empty: %+v", group.Name, group.Style)
		}
		switch group.Name {
		case "Exiled", "Donor", "Custom Titles":
			t.Errorf("non-class/account-state group included: %s", group.Name)
		}
	}
	philosopher := ar.Groups[1].Requirements
	if philosopher.MinUploaded != "80 GB" || philosopher.MinBonusPoints != 60000 || philosopher.MinAge != "4W" {
		t.Errorf("unexpected AlphaRatio Philosopher requirements: %+v", philosopher)
	}
	spartan := ar.Groups[12].Requirements
	if ar.Groups[12].Name != "Spartan" || spartan.MinUploaded != "10 TB" || spartan.MinBonusPoints != 6200000 {
		t.Errorf("unexpected AlphaRatio Spartan (%s) requirements: %+v", ar.Groups[12].Name, spartan)
	}

	ggn, ok := r.TrackerByURL("https://gazellegames.net")
	if !ok {
		t.Fatal("gazellegames.net def not found")
	}
	if kind := r.APIKind(ggn.URL, ggn.Type); kind != "gazelle_games" {
		t.Fatalf("GazelleGames APIKind = %q, want gazelle_games", kind)
	}
	if !ggn.Scrape.DisableScraping || ggn.ApprovalStatus() != ApprovalUnknown {
		t.Fatalf("GazelleGames must be API-only and unapproved: scrape=%+v approval=%q", ggn.Scrape, ggn.ApprovalStatus())
	}
	if ggn.InviteRequirements == nil || ggn.InviteRequirements.MinClass != "Gamer" {
		t.Fatalf("GazelleGames invite requirements = %+v", ggn.InviteRequirements)
	}
	if ggn.Rules == nil || ggn.Rules.MinSeedHours != 80 {
		t.Fatalf("GazelleGames minimum seed rule = %+v, want 80 hours", ggn.Rules)
	}
	wantGGnPrimary := []string{"Amateur", "Gamer", "Pro Gamer", "Elite Gamer", "Legendary Gamer", "Master Gamer", "Gaming God"}
	if len(ggn.Groups) != 21 {
		t.Fatalf("GazelleGames groups = %d, want 21", len(ggn.Groups))
	}
	for i, name := range wantGGnPrimary {
		group := ggn.Groups[i]
		if group.Name != name {
			t.Errorf("GazelleGames group %d = %q, want %q", i, group.Name, name)
		}
		if group.Style.Color != "" || group.Style.Icon != "" {
			t.Errorf("GazelleGames %s style must be empty: %+v", group.Name, group.Style)
		}
		if i > 0 {
			if len(group.Requirements.MinCounts) != 1 || group.Requirements.MinCounts[0].Field != "achievement_points" {
				t.Errorf("GazelleGames %s point requirement = %+v", group.Name, group.Requirements.MinCounts)
			}
		}
	}
	if got := ggn.Groups[6].Requirements.MinCounts[0].Count; got != 6000 {
		t.Errorf("Gaming God points = %d, want 6000", got)
	}

	blu, ok := r.TrackerByURL("https://blutopia.cc")
	if !ok {
		t.Fatal("blutopia.cc def not found")
	}
	if kind := r.APIKind(blu.URL, blu.Type); kind != "unit3d" {
		t.Fatalf("Blutopia APIKind = %q, want unit3d", kind)
	}
	if !blu.Scrape.DisableScraping || blu.ApprovalStatus() != ApprovalUnknown {
		t.Fatalf("Blutopia must be API-only and unapproved: scrape=%+v approval=%q", blu.Scrape, blu.ApprovalStatus())
	}
	if blu.Rules == nil || blu.Rules.MinRatio != 0.4 || blu.Rules.MinSeedDays != 7 {
		t.Fatalf("Blutopia rules = %+v, want ratio 0.4 and 7 seed days", blu.Rules)
	}
	if blu.InviteRequirements == nil || blu.InviteRequirements.MinClass != "BluMaster" {
		t.Fatalf("Blutopia invite requirements = %+v", blu.InviteRequirements)
	}
	wantBLUGroups := []string{
		"User", "BluUser", "BluMaster", "BluExtremist", "BluLegend", "Blutopian",
		"BluSeeder", "BluCollector", "BluArchivist", "Junior Uploader", "Uploader",
		"Trustee", "Internal", "Editor", "Torrent Moderator", "Moderator", "Super Mod",
		"systemd", "Administrator", "Super Admin",
	}
	if len(blu.Groups) != len(wantBLUGroups) {
		t.Fatalf("Blutopia groups = %d, want %d", len(blu.Groups), len(wantBLUGroups))
	}
	wantBLUStyles := map[string]GroupStyle{
		"User":              {Color: "#c2d7fb", Icon: "fas fa-user"},
		"BluUser":           {Color: "#b7c6f1", Icon: "fas fa-user-tie"},
		"BluMaster":         {Color: "#9ba9e5", Icon: "fas fa-user-graduate"},
		"BluExtremist":      {Color: "#707ed2", Icon: "fas fa-user-astronaut"},
		"BluLegend":         {Color: "#515ec8", Icon: "fas fa-solid fa-user-bounty-hunter"},
		"Blutopian":         {Color: "#2978d4", Icon: "fas fa-rocket-launch", Sparkle: true},
		"BluSeeder":         {Color: "#0092e0", Icon: "fas fa-usb-drive"},
		"BluCollector":      {Color: "#1fb0ff", Icon: "fas fa-hdd"},
		"BluArchivist":      {Color: "#5cc6ff", Icon: "fas fa-server", Sparkle: true},
		"Junior Uploader":   {Color: "#67dd99", Icon: "fas fa-angle-up"},
		"Uploader":          {Color: "#2ecc71", Icon: "fas fa-angle-double-up"},
		"Trustee":           {Color: "#bf55ec", Icon: "fas fa-user-shield"},
		"Internal":          {Color: "#baaf92", Icon: "fas fa-wand-magic-sparkles"},
		"Editor":            {Color: "#15b097", Icon: "fas fa-user-pen"},
		"Torrent Moderator": {Color: "#15b097", Icon: "fas fa-badge-check"},
		"Moderator":         {Color: "#0beac5", Icon: "fas fa-gavel"},
		"Super Mod":         {Color: "#ea7c0b", Icon: "fas fa-dragon"},
		"systemd":           {Color: "#3fd475", Icon: "fas fa-code-compare"},
		"Administrator":     {Color: "#e30b5d", Icon: "fas fa-chess-queen"},
		"Super Admin":       {Color: "#ff0000", Icon: "fas fa-chess-king"},
	}
	for i, name := range wantBLUGroups {
		group := blu.Groups[i]
		if group.Name != name {
			t.Errorf("Blutopia group %d = %q, want %q", i, group.Name, name)
		}
		if group.Style != wantBLUStyles[name] {
			t.Errorf("Blutopia %s style = %+v, want %+v", name, group.Style, wantBLUStyles[name])
		}
		switch group.Name {
		case "Pruned", "Banned", "Disabled", "Validating", "Leech", "Supporter":
			t.Errorf("Blutopia account state/supporter overlay included as class: %s", group.Name)
		}
	}
	if req := blu.Groups[1].Requirements; req.MinUploaded != "1 TiB" || req.MinAge != "1M" {
		t.Errorf("BluUser requirements = %+v", req)
	}
	if req := blu.Groups[6].Requirements; req.MinSeedSize != "5 TiB" || req.MinAge != "1M" || req.MinSeedtime != "1M" {
		t.Errorf("BluSeeder requirements = %+v", req)
	}
	if req := blu.Groups[8].Requirements; req.MinSeedSize != "20 TiB" || req.MinAge != "3M" || req.MinSeedtime != "3M" {
		t.Errorf("BluArchivist requirements = %+v", req)
	}

	rfx, ok := r.TrackerByURL("https://reelflix.cc")
	if !ok {
		t.Fatal("reelflix.cc def not found")
	}
	if kind := r.APIKind(rfx.URL, rfx.Type); kind != "unit3d" {
		t.Fatalf("ReelFliX APIKind = %q, want unit3d", kind)
	}
	if !rfx.Scrape.DisableScraping || rfx.ApprovalStatus() != ApprovalUnknown {
		t.Fatalf("ReelFliX must be API-only and unapproved: scrape=%+v approval=%q", rfx.Scrape, rfx.ApprovalStatus())
	}
	if rfx.Rules == nil || rfx.Rules.MinRatio != 0.8 || rfx.Rules.MinSeedDays != 0 {
		t.Fatalf("ReelFliX rules = %+v, want ratio 0.8 and no seed-time rule", rfx.Rules)
	}
	if rfx.InviteRequirements == nil || rfx.InviteRequirements.MinClass != "Elite" {
		t.Fatalf("ReelFliX invite requirements = %+v", rfx.InviteRequirements)
	}
	wantRFXGroups := []string{
		"Leech", "User", "Member", "Pro", "Expert", "Elite", "Distributor",
		"Curator", "Archivist", "Uploader", "Celebrity", "Legend", "Internal", "Torrent Moderator",
	}
	if len(rfx.Groups) != len(wantRFXGroups) {
		t.Fatalf("ReelFliX groups = %d, want %d", len(rfx.Groups), len(wantRFXGroups))
	}
	wantRFXStyles := map[string]GroupStyle{
		"Leech":             {Color: "#96281b", Icon: "fal fa-user-ninja"},
		"User":              {Color: "#adb0b7", Icon: "fal fa-user-large"},
		"Member":            {Color: "#f2f2f2", Icon: "fal fa-user-graduate"},
		"Pro":               {Color: "#50c878", Icon: "fal fa-user-helmet-safety"},
		"Expert":            {Color: "#b2f7b2", Icon: "fal fa-user-astronaut"},
		"Elite":             {Color: "#39ff14", Icon: "fal fa-user-crown"},
		"Distributor":       {Color: "#580aff", Icon: "fal fa-hat-wizard"},
		"Curator":           {Color: "#5c95ff", Icon: "fal fa-helmet-battle"},
		"Archivist":         {Color: "#0aefff", Icon: "fal fa-crown"},
		"Uploader":          {Color: "#ff5f1f", Icon: "fal fa-video-plus"},
		"Celebrity":         {Color: "#af7ac5", Icon: "fal fa-martini-glass-citrus"},
		"Legend":            {Color: "#dbb42c", Icon: "fal fa-star-shooting", Sparkle: true},
		"Internal":          {Color: "#c40018", Icon: "far fa-cassette-vhs"},
		"Torrent Moderator": {Color: "#15b097", Icon: "fal fa-badge-check"},
	}
	for i, name := range wantRFXGroups {
		group := rfx.Groups[i]
		if group.Name != name {
			t.Errorf("ReelFliX group %d = %q, want %q", i, group.Name, name)
		}
		if group.Style != wantRFXStyles[name] {
			t.Errorf("ReelFliX %s style = %+v, want %+v", name, group.Style, wantRFXStyles[name])
		}
	}
	if req := rfx.Groups[2].Requirements; req.MinUploaded != "100 GiB" || req.MinRatio != 0.9 || req.MinAge != "5D" || req.MinSeedtime != "1D" {
		t.Errorf("ReelFliX Member requirements = %+v", req)
	}
	if req := rfx.Groups[8].Requirements; req.MinUploaded != "25 TiB" || req.MinRatio != 1.75 || req.MinAge != "2Y" || req.MinSeedtime != "6M" || req.MinSeedSize != "5 TiB" {
		t.Errorf("ReelFliX Archivist requirements = %+v", req)
	}
	if req := rfx.Groups[9].Requirements; req.MinMonthlyUploads != 25 || req.MinUploaded != "500 GiB" {
		t.Errorf("ReelFliX Uploader requirements = %+v", req)
	}

	ulcx, ok := r.TrackerByURL("https://upload.cx")
	if !ok {
		t.Fatal("upload.cx def not found")
	}
	if kind := r.APIKind(ulcx.URL, ulcx.Type); kind != "unit3d" {
		t.Fatalf("Upload.cx APIKind = %q, want unit3d", kind)
	}
	if !ulcx.Scrape.DisableScraping || ulcx.ApprovalStatus() != ApprovalUnknown {
		t.Fatalf("Upload.cx must be API-only and unapproved: scrape=%+v approval=%q", ulcx.Scrape, ulcx.ApprovalStatus())
	}
	if ulcx.Rules == nil || ulcx.Rules.MinRatio != 0.6 || ulcx.Rules.MinSeedDays != 2 {
		t.Fatalf("Upload.cx rules = %+v, want ratio 0.6 and 2 seed days", ulcx.Rules)
	}
	if ulcx.InviteRequirements == nil || ulcx.InviteRequirements.MinClass != "Seeder" {
		t.Fatalf("Upload.cx invite requirements = %+v", ulcx.InviteRequirements)
	}
	wantULCXGroups := []string{
		"Leech", "Parked", "User", "Seeder", "Collector", "Archivist", "Hoarder",
		"Sharer", "Provider", "Distributor", "Supplier", "Adept", "Master", "Veteran",
		"Champion", "Legend", "Network Affiliate", "Junior Uploader", "Uploader", "Trustee",
		"Internal", "Editor", "Torrent Moderator",
	}
	if len(ulcx.Groups) != len(wantULCXGroups) {
		t.Fatalf("Upload.cx groups = %d, want %d", len(ulcx.Groups), len(wantULCXGroups))
	}
	wantULCXStyles := map[string]GroupStyle{
		"Leech":             {Color: "#c07a3a", Icon: "fas fa-virus-covid"},
		"Parked":            {Icon: "fas fa-box-open-full"},
		"User":              {Color: "#9aa0a6", Icon: "fas fa-cube"},
		"Seeder":            {Color: "#4daf52", Icon: "fas fa-seedling"},
		"Collector":         {Color: "#4d96af", Icon: "fas fa-hand-holding-box"},
		"Archivist":         {Color: "#598bf7", Icon: "fas fa-file-zipper", Sparkle: true},
		"Hoarder":           {Color: "#595cf7", Icon: "fas fa-warehouse-full", Sparkle: true},
		"Sharer":            {Color: "#c9e127", Icon: "fas fa-person-walking-luggage"},
		"Provider":          {Color: "#91e127", Icon: "fas fa-forklift"},
		"Distributor":       {Color: "#59e127", Icon: "fas fa-truck-field", Sparkle: true},
		"Supplier":          {Color: "#2df833", Icon: "fas fa-ship", Sparkle: true},
		"Adept":             {Color: "#ed7b9c", Icon: "fas fa-scroll-old"},
		"Master":            {Color: "#f44d7f", Icon: "fas fa-graduation-cap", Sparkle: true},
		"Veteran":           {Color: "#db148b", Icon: "fas fa-helmet-safety", Sparkle: true},
		"Champion":          {Color: "#00b4f0", Icon: "fas fa-award", Sparkle: true},
		"Legend":            {Color: "#fb9c18", Icon: "fas fa-chess-queen-piece", Sparkle: true},
		"Network Affiliate": {Color: "#78bdbf", Icon: "fas fa-sitemap"},
		"Junior Uploader":   {Color: "#2ecc71", Icon: "fas fa-file-arrow-up"},
		"Uploader":          {Color: "#2ecca0", Icon: "fas fa-cloud-arrow-up", Sparkle: true},
		"Trustee":           {Color: "#bf55ec", Icon: "fas fa-badge-check", Sparkle: true},
		"Internal":          {Color: "#ffce2e", Icon: "fas fa-cards", Sparkle: true},
		"Editor":            {Color: "#159ab0", Icon: "fas fa-square-quote"},
		"Torrent Moderator": {Color: "#15b097", Icon: "fas fa-clipboard-list"},
	}
	for i, name := range wantULCXGroups {
		group := ulcx.Groups[i]
		if group.Name != name {
			t.Errorf("Upload.cx group %d = %q, want %q", i, group.Name, name)
		}
		if group.Style != wantULCXStyles[name] {
			t.Errorf("Upload.cx %s style = %+v, want %+v", name, group.Style, wantULCXStyles[name])
		}
		switch group.Name {
		case "Pruned", "Banned", "Disabled", "Validating":
			t.Errorf("Upload.cx account state included as class: %s", group.Name)
		}
	}
	if req := ulcx.Groups[3].Requirements; req.MinUploaded != "10 TiB" || req.MinRatio != 0.8 || req.MinAge != "1M" || req.MinSeedtime != "1M" || req.MinSeedSize != "5 TiB" {
		t.Errorf("Upload.cx Seeder requirements = %+v", req)
	}
	if req := ulcx.Groups[10].Requirements; req.MinRatio != 1.8 || req.MinAge != "1Y" || req.MinSeedtime != "4M" || req.MinUploads != 500 {
		t.Errorf("Upload.cx Supplier requirements = %+v", req)
	}
	if req := ulcx.Groups[15].Requirements; req.MinUploaded != "150 TiB" || req.MinRatio != 2.0 || req.MinAge != "2Y" || req.MinSeedtime != "6M" || req.MinSeedSize != "50 TiB" || req.MinUploads != 1000 {
		t.Errorf("Upload.cx Legend requirements = %+v", req)
	}

	ab, ok := r.TrackerByURL("https://animebytes.tv")
	if !ok {
		t.Fatal("animebytes.tv def not found")
	}
	if kind := r.APIKind(ab.URL, ab.Type); kind != "custom" {
		t.Fatalf("AnimeBytes APIKind = %q, want custom", kind)
	}
	if ab.API == nil || ab.API.Path != "/api/stats/personal" || ab.API.AuthMethod != "api_key_header" || ab.API.SuccessField != "success" || ab.API.SuccessValue != "true" {
		t.Fatalf("AnimeBytes API = %+v", ab.API)
	}
	if !ab.Scrape.DisableScraping || ab.ApprovalStatus() != ApprovalUnknown {
		t.Fatalf("AnimeBytes must be API-only and unapproved: scrape=%+v approval=%q", ab.Scrape, ab.ApprovalStatus())
	}
	if ab.Rules == nil || ab.Rules.MinRatio != 0.2 || ab.Rules.MinSeedHours != 72 || ab.Rules.Note == "" {
		t.Fatalf("AnimeBytes rules = %+v", ab.Rules)
	}
	if ab.InviteRequirements == nil || ab.InviteRequirements.MinClass != "User" {
		t.Fatalf("AnimeBytes invite requirements = %+v", ab.InviteRequirements)
	}
	wantABGroups := []string{
		"Aka-chan", "User", "Power User", "Elite", "Torrent Master", "Legend",
		"VIP", "Sensei", "Staff", "Editor", "Forum Staff", "App Reviewer",
		"Torrent Support", "First Line Support", "Community Celebrity",
	}
	if len(ab.Groups) != len(wantABGroups) {
		t.Fatalf("AnimeBytes groups = %d, want %d", len(ab.Groups), len(wantABGroups))
	}
	wantABColors := []string{"#a298e1", "#518fd6", "#2ac08c", "#7fae32", "#ff70ea", "#ff9752", "#ff473d", "#c36d50", "#ff1a1a"}
	for i, name := range wantABGroups {
		if ab.Groups[i].Name != name {
			t.Errorf("AnimeBytes group %d = %q, want %q", i, ab.Groups[i].Name, name)
		}
		if i < len(wantABColors) && ab.Groups[i].Style.Color != wantABColors[i] {
			t.Errorf("AnimeBytes %s color = %q, want %q", name, ab.Groups[i].Style.Color, wantABColors[i])
		}
		if ab.Groups[i].Style.Icon != "" {
			t.Errorf("AnimeBytes %s icon must be omitted: %q", name, ab.Groups[i].Style.Icon)
		}
	}
	if req := ab.Groups[1].Requirements; req.MinUploaded != "10.5 GB" || req.MinRatio != 0.5 || req.MinAge != "1W" {
		t.Errorf("AnimeBytes User requirements = %+v", req)
	}
	if req := ab.Groups[5].Requirements; req.MinUploaded != "1 TB" || req.MinRatio != 1.0 || req.MinAge != "6M" || req.MinUploads != 500 {
		t.Errorf("AnimeBytes Legend requirements = %+v", req)
	}

	btn, ok := r.TrackerByURL("https://broadcasthe.net")
	if !ok {
		t.Fatal("broadcasthe.net def not found")
	}
	if kind := r.APIKind(btn.URL, btn.Type); kind != "custom" {
		t.Fatalf("BTN APIKind = %q, want custom", kind)
	}
	if btn.API == nil || btn.API.Path != "/" || btn.API.AuthMethod != "api_key_json_rpc" || btn.API.JSONRPCMethod != "userInfo" {
		t.Fatalf("BTN API = %+v", btn.API)
	}
	if !btn.Scrape.DisableScraping || btn.ApprovalStatus() != ApprovalUnknown {
		t.Fatalf("BTN must be API-only and unapproved: scrape=%+v approval=%q", btn.Scrape, btn.ApprovalStatus())
	}
	if btn.Rules == nil || btn.Rules.MinRatio != 0 || btn.Rules.MinSeedDaysEpisode != 1 || btn.Rules.MinSeedDaysSeason != 5 || btn.Rules.Note == "" {
		t.Fatalf("BTN rules = %+v", btn.Rules)
	}
	wantBTNGroups := []string{
		"Sheep", "User", "Member", "Power User", "Extreme User", "Elite", "Guru",
		"Master", "Overlord", "Encoder", "VIP", "Torrent Celebrity", "Legend",
		"First-Line Support", "TV Technician", "Designer", "Junior Coder", "Moderator",
		"Developer", "Team Leader", "Administrator", "Lead Developer", "SysOp",
	}
	if len(btn.Groups) != len(wantBTNGroups) {
		t.Fatalf("BTN groups = %d, want %d", len(btn.Groups), len(wantBTNGroups))
	}
	for i, name := range wantBTNGroups {
		if btn.Groups[i].Name != name {
			t.Errorf("BTN group %d = %q, want %q", i, btn.Groups[i].Name, name)
		}
		if btn.Groups[i].Style.Color != "" || btn.Groups[i].Style.Icon != "" {
			t.Errorf("BTN %s style must be omitted: %+v", name, btn.Groups[i].Style)
		}
	}
	if req := btn.Groups[2].Requirements; req.MinTotalTransfer != "100 GB" || req.MinAge != "2W" || req.MinBonusPoints != 100000 || len(req.MinCounts) != 1 || req.MinCounts[0].Field != "snatched" || req.MinCounts[0].Count != 100 {
		t.Errorf("BTN Member requirements = %+v", req)
	}
	if req := btn.Groups[8].Requirements; req.MinTotalTransfer != "100 TB" || req.MinAge != "3Y" || req.MinUploads != 500 || req.MinBonusPoints != 250000000 {
		t.Errorf("BTN Overlord requirements = %+v", req)
	}
}
