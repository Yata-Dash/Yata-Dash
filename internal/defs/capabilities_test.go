package defs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// capsRegistry writes a minimal defs dir with one type and one tracker, so the
// merge rules can be exercised without depending on the shipped defs.
func capsRegistry(t *testing.T, typeJSON, trackerJSON string) *Registry {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "types", "fam.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trackers", "t.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, iss := range reg.Issues() {
		t.Fatalf("unexpected load issue %s: %s", iss.File, iss.Error)
	}
	return reg
}

const capsType = `{"schema_version":1,"key":"fam","label":"Family",
	"api":{"kind":"unit3d"},
	"capabilities":{"api_stats":["uploaded","downloaded","ratio","bonus_points"]}}`

// TestCapabilityDeltas: a tracker states how it differs from its software's
// baseline, rather than restating the whole list — so a fork that returns one
// extra field says one thing, and a baseline change reaches everyone who
// didn't opt out of it.
func TestCapabilityDeltas(t *testing.T) {
	cases := []struct {
		name    string
		capsRaw string
		want    []string
	}{
		{"inherits the baseline", ``,
			[]string{"bonus_points", "downloaded", "ratio", "uploaded"}},
		{"adds", `,"capabilities":{"api_stats_add":["seed_size"]}`,
			[]string{"bonus_points", "downloaded", "ratio", "seed_size", "uploaded"}},
		{"omits", `,"capabilities":{"api_stats_omit":["bonus_points"]}`,
			[]string{"downloaded", "ratio", "uploaded"}},
		{"adds and omits together", `,"capabilities":{"api_stats_add":["adoptions"],"api_stats_omit":["ratio"]}`,
			[]string{"adoptions", "bonus_points", "downloaded", "uploaded"}},
		{"a full list replaces the baseline", `,"capabilities":{"api_stats":["username"]}`,
			[]string{"username"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := capsRegistry(t, capsType, `{"schema_version":1,"key":"t","name":"T",
				"url":"https://t.example","type":"fam"`+tc.capsRaw+`}`)
			got := reg.ResolveCapabilities("https://t.example", "").APIStats
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("api stats = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCapabilityDeltasDoNotMutateTheType: the baseline is shared by every
// member of a family, so one tracker's omit must not remove the field for its
// siblings — the same trap the custom-API merge has a test for.
func TestCapabilityDeltasDoNotMutateTheType(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "types", "fam.json"), []byte(capsType), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tr := range []struct{ key, url, caps string }{
		{"a", "https://a.example", `,"capabilities":{"api_stats_omit":["ratio"]}`},
		{"b", "https://b.example", ``},
	} {
		body := `{"schema_version":1,"key":"` + tr.key + `","name":"` + tr.key +
			`","url":"` + tr.url + `","type":"fam"` + tr.caps + `}`
		if err := os.WriteFile(filepath.Join(dir, "trackers", tr.key+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Resolve the one that omits FIRST, then its sibling.
	if got := reg.ResolveCapabilities("https://a.example", "").APIStats; sortedContains(got, "ratio") {
		t.Fatalf("the omitting tracker should not report ratio: %v", got)
	}
	if got := reg.ResolveCapabilities("https://b.example", "").APIStats; !sortedContains(got, "ratio") {
		t.Fatalf("a sibling lost ratio to the other tracker's omit: %v", got)
	}
}

// TestCapabilitiesDerivedFromCustomAPI: a def that describes its own API needs
// no declaration — the field map already says what it returns, including the
// values computed from byte fields rather than mapped.
func TestCapabilitiesDerivedFromCustomAPI(t *testing.T) {
	typeJSON := `{"schema_version":1,"key":"fam","label":"Family","api":{"kind":"custom"}}`
	trackerJSON := `{"schema_version":1,"key":"t","name":"T","url":"https://t.example","type":"fam",
		"api":{"path":"/api","auth_method":"api_key_query",
			"field_map":{"resp.User":"username","resp.Cls":"group"},
			"byte_fields":{"resp.Up":"uploaded","resp.Down":"downloaded"},
			"bool_fields":{"resp.Mail":"unread_mail"},
			"buffer_from_bytes":true,"ratio_from_bytes":true}}`
	reg := capsRegistry(t, typeJSON, trackerJSON)
	caps := reg.ResolveCapabilities("https://t.example", "")

	if caps.Derived != true {
		t.Error("a def with its own field map should be derived, not declared")
	}
	want := []string{"buffer", "downloaded", "group", "ratio", "unread_mail", "uploaded", "username"}
	if !reflect.DeepEqual(caps.APIStats, want) {
		t.Errorf("derived stats = %v, want %v", caps.APIStats, want)
	}
}

// TestScrapeCapabilityFollowsPolicy: a tracker whose operator forbids scraping
// reports nothing scrapeable, however rich its label map is — otherwise the
// summary would promise stats Yata will never fetch.
func TestScrapeCapabilityFollowsPolicy(t *testing.T) {
	typeJSON := `{"schema_version":1,"key":"fam","label":"Family","api":{"kind":"unit3d"},
		"capabilities":{"api_stats":["uploaded"]},
		"scrape":{"labels":{"seeding size":"seed_size"},
			"presence_flags":{"unread_mail":{"link_suffix":"/c","marker":"svg"}}}}`

	allowed := capsRegistry(t, typeJSON,
		`{"schema_version":1,"key":"t","name":"T","url":"https://t.example","type":"fam"}`)
	caps := allowed.ResolveCapabilities("https://t.example", "")
	if !caps.ScrapePossible {
		t.Fatal("scraping should be possible when nothing forbids it")
	}
	// active_event is always present for a scrapeable tracker: the banner
	// extraction runs on every scraped page, independent of the label map.
	if !reflect.DeepEqual(caps.ScrapeStats, []string{"active_event", "seed_size", "unread_mail"}) {
		t.Errorf("scrape stats = %v", caps.ScrapeStats)
	}
	if caps.Notables()["active_events"] != "scrape" {
		t.Error("a scrapeable tracker should report events via the scraped banner")
	}
	if caps.FieldSource("seed_size") != "scrape" || caps.FieldSource("uploaded") != "api" {
		t.Error("field sources should distinguish the two routes")
	}
	if caps.FieldSource("adoptions") != "" {
		t.Error("an unobtainable field must report no source")
	}

	forbidden := capsRegistry(t, typeJSON,
		`{"schema_version":1,"key":"t","name":"T","url":"https://t.example","type":"fam",
		  "scrape":{"disable_scraping":true}}`)
	caps = forbidden.ResolveCapabilities("https://t.example", "")
	if caps.ScrapePossible || len(caps.ScrapeStats) != 0 {
		t.Errorf("an operator's disable_scraping must empty the scrape set, got %v", caps.ScrapeStats)
	}
}

// TestLadderStatsAndSummary: the denominator comes from the tracker's own
// ladder, including either/or routes, and excludes appointed ranks.
func TestLadderStatsAndSummary(t *testing.T) {
	typeJSON := `{"schema_version":1,"key":"fam","label":"Family","api":{"kind":"unit3d"},
		"capabilities":{"api_stats":["uploaded","ratio","join_date"]}}`
	trackerJSON := `{"schema_version":1,"key":"t","name":"T","url":"https://t.example","type":"fam",
		"scrape":{"disable_scraping":true},
		"groups":[
			{"name":"User","requirements":{}},
			{"name":"Power","requirements":{"min_uploaded":"1 TiB","min_ratio":1.0,
				"min_bonus_points":25000,"min_age":"1M",
				"any_of":[{"min_uploads":5},{"min_adoptions":10}]}},
			{"name":"Legend","requirements":{"description":"Appointed by staff."}}
		]}`
	reg := capsRegistry(t, typeJSON, trackerJSON)
	td, _ := reg.Tracker("t")

	want := []string{"adoptions", "bonus_points", "join_date", "ratio", "uploaded", "uploads_approved"}
	if got := LadderStats(td); !reflect.DeepEqual(got, want) {
		t.Fatalf("ladder stats = %v, want %v", got, want)
	}

	sum := reg.ResolveCapabilities("https://t.example", "").Summarise(td)
	// The Anthelion shape exactly: 3 of 6 before the API grew.
	if len(sum.MetAPI) != 3 || len(sum.Required) != 6 {
		t.Errorf("coverage = %d of %d, want 3 of 6 (met: %v)", len(sum.MetAPI), len(sum.Required), sum.MetAPI)
	}
	if len(sum.Missing) != 3 {
		t.Errorf("missing = %v, want three", sum.Missing)
	}
	// Scraping is forbidden here, so it can add nothing.
	if len(sum.MetScrape) != len(sum.MetAPI) {
		t.Errorf("scrape coverage should equal API coverage when scraping is off")
	}
}

// TestLadderStatsIgnoresMonthlyUploads: no live stat backs it anywhere in
// Yata, so counting it would mark a tracker short for a gap on our side.
func TestLadderStatsIgnoresMonthlyUploads(t *testing.T) {
	td := TrackerDef{Groups: []GroupDef{
		{Name: "Uploader", Requirements: GroupRequirements{MinMonthlyUploads: 5, MinRatio: 1}},
	}}
	got := LadderStats(td)
	if !reflect.DeepEqual(got, []string{"ratio"}) {
		t.Errorf("ladder stats = %v, want just ratio", got)
	}
}

// TestRequiredFieldsAreNotAPIStats: api.required_fields already means "the
// user must supply this because the API doesn't", so a field listed there must
// never also count as an API capability. Deriving it keeps the two from being
// maintained separately and drifting apart.
func TestRequiredFieldsAreNotAPIStats(t *testing.T) {
	reg := capsRegistry(t, capsType, `{"schema_version":1,"key":"t","name":"T",
		"url":"https://t.example","type":"fam",
		"capabilities":{"api_stats_add":["join_date"]},
		"api":{"required_fields":["join_date"]}}`)
	caps := reg.ResolveCapabilities("https://t.example", "")
	if sortedContains(caps.APIStats, "join_date") {
		t.Errorf("a required field must not count as an API stat: %v", caps.APIStats)
	}
}

// TestJoinDateNeverCountsAsMissing: a tracker whose API omits the join date
// declares it in api.required_fields, which makes it a required input at
// setup — so account age is trackable either way. Counting it as missing
// marked trackers short for something the user had already handled.
func TestJoinDateNeverCountsAsMissing(t *testing.T) {
	typeJSON := `{"schema_version":1,"key":"fam","label":"Family","api":{"kind":"unit3d"},
		"capabilities":{"api_stats":["uploaded","ratio"]}}`
	trackerJSON := `{"schema_version":1,"key":"t","name":"T","url":"https://t.example","type":"fam",
		"scrape":{"disable_scraping":true},
		"api":{"required_fields":["join_date"]},
		"groups":[{"name":"Power","requirements":{
			"min_uploaded":"1 TiB","min_ratio":1.0,"min_age":"1M","min_bonus_points":100}}]}`
	reg := capsRegistry(t, typeJSON, trackerJSON)
	td, _ := reg.Tracker("t")
	sum := reg.ResolveCapabilities("https://t.example", "").Summarise(td)

	if sortedContains(sum.Missing, "join_date") {
		t.Errorf("join_date must not be reported missing: %v", sum.Missing)
	}
	// It still counts toward the ladder — the requirement is real and IS
	// trackable, so dropping it from the denominator would understate the
	// tracker instead.
	if !sortedContains(sum.Required, "join_date") {
		t.Errorf("join_date should stay in the requirement set: %v", sum.Required)
	}
	if len(sum.MetAPI) != 3 || len(sum.Required) != 4 {
		t.Errorf("coverage = %d of %d, want 3 of 4 (only bonus_points missing)",
			len(sum.MetAPI), len(sum.Required))
	}
	if !reflect.DeepEqual(sum.Missing, []string{"bonus_points"}) {
		t.Errorf("missing = %v, want just bonus_points", sum.Missing)
	}
}
