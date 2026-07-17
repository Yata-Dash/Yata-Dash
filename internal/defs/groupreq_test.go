package defs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMinMonthlyUploadsLoadsClean proves a def using the new
// min_monthly_uploads group requirement (RocketHD/Aither-style uploader
// classes) decodes through the strict DisallowUnknownFields pass in readJSON
// — no load issue, no fallback to the tolerant retry — and the value round-
// trips onto GroupRequirements.
func TestMinMonthlyUploadsLoadsClean(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "types"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "trackers"), 0o755); err != nil {
		t.Fatal(err)
	}
	typeJSON := `{"key":"unit3d","label":"UNIT3D","api":{"kind":"unit3d"}}`
	if err := os.WriteFile(filepath.Join(dir, "types", "unit3d.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	trackerJSON := `{
		"key": "rockethd",
		"name": "RocketHD",
		"url": "https://rockethd.example",
		"type": "unit3d",
		"groups": [
			{
				"name": "Uploader",
				"requirements": { "min_monthly_uploads": 4 }
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(dir, "trackers", "rockethd.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if issues := r.Issues(); len(issues) > 0 {
		t.Fatalf("unexpected load issues: %+v", issues)
	}
	td, ok := r.Tracker("rockethd")
	if !ok {
		t.Fatal("rockethd def not found")
	}
	if len(td.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(td.Groups))
	}
	if got := td.Groups[0].Requirements.MinMonthlyUploads; got != 4 {
		t.Errorf("MinMonthlyUploads = %d, want 4", got)
	}
}
