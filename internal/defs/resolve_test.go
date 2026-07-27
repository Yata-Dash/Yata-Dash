package defs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveCustomAPISharedType: Anthelion and Nebulance run the same
// software from the same developers, so their endpoint, auth and field map
// live on the type. Each def supplies only what genuinely differs — the path
// through that site's own settings UI to find an API key.
func TestResolveCustomAPISharedType(t *testing.T) {
	reg, err := Load("../../defs")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	const wantPath = "/api.php?action=user&method=getuserinfo&type=username&user={username}"

	for _, tc := range []struct{ key, url string }{
		{"anthelion", "https://anthelion.me"},
		{"nebulance", "https://nebulance.io"},
	} {
		api := reg.ResolveCustomAPI(tc.url, "")
		if api == nil {
			t.Fatalf("%s: no custom API resolved", tc.key)
		}
		if api.Path != wantPath {
			t.Errorf("%s: path = %q, want the shared family path", tc.key, api.Path)
		}
		// The parameter rename ANT asked for. Both sites accept "api_key";
		// only ANT ever accepted the older "apikey", so the shared spelling
		// is the one that works for the whole family.
		if api.APIKeyParam != "api_key" {
			t.Errorf("%s: api_key_param = %q, want api_key", tc.key, api.APIKeyParam)
		}
		if api.AuthMethod != "api_key_query" {
			t.Errorf("%s: auth_method = %q", tc.key, api.AuthMethod)
		}
		if !api.RatioFromBytes || !api.BufferFromBytes {
			t.Errorf("%s: derived ratio/buffer flags did not survive the merge", tc.key)
		}
		// Inherited from the type, not restated per tracker.
		if api.FieldMap["response.Orbs"] != "bonus_points" {
			t.Errorf("%s: family field map not inherited", tc.key)
		}
		if api.ByteFields["response.SeedSize"] != "seed_size" {
			t.Errorf("%s: family byte fields not inherited", tc.key)
		}
		// ...but the key hint is the tracker's own.
		if api.APIKeyHint == "" {
			t.Errorf("%s: tracker-level API key hint was lost in the merge", tc.key)
		}
		if reg.APIKind(tc.url, "") != "custom" {
			t.Errorf("%s: should fetch through the custom fetcher", tc.key)
		}
	}

	// The two hints must differ — each names a different site's settings page.
	ant := reg.ResolveCustomAPI("https://anthelion.me", "")
	nbl := reg.ResolveCustomAPI("https://nebulance.io", "")
	if ant.APIKeyHint == nbl.APIKeyHint {
		t.Error("both trackers resolved to the same key hint; the tracker-level override is not applying")
	}
}

// TestResolveCustomAPIMerge covers the merge rules directly: a tracker block
// overrides scalars it sets, adds to maps rather than replacing them, and
// leaves everything it omits alone.
func TestResolveCustomAPIMerge(t *testing.T) {
	base := &CustomAPI{
		Path:            "/type/path",
		AuthMethod:      "api_key_query",
		APIKeyParam:     "api_key",
		APIKeyHint:      "type hint",
		RatioFromBytes:  true,
		FieldMap:        map[string]string{"a": "one", "b": "two"},
		ByteFields:      map[string]string{"up": "uploaded"},
		BufferFromBytes: false,
	}
	over := &CustomAPI{
		APIKeyHint:      "tracker hint",
		FieldMap:        map[string]string{"b": "TWO", "c": "three"},
		BufferFromBytes: true,
	}
	merged := *base
	mergeCustomAPI(&merged, over)

	if merged.Path != "/type/path" {
		t.Errorf("path = %q, want the type's (the tracker set none)", merged.Path)
	}
	if merged.APIKeyHint != "tracker hint" {
		t.Errorf("hint = %q, want the tracker's override", merged.APIKeyHint)
	}
	if merged.FieldMap["a"] != "one" {
		t.Error("an inherited mapping was dropped")
	}
	if merged.FieldMap["b"] != "TWO" {
		t.Error("the tracker's mapping should win on collision")
	}
	if merged.FieldMap["c"] != "three" {
		t.Error("a tracker-only mapping was dropped")
	}
	if merged.ByteFields["up"] != "uploaded" {
		t.Error("inherited byte fields were lost")
	}
	if !merged.RatioFromBytes || !merged.BufferFromBytes {
		t.Error("boolean flags should be the union of both levels")
	}
	// The base must not be mutated — it is the shared type definition, and
	// corrupting it would leak one tracker's overrides into its siblings.
	if base.APIKeyHint != "type hint" || len(base.FieldMap) != 2 {
		t.Error("merging modified the type-level definition in place")
	}
}

// TestRetiredGazelleType: the old "gazelle" type was only ever Anthelion's
// api.php grammar, adopted as a generic base before we learned that every
// Gazelle fork invents its own. It is gone, and its fetcher kind with it —
// this guards against either being reintroduced by habit.
func TestRetiredGazelleType(t *testing.T) {
	reg, err := Load("../../defs")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Type("gazelle"); ok {
		t.Error("the retired gazelle type is still registered")
	}
	for _, tt := range reg.Types() {
		if tt.API.Kind == "gazelle" {
			t.Errorf("type %q still uses the removed gazelle fetcher kind", tt.Key)
		}
	}
	for _, td := range reg.Trackers() {
		if td.Type == "gazelle" {
			t.Errorf("tracker def %q still points at the retired type", td.Key)
		}
	}
	// The kind is no longer accepted by validation either.
	err = validateType(TypeDef{Key: "x", Label: "X", API: TypeAPI{Kind: "gazelle"}})
	if err == nil {
		t.Error("validateType should reject the removed gazelle kind")
	}
}

// TestUnknownFieldIsReportedNotSwallowed: a misspelled key makes its whole
// section vanish while the def still loads looking healthy — the tracker then
// collects nothing and nothing says why. The def must still load (a def
// written for a newer Yata shouldn't be rejected), but the ignored key has to
// be reported.
func TestUnknownFieldIsReportedNotSwallowed(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// "filed_map" is the realistic typo: one transposition away from correct.
	typeJSON := `{"schema_version":1,"key":"fam","label":"Family",
		"api":{"kind":"custom"},
		"custom_api":{"path":"/api.php","filed_map":{"a":"username"}}}`
	if err := os.WriteFile(filepath.Join(dir, "types", "fam.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	trackerJSON := `{"schema_version":1,"key":"t","name":"T",
		"url":"https://t.example","type":"fam"}`
	if err := os.WriteFile(filepath.Join(dir, "trackers", "t.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The def still loads — tolerance is the point.
	if _, ok := reg.Type("fam"); !ok {
		t.Fatal("the type should still load despite the unrecognised key")
	}
	var warned bool
	for _, iss := range reg.Issues() {
		if iss.File == "fam.json" && iss.Warning && strings.Contains(iss.Error, "filed_map") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("the ignored key was not reported; issues = %+v", reg.Issues())
	}
	// A clean defs directory must stay silent, or the warning is just noise.
	clean, err := Load("../../defs")
	if err != nil {
		t.Fatal(err)
	}
	for _, iss := range clean.Issues() {
		t.Errorf("shipped defs should load without issues, got %s: %s", iss.File, iss.Error)
	}
}
