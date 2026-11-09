package api

import (
	"encoding/json"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// warnings is documented in docs/API.md, but it is not a history field, so the
// numeric snapshot the rest of the summary reads from never carried it and it
// was omitted for every tracker. Each shape a fetcher or scrape stores must
// come through, and a tracker that does not report it must still omit it.
func TestSummaryReportsWarnings(t *testing.T) {
	d := testDeps(t)
	cases := []struct {
		id    string
		value any
		want  *float64
	}{
		{"wnum", 2.0, ptrFloat(2)},  // JSON number from an API
		{"wstr", "3", ptrFloat(3)},  // scraped text
		{"wzero", 0.0, ptrFloat(0)}, // zero is a real answer, not an absence
		{"wjunk", "n/a", nil},       // not a count
		{"wnone", nil, nil},         // not reported at all
	}
	for _, c := range cases {
		if err := d.Cfg.AddTracker(models.Tracker{ID: c.id, Name: c.id, URL: "https://" + c.id + ".invalid", Type: "unit3d", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		fields := map[string]any{"ratio": 1.5}
		if c.value != nil {
			fields["warnings"] = c.value
		}
		if err := d.Stats.SaveAPI(c.id, fields); err != nil {
			t.Fatal(err)
		}
	}

	var out struct {
		Trackers []struct {
			ID       string   `json:"id"`
			Warnings *float64 `json:"warnings"`
		} `json:"trackers"`
	}
	if err := json.Unmarshal([]byte(getJSON(t, d, "/api/summary")), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]*float64{}
	for _, tr := range out.Trackers {
		got[tr.ID] = tr.Warnings
	}
	for _, c := range cases {
		g := got[c.id]
		switch {
		case c.want == nil && g != nil:
			t.Errorf("%s: warnings = %v, want omitted", c.id, *g)
		case c.want != nil && g == nil:
			t.Errorf("%s: warnings omitted, want %v", c.id, *c.want)
		case c.want != nil && *g != *c.want:
			t.Errorf("%s: warnings = %v, want %v", c.id, *g, *c.want)
		}
	}
}

func ptrFloat(f float64) *float64 { return &f }
