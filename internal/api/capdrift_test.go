package api

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/logging"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// driftDeps builds a Deps with a capturing logger and a defs dir declaring a
// deliberately narrow capability set.
func driftDeps(t *testing.T, declared string) (*Deps, *bytes.Buffer) {
	t.Helper()
	d := testDeps(t)
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	typeJSON := `{"schema_version":1,"key":"fam","label":"Family","api":{"kind":"unit3d"},
		"capabilities":{"api_stats":[` + declared + `]}}`
	trackerJSON := `{"schema_version":1,"key":"t","name":"TestTracker",
		"url":"https://t.example","type":"fam"}`
	if err := os.WriteFile(filepath.Join(dir, "types", "fam.json"), []byte(typeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trackers", "t.json"), []byte(trackerJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := defs.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	d.Reg = reg

	var buf bytes.Buffer
	lg, err := logging.New("", logging.Trace, 100, &buf, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	d.Log = lg
	resetCapDriftMemory()
	return d, &buf
}

var driftTracker = models.Tracker{ID: "t1", Name: "TestTracker", URL: "https://t.example", Type: "fam"}

// TestCapabilityDriftReportsUndeclaredFields: a tracker that starts returning
// something its def doesn't mention should say so. Without this, a
// hand-written declaration silently rots and the coverage figure quietly
// understates the tracker for as long as nobody notices.
func TestCapabilityDriftReportsUndeclaredFields(t *testing.T) {
	d, buf := driftDeps(t, `"uploaded","downloaded"`)
	checkCapabilityDrift(d, driftTracker, map[string]any{
		"uploaded": "1 GiB", "downloaded": "1 GiB", "seed_size": "2 TiB",
	})
	out := buf.String()
	if !strings.Contains(out, "seed_size") {
		t.Fatalf("the undeclared field was not reported: %q", out)
	}
	if !strings.Contains(out, "defs/trackers/t.json") {
		t.Errorf("the warning should name the file to edit: %q", out)
	}
	if !strings.Contains(out, "api_stats_add") {
		t.Errorf("the warning should say how to fix it: %q", out)
	}
}

// TestCapabilityDriftIsQuietWhenCorrect covers the cases that must NOT warn,
// or the message becomes noise everyone learns to skip.
func TestCapabilityDriftIsQuietWhenCorrect(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		data     map[string]any
	}{
		{"everything declared", `"uploaded","downloaded"`,
			map[string]any{"uploaded": "1 GiB", "downloaded": "1 GiB"}},
		{"a declared field simply absent this time", `"uploaded","warnings"`,
			map[string]any{"uploaded": "1 GiB"}},
		{"values Yata computes rather than the tracker reporting", `"uploaded"`,
			map[string]any{"uploaded": "1 GiB", "buffer": "1 GiB", "ratio": 2.0}},
		{"an empty value is not evidence the field is reported", `"uploaded"`,
			map[string]any{"uploaded": "1 GiB", "seed_size": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, buf := driftDeps(t, tc.declared)
			checkCapabilityDrift(d, driftTracker, tc.data)
			if out := buf.String(); strings.Contains(out, "capabilities:") {
				t.Errorf("should not have warned: %q", out)
			}
		})
	}
}

// TestCapabilityDriftDoesNotRepeat: refreshes run every few minutes, so the
// same discrepancy must be reported once rather than filling the log.
func TestCapabilityDriftDoesNotRepeat(t *testing.T) {
	d, buf := driftDeps(t, `"uploaded"`)
	data := map[string]any{"uploaded": "1 GiB", "seed_size": "2 TiB"}
	for i := 0; i < 5; i++ {
		checkCapabilityDrift(d, driftTracker, data)
	}
	if n := strings.Count(buf.String(), "capabilities:"); n != 1 {
		t.Errorf("warned %d times, want exactly 1", n)
	}
	// A DIFFERENT discrepancy is still worth hearing about.
	checkCapabilityDrift(d, driftTracker, map[string]any{"uploaded": "1 GiB", "adoptions": 3})
	if n := strings.Count(buf.String(), "capabilities:"); n != 2 {
		t.Errorf("a new discrepancy should warn again, got %d warnings", n)
	}
	// Reloading defs re-arms it, so a fix can be confirmed and a regression seen.
	resetCapDriftMemory()
	checkCapabilityDrift(d, driftTracker, data)
	if n := strings.Count(buf.String(), "capabilities:"); n != 3 {
		t.Errorf("a defs reload should re-arm the warning, got %d", n)
	}
}

// TestCapabilityDriftSkipsDerivedDefs: a def describing its own API is read
// directly, so a mismatch there would mean the derivation is wrong — not that
// someone's declaration is stale. Warning about it would send people editing
// a file that has nothing to edit.
func TestCapabilityDriftSkipsDerivedDefs(t *testing.T) {
	d := testDeps(t)
	dir := t.TempDir()
	for _, sub := range []string{"types", "trackers"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "types", "fam.json"),
		[]byte(`{"schema_version":1,"key":"fam","label":"F","api":{"kind":"custom"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "trackers", "t.json"),
		[]byte(`{"schema_version":1,"key":"t","name":"T","url":"https://t.example","type":"fam",
			"api":{"path":"/api","auth_method":"api_key_query","field_map":{"a":"uploaded"}}}`), 0o644)
	reg, err := defs.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	d.Reg = reg
	var buf bytes.Buffer
	lg, _ := logging.New("", logging.Trace, 100, &buf, 0, 0)
	d.Log = lg
	resetCapDriftMemory()

	checkCapabilityDrift(d, driftTracker, map[string]any{"uploaded": "1 GiB", "seed_size": "2 TiB"})
	if out := buf.String(); strings.Contains(out, "capabilities:") {
		t.Errorf("a derived def should not produce a drift warning: %q", out)
	}
}
