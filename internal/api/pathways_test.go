package api

import (
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/pathways"
)

// pathDeps is testDeps plus the real community pathways dataset, so the
// name-resolution tests run against the shipped routes.json rather than a
// synthetic one. "AlphaRatio" is a real dataset tracker that Yata has NO def
// for — exactly the imported/def-less case this feature exists for.
func pathDeps(t *testing.T) *Deps {
	t.Helper()
	d := testDeps(t)
	p, err := pathways.Load("../../defs/pathways/routes.json")
	if err != nil {
		t.Fatalf("load pathways: %v", err)
	}
	d.Paths = p
	return d
}

func setIncludeDisabled(t *testing.T, d *Deps, on bool) {
	t.Helper()
	s := d.Cfg.Settings()
	s.PathwaysIncludeDisabled = on
	if err := d.Cfg.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
}

// TestPathwayNameForFallbacks: a tracker with no def must still resolve to
// its dataset entry — by display name, or failing that by its URL host.
// Without this, the "include disabled trackers" toggle would do nothing for
// the imported trackers it was built for.
func TestPathwayNameForFallbacks(t *testing.T) {
	d := pathDeps(t)
	nameIndex := map[string]string{}
	for _, n := range d.Paths.Names() {
		nameIndex[lowerTrim(n)] = n
		if a := d.Paths.Abbr[n]; a != "" {
			nameIndex[lowerTrim(a)] = n
		}
	}

	cases := []struct {
		what string
		tr   models.Tracker
		want string
	}{
		{"def-less, name matches the dataset",
			models.Tracker{Name: "AlphaRatio", URL: "https://example.invalid"}, "AlphaRatio"},
		{"def-less, name match is case-insensitive",
			models.Tracker{Name: "alpharatio", URL: "https://example.invalid"}, "AlphaRatio"},
		{"def-less, abbreviation matches",
			models.Tracker{Name: "AR", URL: "https://example.invalid"}, "AlphaRatio"},
		{"def-less, resolves via the URL host when the name doesn't match",
			models.Tracker{Name: "My Random Import", URL: "https://alpharatio.cc"}, "AlphaRatio"},
		{"unrelated tracker stays out of the dataset",
			models.Tracker{Name: "Not A Real Tracker", URL: "https://nowhere.invalid"}, ""},
	}
	for _, tc := range cases {
		if got := pathwayNameFor(d, tc.tr, nameIndex); got != tc.want {
			t.Errorf("%s: pathwayNameFor = %q, want %q", tc.what, got, tc.want)
		}
	}
}

func lowerTrim(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// TestMapUserTrackersDisabledToggle: disabled trackers join the pathway
// inputs only when the setting is on, and always without stats.
func TestMapUserTrackersDisabledToggle(t *testing.T) {
	d := pathDeps(t)
	if err := d.Cfg.AddTracker(models.Tracker{
		ID: "t-off", Name: "AlphaRatio", URL: "https://alpharatio.cc",
		Type: "custom", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	// Frozen stats from before it was switched off — these must never be used.
	if err := d.Stats.SaveAPI("t-off", map[string]any{
		"uploaded": "50.00 TiB", "ratio": 9.9, "join_date": "2020-01-01",
	}); err != nil {
		t.Fatal(err)
	}

	if got := mapUserTrackers(d); len(got) != 0 {
		t.Fatalf("disabled tracker must be excluded by default, got %+v", got)
	}

	setIncludeDisabled(t, d, true)
	got := mapUserTrackers(d)
	if len(got) != 1 {
		t.Fatalf("expected the disabled tracker to be included, got %d", len(got))
	}
	u := got[0]
	if !u.Disabled || u.PathwayName != "AlphaRatio" {
		t.Errorf("disabled/name wrong: %+v", u)
	}
	// Every stat must read unknown (-1) despite the saved layer above.
	if u.Stats.UploadedGiB != -1 || u.Stats.Ratio != -1 || u.Stats.AgeDays != -1 {
		t.Errorf("frozen stats leaked into a disabled tracker: %+v", u.Stats)
	}
	if u.Rates != (pathways.Rates{}) {
		t.Errorf("disabled tracker should carry no growth rates: %+v", u.Rates)
	}
}

// TestMapUserTrackersEnabledDefless: an ENABLED tracker with no def also
// resolves through the name fallback (a def-less tracker shouldn't be
// invisible just because it's switched on), and keeps its normal stats path.
func TestMapUserTrackersEnabledDefless(t *testing.T) {
	d := pathDeps(t)
	if err := d.Cfg.AddTracker(models.Tracker{
		ID: "t-on", Name: "AlphaRatio", URL: "https://alpharatio.cc",
		Type: "custom", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	got := mapUserTrackers(d)
	if len(got) != 1 || got[0].PathwayName != "AlphaRatio" {
		t.Fatalf("def-less enabled tracker should resolve, got %+v", got)
	}
	if got[0].Disabled {
		t.Error("an enabled tracker must not be marked disabled")
	}
}
