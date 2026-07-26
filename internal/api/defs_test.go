package api

import (
	"slices"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
)

// TestRequiredFieldsForDefLevel: a def may add its own required fields on top
// of its type's. Before this, api.required_fields on a tracker def parsed into
// nothing and was silently ignored — Aura4K had declared it since 2026-07 and
// no user was ever asked. The case it exists for is a UNIT3D tracker whose API
// omits the join date and whose operator has asked not to be scraped: without
// the prompt, account-age tracking quietly never works.
func TestRequiredFieldsForDefLevel(t *testing.T) {
	// unit3d's type requires nothing; the def asks for a join date.
	got := requiredFieldsFor(nil, &defs.CustomAPI{RequiredFields: []string{"join_date"}})
	if len(got) != 1 || got[0] != "join_date" {
		t.Errorf("def-level only = %v, want [join_date]", got)
	}

	// Type and def are unioned, not replaced, and neither duplicates.
	got = requiredFieldsFor([]string{"username", "join_date"},
		&defs.CustomAPI{RequiredFields: []string{"join_date", "session_cookie"}})
	want := []string{"username", "join_date", "session_cookie"}
	if len(got) != len(want) {
		t.Fatalf("union = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("union = %v, want %v", got, want)
		}
	}

	// A field the def's own API provides is still dropped — declaring it is a
	// harmless no-op, not a contradiction that demands it from the user.
	got = requiredFieldsFor(nil, &defs.CustomAPI{
		RequiredFields: []string{"join_date"},
		FieldMap:       map[string]string{"member_since": "join_date"},
	})
	if len(got) != 0 {
		t.Errorf("mapped field still required: %v", got)
	}

	// Nil API is unchanged behaviour.
	if got = requiredFieldsFor([]string{"username"}, nil); len(got) != 1 || got[0] != "username" {
		t.Errorf("nil api = %v, want [username]", got)
	}
}

// TestZenithRequiresJoinDate guards the shipped def: Zenith is API-only
// (disable_scraping) and its API reports no join date, so the setup form must
// demand one. A def edit that drops either half silently breaks account age.
func TestZenithRequiresJoinDate(t *testing.T) {
	d := testDeps(t)
	td, ok := d.Reg.Tracker("zenith")
	if !ok {
		t.Fatal("zenith def not found")
	}
	if td.API == nil || !slices.Contains(td.API.RequiredFields, "join_date") {
		t.Errorf("zenith must declare api.required_fields join_date, got %+v", td.API)
	}
	tt, ok := d.Reg.Type(td.Type)
	if !ok {
		t.Fatalf("type %q not found", td.Type)
	}
	if got := requiredFieldsFor(tt.API.RequiredFields, td.API); !slices.Contains(got, "join_date") {
		t.Errorf("resolved required fields = %v, want join_date", got)
	}
}
