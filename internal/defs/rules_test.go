package defs

import (
	"encoding/json"
	"testing"
)

func TestTrackerRulesSupportCategorySpecificSeedTimes(t *testing.T) {
	var rules TrackerRules
	if err := json.Unmarshal([]byte(`{
		"min_seed_days_episode": 1,
		"min_seed_days_season": 5
	}`), &rules); err != nil {
		t.Fatal(err)
	}
	if rules.MinSeedDaysEpisode != 1 {
		t.Errorf("episode seed days = %d, want 1", rules.MinSeedDaysEpisode)
	}
	if rules.MinSeedDaysSeason != 5 {
		t.Errorf("season seed days = %d, want 5", rules.MinSeedDaysSeason)
	}
}

func TestTrackerRulesSupportFinePrint(t *testing.T) {
	var rules TrackerRules
	if err := json.Unmarshal([]byte(`{
		"min_seed_hours": 72,
		"note": "Add five hours per GiB over 10 GiB, capped at 21 days."
	}`), &rules); err != nil {
		t.Fatal(err)
	}
	if rules.MinSeedHours != 72 {
		t.Errorf("seed hours = %d, want 72", rules.MinSeedHours)
	}
	if rules.Note != "Add five hours per GiB over 10 GiB, capped at 21 days." {
		t.Errorf("rule note = %q", rules.Note)
	}
}
