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

func TestTrackerRulesCarryLoginGap(t *testing.T) {
	var rules TrackerRules
	if err := json.Unmarshal([]byte(`{"max_login_gap_days": 90}`), &rules); err != nil {
		t.Fatal(err)
	}
	if rules.MaxLoginGapDays != 90 {
		t.Errorf("max login gap = %d, want 90", rules.MaxLoginGapDays)
	}
}

// TestLoginImmuneFollowsTheLadder: immunity starts at the named rank and
// covers everything above it on the def's own ladder; an unknown group on
// either side is not immune, because a policy Yata cannot rule out is one it
// should still warn about.
func TestLoginImmuneFollowsTheLadder(t *testing.T) {
	reg, err := Load("../../defs")
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://anthelion.me"
	cases := map[string]bool{
		"Torrent Master": true, "Legend": true, "torrent master": true,
		"Guru": false, "User": false, "": false, "Made Up": false,
	}
	for group, want := range cases {
		if got := reg.LoginImmune(url, group); got != want {
			t.Errorf("LoginImmune(anthelion, %q) = %v, want %v", group, got, want)
		}
	}
	if reg.LoginImmune("https://aither.cc", "Elite") {
		t.Error("a def with no exemption declared must not grant one")
	}
}
