package fetch

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// hunoProfile is the documented HUNO GET /api/profile response shape —
// a {success, data} envelope with per-bracket seed-division counts.
const hunoProfile = `{
  "success": true,
  "data": {
    "username": "hawke",
    "group": "Targaryen",
    "member_since": "2022-01-01T00:00:00+00:00",
    "uploaded": 1099511627776,
    "downloaded": 549755813888,
    "ratio": %s,
    "buffer": 549755813888,
    "hunos": 1500,
    "active_seeds": 42,
    "active_leeches": 3,
    "hit_and_runs": 0,
    "seed_divisions": {
      "vanguard": 10, "squire": 25, "knight": 50,
      "champion": 100, "legend": 5, "guardian": 3
    },
    "warnings": 0,
    "can_upload": true
  },
  "message": "ok"
}`

// customRegistry writes a minimal defs dir (custom type + one tracker def
// pointing at baseURL with the HUNO api block) and loads a Registry from it.
func customRegistry(t *testing.T, baseURL string) *defs.Registry {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	typeJSON := `{"schema_version":1,"key":"custom","label":"Custom API",
		"api":{"kind":"custom","required_fields":["join_date"]},
		"scrape":{"skip_html_scrape":true}}`
	trackerJSON := fmt.Sprintf(`{
		"schema_version":1,"key":"huno","name":"Hawke-uno","abbr":"HUNO",
		"url":%q,"type":"custom",
		"api":{
			"path":"/api/profile","auth_method":"api_key_header",
			"field_map":{
				"data.username":"username","data.group":"group",
				"data.member_since":"join_date","data.ratio":"ratio",
				"data.hunos":"bonus_points","data.active_seeds":"seeding",
				"data.active_leeches":"leeching","data.hit_and_runs":"hit_and_runs",
				"data.warnings":"warnings",
				"data.seed_divisions.vanguard":"vanguard_seeds",
				"data.seed_divisions.squire":"squire_seeds",
				"data.seed_divisions.knight":"knight_seeds",
				"data.seed_divisions.champion":"champion_seeds",
				"data.seed_divisions.guardian":"guardian_seeds",
				"data.seed_divisions.legend":"legend_seeds"
			},
			"byte_fields":{
				"data.uploaded":"uploaded","data.downloaded":"downloaded",
				"data.buffer":"buffer"
			}
		}
	}`, baseURL)
	if err := os.WriteFile(filepath.Join(dir, "types", "custom.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trackers", "huno.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := defs.Load(dir)
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	return reg
}

// TestFetchCustomHUNOShape exercises the custom fetcher against the HUNO
// /api/profile envelope: Bearer auth, dot-path field mapping into a nested
// envelope, byte-field conversion, seed-division counts, and the string
// normalisations (ISO join_date → date only).
func TestFetchCustomHUNOShape(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/profile" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, hunoProfile, "2.0")
	}))
	defer ts.Close()

	c := NewClient(customRegistry(t, ts.URL), "")
	data, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "custom", APIKey: "sekrit"})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	if gotAuth != "Bearer sekrit" {
		t.Errorf("auth header = %q, want Bearer token", gotAuth)
	}

	want := map[string]any{
		"username":        "hawke",
		"group":           "Targaryen",
		"join_date":       "2022-01-01", // ISO datetime trimmed to date
		"uploaded":        "1.00 TiB",
		"downloaded":      "512.00 GiB",
		"buffer":          "512.00 GiB",
		"ratio":           2.0,
		"bonus_points":    1500.0,
		"seeding":         42,
		"leeching":        3,
		"hit_and_runs":    0,
		"warnings":        0,
		"vanguard_seeds":  10,
		"squire_seeds":    25,
		"knight_seeds":    50,
		"champion_seeds":  100,
		"guardian_seeds":  3,
		"legend_seeds":    5,
	}
	for k, w := range want {
		if got, ok := data[k]; !ok {
			t.Errorf("missing field %q", k)
		} else if got != w {
			t.Errorf("%s = %#v, want %#v", k, got, w)
		}
	}
}

// retroflixMe is the documented RetroFlix GET /api/me response shape — a flat
// object with a numeric membership "class", byte up/down, an unread PM count,
// and a stringy seed_bonus.
const retroflixMe = `{
  "ratio": 6.98,
  "unread_private_message_count": %s,
  "id": 17594,
  "username": "MysteryZiLLA",
  "email": "user@example.com",
  "created_at": "2026-02-19T19:42:20+00:00",
  "class": 3,
  "uploaded": 1116908837235,
  "downloaded": 159913546812,
  "seed_time": 257125525,
  "leech_time": 723055,
  "snatched_count": 20,
  "average_seed_time": 8285514,
  "hit_and_run_count": 0,
  "title": "",
  "invites": 3,
  "seed_bonus": "172236.2"
}`

// retroflixRegistry writes a custom type + a RetroFlix tracker def exercising
// the class_map, bool_fields, byte_fields and buffer_from_bytes mechanisms.
func retroflixRegistry(t *testing.T, baseURL string) *defs.Registry {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	typeJSON := `{"schema_version":1,"key":"custom","label":"Custom API",
		"api":{"kind":"custom","required_fields":["join_date"]},
		"scrape":{"skip_html_scrape":true}}`
	trackerJSON := fmt.Sprintf(`{
		"schema_version":1,"key":"retroflix","name":"RetroFlix","abbr":"RF",
		"url":%q,"type":"custom",
		"api":{
			"path":"/api/me","auth_method":"api_key_header",
			"field_map":{
				"username":"username","id":"user_id","ratio":"ratio",
				"created_at":"join_date","seed_bonus":"bonus_points",
				"snatched_count":"snatched","average_seed_time":"avg_seed_time",
				"seed_time":"total_seedtime","hit_and_run_count":"hit_and_runs",
				"invites":"invites"
			},
			"byte_fields":{"uploaded":"uploaded","downloaded":"downloaded"},
			"buffer_from_bytes":true,
			"bool_fields":{"unread_private_message_count":"unread_mail"},
			"class_field":"class",
			"class_map":{"2":"Movie Lover","3":"Cinema Addicted","4":"Film Critic"}
		}
	}`, baseURL)
	if err := os.WriteFile(filepath.Join(dir, "types", "custom.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trackers", "retroflix.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := defs.Load(dir)
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	return reg
}

// TestFetchCustomRetroflix exercises the RetroFlix /api/me shape: Bearer auth,
// numeric class → group name (class_map), unread count → unread_mail flag
// (bool_fields), byte → size + computed buffer, and ISO join_date trimming.
func TestFetchCustomRetroflix(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, retroflixMe, "2") // 2 unread PMs → unread_mail "true"
	}))
	defer ts.Close()

	c := NewClient(retroflixRegistry(t, ts.URL), "")
	data, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "custom", APIKey: "sekrit"})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	if gotAuth != "Bearer sekrit" {
		t.Errorf("auth header = %q, want Bearer token", gotAuth)
	}
	want := map[string]any{
		"username":      "MysteryZiLLA",
		"group":         "Cinema Addicted", // class 3 via class_map
		"unread_mail":   "true",            // count 2 → truthy
		"join_date":     "2026-02-19",      // ISO trimmed
		"uploaded":      "1.02 TiB",
		"downloaded":    "148.93 GiB",
		"buffer":        "891.27 GiB", // uploaded − downloaded
		"ratio":         6.98,
		"bonus_points":  "172236.2",
		"snatched":      20,
		"hit_and_runs":  0,
		"invites":       3,
		"avg_seed_time": 8285514,
		"total_seedtime": 257125525,
	}
	for k, w := range want {
		if got, ok := data[k]; !ok {
			t.Errorf("missing field %q", k)
		} else if got != w {
			t.Errorf("%s = %#v, want %#v", k, got, w)
		}
	}
}

// TestFetchCustomRetroflixNoUnread: a zero unread count → unread_mail "false".
func TestFetchCustomRetroflixNoUnread(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, retroflixMe, "0")
	}))
	defer ts.Close()

	c := NewClient(retroflixRegistry(t, ts.URL), "")
	data, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "custom", APIKey: "sekrit"})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	if got := data["unread_mail"]; got != "false" {
		t.Errorf("unread_mail = %#v, want \"false\"", got)
	}
}

// TestFetchCustomInfRatio: HUNO returns ratio as the string "Inf" when
// downloaded is 0 — it must be normalised to "Infinity" (which the frontend
// parses to a real Infinity and renders as ∞).
func TestFetchCustomInfRatio(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, hunoProfile, `"Inf"`)
	}))
	defer ts.Close()

	c := NewClient(customRegistry(t, ts.URL), "")
	data, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "custom", APIKey: "sekrit"})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	if got := data["ratio"]; got != "Infinity" {
		t.Errorf("ratio = %#v, want \"Infinity\"", got)
	}
}
