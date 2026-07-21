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
