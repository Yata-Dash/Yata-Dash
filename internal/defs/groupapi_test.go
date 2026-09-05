package defs

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// pgLadder is a trimmed copy of what PeerGarden's /api/user/groups actually
// served on 2026-09-04 — the four ladders, the requirement shapes, and the
// per-user "satisfied" flag that must never reach storage.
const pgLadder = `{
  "auto": [
    {"id":7,"title":"Weed","description":null,"requirements":[]},
    {"id":9,"title":"Seedling","description":null,"requirements":[
      {"type":"upload","value":53687091200,"satisfied":false}]},
    {"id":10,"title":"Sprout","description":null,"requirements":[
      {"type":"upload","value":536870912000,"satisfied":false},
      {"type":"age","value":2592000,"satisfied":false}]},
    {"id":14,"title":"Flowering","description":null,"requirements":[
      {"type":"upload","value":54975581388800,"satisfied":false},
      {"type":"comments","value":50,"satisfied":false},
      {"type":"posts","value":25,"satisfied":false},
      {"type":"age","value":31536000,"satisfied":false}]},
    {"id":16,"title":"Seed Vault","description":null,"requirements":[
      {"type":"avg_seedtime","value":5184000,"satisfied":false},
      {"type":"seedsize","value":10995116277760,"satisfied":false},
      {"type":"age","value":7776000,"satisfied":false}]}
  ],
  "upload": [
    {"id":17,"title":"Trainee Uploader","description":null,"requirements":[
      {"type":"uploads","value":10,"satisfied":false}]}
  ],
  "internal": [],
  "premium": [
    {"id":1,"title":"1337","perks":["15% Global freeleech","15% more upload"]}
  ]
}`

func traxarySpec() GroupAPISpec {
	return GroupAPISpec{
		Path:   "/api/user/groups",
		Ladder: "auto",
		Requirements: map[string]string{
			"upload":       "min_uploaded",
			"seedsize":     "min_seed_size",
			"age":          "min_age",
			"avg_seedtime": "min_seedtime",
			"uploads":      "min_uploads",
			"comments":     "comments",
			"posts":        "forum_posts",
		},
	}
}

func TestLadderFromAPI(t *testing.T) {
	got := LadderFromAPI([]byte(pgLadder), traxarySpec())
	if len(got) != 5 {
		t.Fatalf("ladder length = %d, want 5 (the auto ladder only)", len(got))
	}
	// Order is the API's own, lowest rung first — LadderIndex compares
	// positions, so a reordered ladder would invert promotions.
	if got[0].Name != "Weed" || got[4].Name != "Seed Vault" {
		t.Fatalf("ladder order = %q…%q, want Weed…Seed Vault", got[0].Name, got[4].Name)
	}

	// Raw values are written in the unit each destination field uses, inferred
	// from the field rather than declared by the def.
	if v := got[1].Requirements.MinUploaded; v != "50.00 GiB" {
		t.Errorf("Seedling min_uploaded = %q, want 50.00 GiB", v)
	}
	if v := got[2].Requirements.MinAge; v != "1M" {
		t.Errorf("Sprout min_age = %q, want 1M (2592000s)", v)
	}
	if v := got[4].Requirements.MinSeedtime; v != "2M" {
		t.Errorf("Seed Vault min_seedtime = %q, want 2M", v)
	}
	if v := got[4].Requirements.MinSeedSize; v != "10.00 TiB" {
		t.Errorf("Seed Vault min_seed_size = %q, want 10.00 TiB", v)
	}

	// A type with no GroupRequirements field of its own becomes a min_counts
	// entry against the canonical stat, in the API's own order.
	mc := got[3].Requirements.MinCounts
	if len(mc) != 2 {
		t.Fatalf("Flowering min_counts = %d entries, want 2", len(mc))
	}
	if mc[0].Field != "comments" || mc[0].Count != 50 {
		t.Errorf("min_counts[0] = %+v, want comments/50", mc[0])
	}
	if mc[1].Field != "forum_posts" || mc[1].Count != 25 {
		t.Errorf("min_counts[1] = %+v, want forum_posts/25", mc[1])
	}

	// A rank granted automatically has no thresholds, and that must stay
	// distinguishable from a rank whose thresholds we failed to read.
	if r := got[0].Requirements; r.MinUploaded != "" || len(r.MinCounts) != 0 {
		t.Errorf("Weed has requirements %+v, want none", r)
	}
}

// An unmapped requirement type is dropped rather than guessed at: a threshold
// Yata cannot measure would otherwise become a rung nobody can ever satisfy.
func TestLadderFromAPIDropsUnmappedRequirement(t *testing.T) {
	spec := traxarySpec()
	delete(spec.Requirements, "comments")
	got := LadderFromAPI([]byte(pgLadder), spec)
	mc := got[3].Requirements.MinCounts
	if len(mc) != 1 || mc[0].Field != "forum_posts" {
		t.Fatalf("min_counts = %+v, want forum_posts only", mc)
	}
}

// Selecting a ladder the response doesn't carry must read as "unknown", never
// as "this tracker has no ranks" — an empty ladder would overwrite a good one.
func TestLadderFromAPIMissingLadderIsNil(t *testing.T) {
	for _, name := range []string{"internal", "nope", ""} {
		spec := traxarySpec()
		spec.Ladder = name
		if got := LadderFromAPI([]byte(pgLadder), spec); got != nil {
			t.Errorf("ladder %q = %v, want nil", name, got)
		}
	}
}

// Perks arrive as bare strings on the premium ladder and (once the platform
// serves them) as objects elsewhere. Both have to land.
func TestLadderFromAPIPerkShapes(t *testing.T) {
	spec := traxarySpec()
	spec.Ladder = "premium"
	got := LadderFromAPI([]byte(pgLadder), spec)
	if len(got) != 1 || len(got[0].Perks) != 2 {
		t.Fatalf("premium ladder = %+v, want 1 group with 2 perks", got)
	}
	if got[0].Perks[0].Label != "15% Global freeleech" {
		t.Errorf("perk label = %q", got[0].Perks[0].Label)
	}

	const objectPerks = `{"auto":[{"title":"Sprout","perks":[{"icon":"fas fa-ticket","label":"2 vouchers/mo"}]}]}`
	obj := LadderFromAPI([]byte(objectPerks), traxarySpec())
	if len(obj) != 1 || len(obj[0].Perks) != 1 {
		t.Fatalf("object perks = %+v", obj)
	}
	if obj[0].Perks[0].Icon != "fas fa-ticket" || obj[0].Perks[0].Label != "2 vouchers/mo" {
		t.Errorf("perk = %+v", obj[0].Perks[0])
	}
}

// Style and perks are read when the platform serves them and simply absent
// otherwise, so adding them upstream needs no Yata change and no def edit.
func TestLadderFromAPIReadsStyleWhenServed(t *testing.T) {
	if got := LadderFromAPI([]byte(pgLadder), traxarySpec()); got[0].Style.Color != "" {
		t.Errorf("colour = %q, want empty — the platform serves none today", got[0].Style.Color)
	}
	const styled = `{"auto":[{"title":"Seed","color":"#a8e6a3","icon":"fa-solid fa-seedling","requirements":[]}]}`
	got := LadderFromAPI([]byte(styled), traxarySpec())
	if got[0].Style.Color != "#a8e6a3" || got[0].Style.Icon != "fa-solid fa-seedling" {
		t.Errorf("style = %+v", got[0].Style)
	}
}

// The per-user verdict must not reach storage: it flips as the user climbs, so
// hashing it would file a fresh "the tracker changed its rules" revision every
// time they crossed a threshold.
func TestCanonicalLadder(t *testing.T) {
	stripped, err := CanonicalLadder([]byte(pgLadder))
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	var v map[string][]map[string]any
	if err := json.Unmarshal(stripped, &v); err != nil {
		t.Fatalf("stripped payload is not valid JSON: %v", err)
	}
	for _, g := range v["auto"] {
		reqs, _ := g["requirements"].([]any)
		for _, r := range reqs {
			if _, present := r.(map[string]any)["satisfied"]; present {
				t.Fatalf("satisfied survived the projection in %v", g["title"])
			}
			if _, present := r.(map[string]any)["type"]; !present {
				t.Fatalf("the projection dropped the requirement type in %v", g["title"])
			}
		}
	}
	// The thresholds themselves must survive, or the stored revision is useless.
	if len(LadderFromAPI(stripped, traxarySpec())) != 5 {
		t.Fatal("projected payload no longer maps to a ladder")
	}
}

// Two responses differing only in the user's progress must hash the same, which
// is what makes a revision mean "the tracker changed its requirements".
func TestCanonicalLadderIsStableAcrossUserProgress(t *testing.T) {
	before, err := CanonicalLadder([]byte(pgLadder))
	if err != nil {
		t.Fatal(err)
	}
	promoted := []byte(`{"auto":[
	    {"id":7,"title":"Weed","description":null,"requirements":[]},
	    {"id":9,"title":"Seedling","description":null,"requirements":[
	      {"type":"upload","value":53687091200,"satisfied":true}]},
	    {"id":10,"title":"Sprout","description":null,"requirements":[
	      {"type":"upload","value":536870912000,"satisfied":true},
	      {"type":"age","value":2592000,"satisfied":true}]},
	    {"id":14,"title":"Flowering","description":null,"requirements":[
	      {"type":"upload","value":54975581388800,"satisfied":false},
	      {"type":"comments","value":50,"satisfied":true},
	      {"type":"posts","value":25,"satisfied":false},
	      {"type":"age","value":31536000,"satisfied":true}]},
	    {"id":16,"title":"Seed Vault","description":null,"requirements":[
	      {"type":"avg_seedtime","value":5184000,"satisfied":false},
	      {"type":"seedsize","value":10995116277760,"satisfied":true},
	      {"type":"age","value":7776000,"satisfied":true}]}
	  ],
	  "upload":[{"id":17,"title":"Trainee Uploader","description":null,"requirements":[
	      {"type":"uploads","value":10,"satisfied":true}]}],
	  "internal":[],
	  "premium":[{"id":1,"title":"1337","perks":["15% Global freeleech","15% more upload"]}]
	}`)
	after, err := CanonicalLadder(promoted)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("stripped payloads differ after the user climbed:\n%s\n%s", before, after)
	}
}

// A genuine change to the rules must still be visible after stripping.
func TestCanonicalLadderKeepsRealChanges(t *testing.T) {
	before, _ := CanonicalLadder([]byte(pgLadder))
	raised, _ := CanonicalLadder([]byte(
		`{"auto":[{"id":9,"title":"Seedling","requirements":[{"type":"upload","value":107374182400,"satisfied":false}]}]}`))
	if string(before) == string(raised) {
		t.Error("a raised threshold hashed the same as the old ladder")
	}
}

func TestLadderHasGroup(t *testing.T) {
	ladder := LadderFromAPI([]byte(pgLadder), traxarySpec())
	if !LadderHasGroup(ladder, "seed vault") {
		t.Error("known rank not found (match is case-insensitive)")
	}
	if LadderHasGroup(ladder, "Sapling") {
		t.Error("a rank absent from this ladder reported as present")
	}
}

// The projection is an allowlist by construction: only fields the structs above
// declare survive. A denylist would have kept whatever a /api/user route grows
// next — and hashed it, so a user field would look like the tracker rewriting
// its requirements.
func TestCanonicalLadderDropsUnknownFields(t *testing.T) {
	const withUserState = `{
	  "viewer_id": 4021,
	  "auto": [{
	    "id": 9, "title": "Seedling",
	    "joined_at": "2026-09-03T06:29:05Z",
	    "is_current": true,
	    "requirements": [
	      {"type":"upload","value":53687091200,"satisfied":true,"remaining":12345}
	    ]
	  }]
	}`
	out, err := CanonicalLadder([]byte(withUserState))
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"viewer_id", "joined_at", "is_current", "satisfied", "remaining"} {
		if strings.Contains(string(out), leaked) {
			t.Errorf("%q reached the store: %s", leaked, out)
		}
	}
	// The ladder itself must still be intact and still map.
	got := LadderFromAPI(out, traxarySpec())
	if len(got) != 1 || got[0].Name != "Seedling" || got[0].Requirements.MinUploaded != "50.00 GiB" {
		t.Errorf("projection damaged the ladder: %+v", got)
	}
}

// Two responses differing only in a per-user field must hash the same, which is
// what makes a stored revision mean "the tracker changed its requirements".
func TestCanonicalLadderIgnoresUnknownUserFields(t *testing.T) {
	base := `{"auto":[{"id":9,"title":"Seedling","requirements":[{"type":"upload","value":53687091200%s}]}]}`
	before, err := CanonicalLadder([]byte(fmt.Sprintf(base, "")))
	if err != nil {
		t.Fatal(err)
	}
	after, err := CanonicalLadder([]byte(fmt.Sprintf(base, `,"satisfied":true,"remaining":0`)))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("per-user fields changed the canonical form: %s vs %s", before, after)
	}
}

// A user endpoint growing a scalar beside its ladders must not take the feature
// down: decoding the whole object in one go fails on it, so ladders are decoded
// one key at a time.
func TestLadderSurvivesUnknownTopLevelShapes(t *testing.T) {
	const noisy = `{
	  "viewer_id": 4021,
	  "meta": {"generated_at": "2026-09-05T02:00:00Z"},
	  "flags": ["beta"],
	  "auto": [{"id":9,"title":"Seedling","requirements":[{"type":"upload","value":53687091200}]}]
	}`
	got := LadderFromAPI([]byte(noisy), traxarySpec())
	if len(got) != 1 || got[0].Name != "Seedling" {
		t.Fatalf("ladder = %+v, want Seedling alongside the noise", got)
	}
	out, err := CanonicalLadder([]byte(noisy))
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	for _, leaked := range []string{"viewer_id", "meta", "generated_at", "flags"} {
		if strings.Contains(string(out), leaked) {
			t.Errorf("%q reached the store: %s", leaked, out)
		}
	}
}

// A response with no ladder at all is an error, not an empty ladder — storing
// one would read downstream as a tracker that abolished its ranks.
func TestCanonicalLadderRejectsResponseWithNoLadders(t *testing.T) {
	if _, err := CanonicalLadder([]byte(`{"error":"unauthorised"}`)); err == nil {
		t.Error("a response carrying no ladder was accepted")
	}
}
