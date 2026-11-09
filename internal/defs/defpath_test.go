package defs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A def path is appended to the tracker URL by concatenation, so one that does
// not begin with a single "/" can name a different host — and the request
// carries the tracker's API key or session cookie there.
var hostChangingPaths = []string{
	"@evil.example/api/user", // https://tracker.example@evil.example/…
	".evil.example/api/user", // https://tracker.example.evil.example/…
	"//evil.example/api",     // how a URL names a different host
	"api/user",               // https://tracker.exampleapi/user
	":8443/api",              // a different port, a different service
}

func TestValidateDefPathsRefusesHostChangingPaths(t *testing.T) {
	for _, p := range hostChangingPaths {
		err := validateDefPaths(map[string]string{"api.path": p})
		if err == nil {
			t.Errorf("path %q was accepted", p)
			continue
		}
		if !strings.Contains(err.Error(), "api.path") {
			t.Errorf("error %q does not name the field", err)
		}
	}
}

func TestValidateDefPathsAcceptsRealPaths(t *testing.T) {
	for _, p := range []string{
		"", // absent: optional fields stay optional
		"/",
		"/api/user",
		"/users/{username}",
		"/user.php?id={id}",
		"/api.php?action=user&method=getuserinfo&type=username&user={username}",
	} {
		if err := validateDefPaths(map[string]string{"api.path": p}); err != nil {
			t.Errorf("path %q = %v, want nil", p, err)
		}
	}
}

// Every path-bearing field on both def kinds is checked, and a def that fails
// is skipped and reported, like any other invalid def.
func TestDefsWithHostChangingPathsAreSkipped(t *testing.T) {
	const goodType = `{"key":"u3","label":"U3","api":{"kind":"unit3d"}}`
	cases := []struct {
		name, typeJSON, trackerJSON, field string
	}{
		{"tracker api.path",
			`{"key":"c","label":"C","api":{"kind":"custom"}}`,
			`{"key":"t","name":"T","url":"https://t.example","type":"c","api":{"path":"@evil.example/api","auth_method":"api_key_header"}}`,
			"api.path"},
		{"tracker extended_stats.path", goodType,
			`{"key":"t","name":"T","url":"https://t.example","type":"u3","extended_stats":{"path":".evil.example/stats"}}`,
			"extended_stats.path"},
		{"tracker events.path", goodType,
			`{"key":"t","name":"T","url":"https://t.example","type":"u3","events":{"path":"//evil.example/ev","active_field":"on"}}`,
			"events.path"},
		{"tracker scrape.profile_path", goodType,
			`{"key":"t","name":"T","url":"https://t.example","type":"u3","scrape":{"profile_path":"@evil.example/users/{username}"}}`,
			"scrape.profile_path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := loadDefDir(t, tc.typeJSON, tc.trackerJSON)
			if _, ok := r.Tracker("t"); ok {
				t.Fatal("a def with a host-changing path loaded")
			}
			if !hasIssue(r, "t.json", tc.field) {
				t.Errorf("no load issue naming %s: %+v", tc.field, r.Issues())
			}
		})
	}

	typeCases := []struct{ name, typeJSON, field string }{
		{"type custom_api.path",
			`{"key":"x","label":"X","api":{"kind":"custom"},"custom_api":{"path":"@evil.example/api","auth_method":"api_key_header"}}`,
			"custom_api.path"},
		{"type group_api.path",
			`{"key":"x","label":"X","api":{"kind":"unit3d"},"group_api":{"path":".evil.example/groups","ladder":"auto"}}`,
			"group_api.path"},
		{"type scrape.profile_path",
			`{"key":"x","label":"X","api":{"kind":"unit3d"},"scrape":{"profile_path":"users/{username}"}}`,
			"scrape.profile_path"},
	}
	for _, tc := range typeCases {
		t.Run(tc.name, func(t *testing.T) {
			r := loadDefDir(t, tc.typeJSON, "")
			if _, ok := r.Type("x"); ok {
				t.Fatal("a type with a host-changing path loaded")
			}
			if !hasIssue(r, "x.json", tc.field) {
				t.Errorf("no load issue naming %s: %+v", tc.field, r.Issues())
			}
		})
	}
}

func loadDefDir(t *testing.T, typeJSON, trackerJSON string) *Registry {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	key := "x"
	if trackerJSON != "" {
		// The tracker cases name their type in the JSON; file name is cosmetic.
		key = "type"
		if err := os.WriteFile(filepath.Join(dir, "trackers", "t.json"), []byte(trackerJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "types", key+".json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

func hasIssue(r *Registry, file, field string) bool {
	for _, is := range r.Issues() {
		if is.File == file && !is.Warning && strings.Contains(is.Error, field) {
			return true
		}
	}
	return false
}
