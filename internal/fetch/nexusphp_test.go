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

// TestFetchNexusPHPShape runs the shipped defs/types/nexusphp.json against a
// server answering the way NexusPHP's source says /api/v1/profile does: a
// {ret, msg, data} envelope around a Laravel resource, so the user is at
// data.data. No def — a NexusPHP tracker added by hand gets exactly this.
// Written before anyone could test a live site; when one is reached, fix the
// type from what it returns and this test with it.
func TestFetchNexusPHPShape(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/profile" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("include_fields[user]"); got != "seeding_leeching_data" {
			t.Errorf("include_fields[user] = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ret":0,"msg":"OK","time":0.01,"rid":"x","data":{"data":{
			"id":7,"username":"someone","class":3,"class_text":"Elite User",
			"added":"2024-02-03 04:05:06","last_login":"2026-09-20 10:00:00",
			"invites":2,"uploaded":2199023255552,"downloaded":1099511627776,
			"bonus":12345.5,"seed_points":67890.25,"seedtime":864000,
			"seeding_leeching_data":{"seeding_count":42,"seeding_size":3298534883328,"leeching_count":1,"leeching_size":0}
		}}}`)
	}))
	defer ts.Close()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "types"), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("../../defs/types/nexusphp.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "types", "nexusphp.json"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := defs.Load(dir)
	if err != nil || len(reg.Issues()) > 0 {
		t.Fatalf("load: %v %+v", err, reg.Issues())
	}

	data, ferr := NewClient(reg, "").Fetch(models.Tracker{URL: ts.URL, Type: "nexusphp", APIKey: "tok"})
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	want := map[string]any{
		"username": "someone", "group": "Elite User", "join_date": "2024-02-03",
		"last_login": "2026-09-20 10:00:00", "invites": 2,
		"uploaded": "2.00 TiB", "downloaded": "1.00 TiB", "buffer": "1.00 TiB", "ratio": 2.0,
		"seed_size": "3.00 TiB", "seeding": 42, "leeching": 1,
		"bonus_points": 12345.5, "seeding_points": 67890.25, "total_seedtime": 864000,
	}
	for k, w := range want {
		if got := data[k]; fmt.Sprint(got) != fmt.Sprint(w) {
			t.Errorf("%s = %#v, want %#v", k, got, w)
		}
	}

	// ret != 0 is NexusPHP's failure envelope — refused, not read as zeros.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"ret":-1,"msg":"Unauthenticated.","data":[]}`)
	}))
	defer bad.Close()
	if _, ferr := NewClient(reg, "").Fetch(models.Tracker{URL: bad.URL, Type: "nexusphp", APIKey: "tok"}); ferr == nil {
		t.Error("a ret -1 envelope should fail the fetch")
	}
}
