package defs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAPICookieNameOverrideChain verifies the resolution order for
// gazelle_json_cookie's session-cookie name: a tracker def's own
// api.cookie_name wins over the type default, which wins over the
// hardcoded "session" fallback. No shipped tracker currently needs the
// per-tracker override (GreatPosterWall turned out to use the type default
// "session", not PHPSESSID, despite first appearances), but the mechanism
// exists for the next Gazelle fork that genuinely does.
func TestAPICookieNameOverrideChain(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	typeJSON := `{"schema_version":1,"key":"gazelle_json_cookie","label":"Gazelle JSON API (session cookie)",
		"api":{"kind":"gazelle_json_cookie","cookie_name":"session","required_fields":["session_cookie"]}}`
	if err := os.WriteFile(filepath.Join(dir, "types", "gazelle_json_cookie.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultJSON := `{"schema_version":1,"key":"usesdefault","name":"Uses Default","abbr":"UD",
		"url":"https://usesdefault.example","type":"gazelle_json_cookie"}`
	if err := os.WriteFile(filepath.Join(dir, "trackers", "usesdefault.json"), []byte(defaultJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	overrideJSON := `{"schema_version":1,"key":"overrides","name":"Overrides","abbr":"OV",
		"url":"https://overrides.example","type":"gazelle_json_cookie",
		"api":{"cookie_name":"PHPSESSID"}}`
	if err := os.WriteFile(filepath.Join(dir, "trackers", "overrides.json"), []byte(overrideJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if issues := r.Issues(); len(issues) > 0 {
		t.Fatalf("load issues: %+v", issues)
	}

	if kind := r.APIKind("https://overrides.example", "gazelle_json_cookie"); kind != "gazelle_json_cookie" {
		t.Fatalf("a cookie_name-only api block must not force kind=custom, got %q", kind)
	}
	if name := r.APICookieName("https://usesdefault.example", "gazelle_json_cookie"); name != "session" {
		t.Errorf("default cookie name = %q, want session (type default)", name)
	}
	if name := r.APICookieName("https://overrides.example", "gazelle_json_cookie"); name != "PHPSESSID" {
		t.Errorf("override cookie name = %q, want PHPSESSID (tracker override)", name)
	}
	if name := r.APICookieName("https://unknown.example", "gazelle_json_cookie"); name != "session" {
		t.Errorf("unknown tracker cookie name = %q, want session (type default, no tracker def)", name)
	}
}
