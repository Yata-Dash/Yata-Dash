package api

import (
	"testing"
)

// TestLooksLikeStats is the guard that stops detection adopting a type just
// because the request came back as JSON. Plenty of sites answer an unknown
// path with 200 and a one-key error body, and a field count alone reads that
// as a successful match — which is how a tracker ends up permanently mislabelled.
func TestLooksLikeStats(t *testing.T) {
	convincing := []map[string]any{
		{"username": "mystery", "group": "PowerUser"},
		{"uploaded": "5.00 TiB", "downloaded": "1.00 TiB"},
		{"ratio": 5.0},
		{"seeding": 42.0},
		{"bonus_points": "1234.50"},
	}
	for _, fields := range convincing {
		if !looksLikeStats(fields) {
			t.Errorf("%v should be recognised as stats", fields)
		}
	}

	unconvincing := []map[string]any{
		{},
		{"error": "not found"},
		{"message": "Unauthenticated."},
		{"data": map[string]any{"username": "buried"}}, // envelope not unwrapped
		{"success": false},
		// Present but empty carries no information — a site echoing back the
		// field names it was asked for would otherwise pass.
		{"username": ""},
		{"ratio": nil},
	}
	for _, fields := range unconvincing {
		if looksLikeStats(fields) {
			t.Errorf("%v should NOT be accepted as stats", fields)
		}
	}
}

// TestDetectCandidatesAreReal: every candidate must be a type the registry
// knows and a fetcher handles, or detection would silently skip it.
func TestDetectCandidatesAreReal(t *testing.T) {
	d := testDeps(t)
	for _, key := range detectCandidates {
		tt, ok := d.Reg.Type(key)
		if !ok {
			t.Errorf("candidate type %q is not in the registry", key)
			continue
		}
		if tt.API.Kind == "none" {
			t.Errorf("candidate type %q has no API to probe", key)
		}
	}
	// The no-definition placeholder must never be probed — it exists to mean
	// "nothing is collected", so trying it would always look like a match.
	for _, key := range detectCandidates {
		if key == "unknown" {
			t.Error("the unknown type must not be a detection candidate")
		}
	}
}
