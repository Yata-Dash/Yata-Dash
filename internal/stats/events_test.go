package stats

import (
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// A scrape layer nobody rewrites (scraping switched off after it ran) kept
// announcing an event that ended weeks ago, because the fetcher's grace rule
// only ever saw the API's event list. The merge applies the same rule to
// whatever it assembled — the ended banner goes, a live one stays, and a
// string-typed timestamp (which is how the scrape layer stores it) counts.
func TestMergedExpiresStaleEvents(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-EventGrace - time.Hour).Unix()
	live := now.Add(2 * time.Hour).Unix()
	field := func(v any) models.StatField { return models.StatField{Value: v, Source: models.SourceScrape} }

	out := models.MergedStats{
		"active_event":         field("Global freeleech mode activated"),
		"active_event_ends_at": field("1788588000"), // 2026-09-05, as a string
		"active_events": field([]any{
			map[string]any{"label": "Old", "ends_at": float64(stale)},
			map[string]any{"label": "Live", "ends_at": float64(live)},
			map[string]any{"label": "Open-ended"},
		}),
		"ratio": field("2.0"),
	}
	expireStaleEvents(out, now)
	if _, ok := out["active_event"]; ok {
		t.Error("ended banner survived")
	}
	if _, ok := out["active_event_ends_at"]; ok {
		t.Error("ended countdown survived")
	}
	list := out["active_events"].Value.([]any)
	if len(list) != 2 || list[0].(map[string]any)["label"] != "Live" {
		t.Errorf("structured list = %v, want Live and Open-ended", list)
	}
	if _, ok := out["ratio"]; !ok {
		t.Error("an unrelated field was touched")
	}

	// Within the grace, "Ended" is still shown.
	recent := models.MergedStats{
		"active_event":         field("Freeleech"),
		"active_event_ends_at": field(now.Add(-time.Hour).Unix()),
	}
	expireStaleEvents(recent, now)
	if _, ok := recent["active_event"]; !ok {
		t.Error("an event that ended an hour ago should still show as Ended")
	}
}
