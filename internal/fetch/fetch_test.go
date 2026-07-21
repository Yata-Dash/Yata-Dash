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

func gazelleJSONRegistry(t *testing.T, baseURL string) *defs.Registry {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	typeJSON := `{"schema_version":1,"key":"gazelle_json","label":"Gazelle JSON API",
		"api":{"kind":"gazelle_json","required_fields":[]}}`
	trackerJSON := fmt.Sprintf(`{"schema_version":1,"key":"redacted",
		"name":"Redacted","abbr":"RED","url":%q,"type":"gazelle_json"}`, baseURL)
	if err := os.WriteFile(filepath.Join(dir, "types", "gazelle_json.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trackers", "redacted.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := defs.Load(dir)
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	return reg
}

func gazelleGamesRegistry(t *testing.T, baseURL string) *defs.Registry {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	typeJSON := `{"schema_version":1,"key":"gazelle_games","label":"GazelleGames API",
		"api":{"kind":"gazelle_games","required_fields":[]}}`
	trackerJSON := fmt.Sprintf(`{"schema_version":1,"key":"gazellegames",
		"name":"GazelleGames","abbr":"GGn","url":%q,"type":"gazelle_games"}`, baseURL)
	if err := os.WriteFile(filepath.Join(dir, "types", "gazelle_games.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trackers", "gazellegames.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := defs.Load(dir)
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	return reg
}

func legacyGazelleRegistry(t *testing.T, baseURL string) *defs.Registry {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	typeJSON := `{"schema_version":1,"key":"gazelle","label":"Gazelle",
		"api":{"kind":"gazelle","required_fields":["username"]}}`
	trackerJSON := fmt.Sprintf(`{"schema_version":1,"key":"anthelion",
		"name":"Anthelion","abbr":"ANT","url":%q,"type":"gazelle"}`, baseURL)
	if err := os.WriteFile(filepath.Join(dir, "types", "gazelle.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trackers", "anthelion.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := defs.Load(dir)
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	return reg
}

func TestFetchUnit3DBlutopiaResponseShape(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user" {
			t.Errorf("path = %q, want /api/user", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sekrit" {
			t.Errorf("Authorization = %q, want Bearer API key", got)
		}
		fmt.Fprint(w, `{
			"username":"testuser","group":"BluUser",
			"uploaded":400,"downloaded":100,"ratio":4,"buffer":900,
			"seeding":12,"leeching":0,"seedbonus":"1234.50","hit_and_runs":0
		}`)
	}))
	defer ts.Close()

	reg, err := defs.Load("../../defs")
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	c := NewClient(reg, "")
	data, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "unit3d", APIKey: "sekrit"})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	want := map[string]any{
		"username": "testuser", "group": "BluUser",
		"uploaded": 400.0, "downloaded": 100.0, "ratio": 4.0, "buffer": 900.0,
		"seeding": 12.0, "leeching": 0.0, "bonus_points": "1234.50", "hit_and_runs": 0.0,
	}
	for key, expected := range want {
		if got := data[key]; got != expected {
			t.Errorf("%s = %#v, want %#v", key, got, expected)
		}
	}
	if _, ok := data["seedbonus"]; ok {
		t.Errorf("seedbonus alias should be normalized: %+v", data)
	}
}

func TestFetchUnit3DReelFliXResponseShape(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user" {
			t.Errorf("path = %q, want /api/user", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sekrit" {
			t.Errorf("Authorization = %q, want Bearer API key", got)
		}
		w.Header().Set("X-RateLimit-Limit", "30")
		fmt.Fprint(w, `{
			"username":"testuser","group":"User",
			"uploaded":"80.00 GiB","downloaded":"10.00 GiB","ratio":"8.00","buffer":"90.00 GiB",
			"seeding":3,"leeching":0,"seedbonus":"1234.50","hit_and_runs":0
		}`)
	}))
	defer ts.Close()

	reg, err := defs.Load("../../defs")
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	c := NewClient(reg, "")
	data, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "unit3d", APIKey: "sekrit"})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	want := map[string]any{
		"username": "testuser", "group": "User",
		"uploaded": "80.00 GiB", "downloaded": "10.00 GiB", "ratio": "8.00", "buffer": "90.00 GiB",
		"seeding": 3.0, "leeching": 0.0, "bonus_points": "1234.50", "hit_and_runs": 0.0,
	}
	for key, expected := range want {
		if got := data[key]; got != expected {
			t.Errorf("%s = %#v, want %#v", key, got, expected)
		}
	}
}

func TestFetchGazellePreservesLegacyQueryAPI(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api.php" || r.URL.Query().Get("apikey") != "sekrit" || r.URL.Query().Get("user") != "alice" {
			t.Fatalf("unexpected legacy Gazelle request: %s", r.URL.String())
		}
		fmt.Fprint(w, `{"status":"success","response":{
			"ID":7,"Username":"alice","Class":"Member","Uploaded":300,
			"Downloaded":100,"SeedCount":4,"Invites":2,
			"JoinDate":"2025-01-02 03:04:05","Snatched":9
		}}`)
	}))
	defer ts.Close()

	c := NewClient(legacyGazelleRegistry(t, ts.URL), "")
	data, ferr := c.Fetch(models.Tracker{
		URL: ts.URL, Type: "gazelle", APIKey: "sekrit", Username: "alice",
	})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	if data["username"] != "alice" || data["seeding"] != 4 {
		t.Fatalf("unexpected legacy Gazelle data: %+v", data)
	}
}

func TestFetchGazelleMergesStandardEndpoints(t *testing.T) {
	seen := map[string]int{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "sekrit" {
			t.Errorf("Authorization = %q, want raw API key", got)
		}
		action := r.URL.Query().Get("action")
		seen[action]++
		w.Header().Set("Content-Type", "application/json")
		switch action {
		case "index":
			fmt.Fprint(w, `{"status":"success","response":{
				"username":"listener","id":42,"giftTokens":30,"meritTokens":109,
				"userstats":{"uploaded":300,"downloaded":100,"ratio":3,"requiredratio":0.6,"class":"Elite"}
			}}`)
		case "user":
			if got := r.URL.Query().Get("id"); got != "42" {
				t.Errorf("user id = %q, want 42", got)
			}
			fmt.Fprint(w, `{"status":"success","response":{
				"username":"listener",
				"stats":{"joinedDate":"2025-02-03 04:05:06","uploaded":300,"downloaded":100,"ratio":3,"buffer":200,"requiredRatio":0.6},
				"personal":{"class":"Elite","warned":false,"enabled":true},
				"community":{"posts":1,"requestsFilled":2,"perfectFlacs":500,"uploaded":50,"groups":510,"seeding":20,"leeching":1,"snatched":30,"invited":4}
			}}`)
		case "community_stats":
			if got := r.URL.Query().Get("userid"); got != "42" {
				t.Errorf("community_stats userid = %q, want 42", got)
			}
			fmt.Fprint(w, `{"status":"success","response":{"leeching":1,"seeding":"20","snatched":"30","seedingsize":"476.55 GB"}}`)
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer ts.Close()

	c := NewClient(gazelleJSONRegistry(t, ts.URL), "")
	data, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "gazelle_json", APIKey: "sekrit"})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	want := map[string]any{
		"username": "listener", "user_id": "42", "group": "Elite",
		"uploaded": "300 B", "downloaded": "100 B", "buffer": "200 B",
		"ratio": 3.0, "required_ratio": 0.6, "fl_tokens": 139.0,
		"join_date": "2025-02-03", "warnings": 0, "seeding": 20,
		"leeching": 1, "snatched": 30, "users_invited": 4,
		"uploads_approved": 50, "requests_filled": 2, "forum_posts": 1,
		"groups_uploaded": 510, "perfect_flacs": 500, "seed_size": "476.55 GB",
	}
	for key, expected := range want {
		if got := data[key]; got != expected {
			t.Errorf("%s = %#v, want %#v", key, got, expected)
		}
	}
	for _, action := range []string{"index", "user", "community_stats"} {
		if seen[action] != 1 {
			t.Errorf("%s calls = %d, want 1", action, seen[action])
		}
	}
}

func TestFetchGazelleJSONRejectsFailureEnvelope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"failure","error":"User scope required"}`)
	}))
	defer ts.Close()

	c := NewClient(gazelleJSONRegistry(t, ts.URL), "")
	_, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "gazelle_json", APIKey: "sekrit"})
	if ferr == nil || ferr.Kind != "api_error" {
		t.Fatalf("Fetch error = %v, want api_error", ferr)
	}
	if ferr.Err == nil || ferr.Err.Error() != "User scope required" {
		t.Fatalf("Fetch error detail = %v, want API message", ferr.Err)
	}
}

func TestFetchGazelleGamesMergesAccountEndpoints(t *testing.T) {
	seen := map[string]int{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "sekrit" {
			t.Errorf("X-API-Key = %q, want API key", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		request := r.URL.Query().Get("request")
		seen[request]++
		w.Header().Set("Content-Type", "application/json")
		switch request {
		case "quick_user":
			fmt.Fprint(w, `{"status":"success","response":{
				"username":"player","id":24,
				"userstats":{"uploaded":4398046511104,"downloaded":1099511627776,"ratio":4,"requiredratio":0.6,"class":"Legendary Gamer"}
			}}`)
		case "user_stats_ratio":
			fmt.Fprint(w, `{"status":"success","response":{
				"uploaded":4398046511104,"downloaded":1099511627776,"ratio":4,
				"buffer":3298534883328,"disposable":3848290697216,"reqratio":0.6
			}}`)
		case "user":
			if got := r.URL.Query().Get("id"); got != "24" {
				t.Errorf("user id = %q, want 24", got)
			}
			fmt.Fprint(w, `{"status":"success","response":{
				"username":"player",
				"stats":{"joinedDate":"2025-01-02 03:04:05","ratio":"4.0","requiredRatio":0.6,"shareScore":1.25,"gold":1000},
				"personal":{"class":"Legendary Gamer","hnrs":null,"warned":false,"invites":2},
				"community":{"hourlyGold":2.5,"actualPosts":0,"ircActualLines":14,"seeding":20,"leeching":null,"snatched":100,"uniqueSnatched":90,"seedSize":34359738368},
				"achievements":{"userLevel":"Legendary Gamer","nextLevel":"Master Gamer","totalPoints":3000,"pointsToNextLvl":1200}
			}}`)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer ts.Close()

	c := NewClient(gazelleGamesRegistry(t, ts.URL), "")
	data, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "gazelle_games", APIKey: "sekrit"})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	want := map[string]any{
		"username": "player", "user_id": "24", "group": "Legendary Gamer",
		"uploaded": "4.00 TiB", "downloaded": "1.00 TiB", "buffer": "3.00 TiB",
		"ratio": 4.0, "required_ratio": 0.6,
		"disposable": "3.50 TiB", "join_date": "2025-01-02",
		"bonus_points": 1000.0, "share_score": 1.25, "invites": 2,
		"warnings": 0, "seeding": 20, "snatched": 100,
		"unique_snatched": 90, "seed_size": "32.00 GiB",
		"hourly_gold": 2.5, "forum_posts": 0, "irc_lines": 14,
		"achievement_points": 3000, "points_to_next_level": 1200,
		"next_group": "Master Gamer",
	}
	for key, expected := range want {
		if got := data[key]; got != expected {
			t.Errorf("%s = %#v, want %#v", key, got, expected)
		}
	}
	if _, ok := data["hit_and_runs"]; ok {
		t.Errorf("hit_and_runs should be omitted when API returns null: %+v", data)
	}
	if _, ok := data["leeching"]; ok {
		t.Errorf("leeching should be omitted when API returns null: %+v", data)
	}
	for _, request := range []string{"quick_user", "user_stats_ratio", "user"} {
		if seen[request] != 1 {
			t.Errorf("%s calls = %d, want 1", request, seen[request])
		}
	}
}

func TestFetchGazelleGamesRejectsFailureEnvelope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"failure","error":"User permission required"}`)
	}))
	defer ts.Close()

	c := NewClient(gazelleGamesRegistry(t, ts.URL), "")
	_, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "gazelle_games", APIKey: "sekrit"})
	if ferr == nil || ferr.Kind != "api_error" {
		t.Fatalf("Fetch error = %v, want api_error", ferr)
	}
	if ferr.Err == nil || ferr.Err.Error() != "User permission required" {
		t.Fatalf("Fetch error detail = %v, want API message", ferr.Err)
	}
}

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

func nebulanceRegistry(t *testing.T, baseURL string) *defs.Registry {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	typeJSON := `{"schema_version":1,"key":"custom","label":"Custom API",
		"api":{"kind":"custom","required_fields":["username"]},
		"scrape":{"skip_html_scrape":true}}`
	trackerJSON := fmt.Sprintf(`{
		"schema_version":1,"key":"nebulance","name":"Nebulance","abbr":"NBL",
		"url":%q,"type":"custom",
		"api":{
			"path":"/api.php?action=user&method=getuserinfo&type=username&user={username}",
			"auth_method":"api_key_query","api_key_param":"api_key",
			"success_field":"status","success_value":"success",
			"field_map":{
				"response.Username":"username","response.Class":"group",
				"response.JoinDate":"join_date","response.SeedCount":"seeding",
				"response.HnR":"hit_and_runs","response.Invites":"invites",
				"response.Grabbed":"grabbed","response.Snatched":"snatched",
				"response.ForumPosts":"forum_posts"
			},
			"byte_fields":{
				"response.Uploaded":"uploaded","response.Downloaded":"downloaded"
			},
			"buffer_from_bytes":true,"ratio_from_bytes":true
		}
	}`, baseURL)
	if err := os.WriteFile(filepath.Join(dir, "types", "custom.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trackers", "nebulance.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := defs.Load(dir)
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	return reg
}

func TestFetchCustomInterpolatesUsernameInQuery(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("user")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","response":{"Username":"Star Buck"}}`)
	}))
	defer ts.Close()

	c := NewClient(nebulanceRegistry(t, ts.URL), "")
	data, ferr := c.Fetch(models.Tracker{
		URL: ts.URL, Type: "custom", APIKey: "sekrit", Username: "Star Buck",
	})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	if gotQuery != "Star Buck" {
		t.Errorf("user query = %q, want %q", gotQuery, "Star Buck")
	}
	if got := data["username"]; got != "Star Buck" {
		t.Errorf("username = %#v, want %q", got, "Star Buck")
	}
}

func TestFetchCustomRejectsErrorEnvelope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"API key lacks user permission"}`)
	}))
	defer ts.Close()

	c := NewClient(nebulanceRegistry(t, ts.URL), "")
	_, ferr := c.Fetch(models.Tracker{
		URL: ts.URL, Type: "custom", APIKey: "sekrit", Username: "Starbuck",
	})
	if ferr == nil {
		t.Fatal("Fetch succeeded, want api_error")
	}
	if ferr.Kind != "api_error" {
		t.Fatalf("error kind = %q, want api_error", ferr.Kind)
	}
	if ferr.Err == nil || ferr.Err.Error() != "API key lacks user permission" {
		t.Errorf("error = %v, want API response message", ferr.Err)
	}
}

func TestFetchCustomRejectsUnsuccessfulEnvelope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"failed","response":{}}`)
	}))
	defer ts.Close()

	c := NewClient(nebulanceRegistry(t, ts.URL), "")
	_, ferr := c.Fetch(models.Tracker{
		URL: ts.URL, Type: "custom", APIKey: "sekrit", Username: "Starbuck",
	})
	if ferr == nil || ferr.Kind != "api_error" {
		t.Fatalf("Fetch error = %v, want api_error", ferr)
	}
}

func TestFetchCustomNebulanceShape(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("api_key"); got != "sekrit" {
			t.Errorf("api_key query = %q, want sekrit", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"status":"success",
			"response":{
				"Username":"testuser","Uploaded":300,"Downloaded":100,
				"SeedCount":92,"HnR":0,"Invites":1,"Class":"Flattop",
				"JoinDate":"2025-01-02 03:04:05","Grabbed":12,
				"Snatched":34,"ForumPosts":7
			}
		}`)
	}))
	defer ts.Close()

	c := NewClient(nebulanceRegistry(t, ts.URL), "")
	data, ferr := c.Fetch(models.Tracker{
		URL: ts.URL, Type: "custom", APIKey: "sekrit", Username: "testuser",
	})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	want := map[string]any{
		"username": "testuser", "group": "Flattop", "join_date": "2025-01-02",
		"uploaded": "300 B", "downloaded": "100 B", "buffer": "200 B",
		"ratio": 3.0, "seeding": 92, "hit_and_runs": 0, "invites": 1,
		"grabbed": 12, "snatched": 34, "forum_posts": 7,
	}
	for key, expected := range want {
		if got := data[key]; got != expected {
			t.Errorf("%s = %#v, want %#v", key, got, expected)
		}
	}
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
		"username":       "hawke",
		"group":          "Targaryen",
		"join_date":      "2022-01-01", // ISO datetime trimmed to date
		"uploaded":       "1.00 TiB",
		"downloaded":     "512.00 GiB",
		"buffer":         "512.00 GiB",
		"ratio":          2.0,
		"bonus_points":   1500.0,
		"seeding":        42,
		"leeching":       3,
		"hit_and_runs":   0,
		"warnings":       0,
		"vanguard_seeds": 10,
		"squire_seeds":   25,
		"knight_seeds":   50,
		"champion_seeds": 100,
		"guardian_seeds": 3,
		"legend_seeds":   5,
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
		"username":       "MysteryZiLLA",
		"group":          "Cinema Addicted", // class 3 via class_map
		"unread_mail":    "true",            // count 2 → truthy
		"join_date":      "2026-02-19",      // ISO trimmed
		"uploaded":       "1.02 TiB",
		"downloaded":     "148.93 GiB",
		"buffer":         "891.27 GiB", // uploaded − downloaded
		"ratio":          6.98,
		"bonus_points":   "172236.2",
		"snatched":       20,
		"hit_and_runs":   0,
		"invites":        3,
		"avg_seed_time":  8285514,
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

// speedappMe is the documented SpeedApp GET /api/me response shape — a flat
// object like RetroFlix's but with NO ratio field (only raw transfer bytes),
// so ratio_from_bytes must derive it. uploaded/downloaded are printf slots.
const speedappMe = `{
  "id": 5897683,
  "username": "MysteryZiLLA",
  "email": "user@example.com",
  "created_at": "2026-07-18T02:40:17+00:00",
  "class": 0,
  "uploaded": %d,
  "downloaded": %d,
  "title": "",
  "is_donor": false,
  "warned": false,
  "invites": 0,
  "hit_and_run_count": 0,
  "snatch_count": 2,
  "need_seed": 1,
  "average_seed_time": 8640,
  "free_leech_tokens": 3,
  "double_upload_tokens": 1
}`

// speedappRegistry writes a custom type + a SpeedApp tracker def exercising
// ratio_from_bytes alongside buffer_from_bytes.
func speedappRegistry(t *testing.T, baseURL string) *defs.Registry {
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
		"schema_version":1,"key":"speedapp","name":"SpeedApp","abbr":"SP",
		"url":%q,"type":"custom",
		"api":{
			"path":"/api/me","auth_method":"api_key_header",
			"field_map":{
				"username":"username","id":"user_id","created_at":"join_date",
				"snatch_count":"snatched","hit_and_run_count":"hit_and_runs",
				"average_seed_time":"avg_seed_time","invites":"invites",
				"free_leech_tokens":"fl_tokens","need_seed":"need_seed"
			},
			"byte_fields":{"uploaded":"uploaded","downloaded":"downloaded"},
			"buffer_from_bytes":true,
			"ratio_from_bytes":true
		}
	}`, baseURL)
	if err := os.WriteFile(filepath.Join(dir, "types", "custom.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trackers", "speedapp.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := defs.Load(dir)
	if err != nil {
		t.Fatalf("defs.Load: %v", err)
	}
	return reg
}

// fetchSpeedapp serves speedappMe with the given transfer bytes and fetches it.
func fetchSpeedapp(t *testing.T, up, down int64) map[string]any {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, speedappMe, up, down)
	}))
	defer ts.Close()

	c := NewClient(speedappRegistry(t, ts.URL), "")
	data, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "custom", APIKey: "sekrit"})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	return data
}

// TestFetchCustomRatioFromBytes: an API with raw transfer bytes and no ratio
// field gets ratio = uploaded/downloaded computed by the fetcher.
func TestFetchCustomRatioFromBytes(t *testing.T) {
	data := fetchSpeedapp(t, 53687091200, 10737418240) // 50 GiB / 10 GiB
	want := map[string]any{
		"uploaded":      "50.00 GiB",
		"downloaded":    "10.00 GiB",
		"buffer":        "40.00 GiB",
		"ratio":         5.0,
		"join_date":     "2026-07-18", // ISO trimmed
		"snatched":      2,
		"need_seed":     1,
		"avg_seed_time": 8640,
		"fl_tokens":     3.0,
	}
	for k, w := range want {
		if got, ok := data[k]; !ok {
			t.Errorf("missing field %q", k)
		} else if got != w {
			t.Errorf("%s = %#v, want %#v", k, got, w)
		}
	}
}

// TestFetchCustomRatioFromBytesInf: uploads but nothing downloaded → "Infinity"
// (the frontend renders ∞), matching how trackers report an undivided ratio.
func TestFetchCustomRatioFromBytesInf(t *testing.T) {
	data := fetchSpeedapp(t, 53687091200, 0)
	if got := data["ratio"]; got != "Infinity" {
		t.Errorf("ratio = %#v, want \"Infinity\"", got)
	}
}

// TestFetchCustomRatioFromBytesNoData: a 0/0 account has no meaningful ratio —
// the field must be absent (renders as an em dash), not 0 or ∞.
func TestFetchCustomRatioFromBytesNoData(t *testing.T) {
	data := fetchSpeedapp(t, 0, 0)
	if got, ok := data["ratio"]; ok {
		t.Errorf("ratio = %#v, want absent", got)
	}
}
