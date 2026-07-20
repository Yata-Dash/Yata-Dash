package api

import (
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// fixedNow is a stable "today" for pacing math so deadline-relative test
// cases don't drift with the calendar.
var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func dateIn(days int) string {
	return fixedNow.AddDate(0, 0, days).Format("2006-01-02")
}

// TestComputeGoalPacingCoreStates covers the on_track/behind boundary
// (rate == required is on_track, just under is behind), done (already met),
// overdue (deadline reached, still unmet — with and without a rate), and
// no_rate (time remaining but no measurable growth).
func TestComputeGoalPacingCoreStates(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		target    string
		deadline  string
		merged    models.MergedStats
		rates     map[string]float64
		wantState PacingState
		wantOK    bool
		wantDays  int
		wantHasRt bool
	}{
		{
			name: "on_track: rate exactly meets the required pace",
			key:  "uploaded", target: "100 GiB", deadline: dateIn(10),
			merged:    models.MergedStats{"uploaded": models.StatField{Value: "50 GiB"}},
			rates:     map[string]float64{"uploaded": 5}, // remaining 50 / 10 days = 5/day required
			wantState: PacingOnTrack, wantOK: true, wantDays: 10, wantHasRt: true,
		},
		{
			name: "behind: rate just under the required pace",
			key:  "uploaded", target: "100 GiB", deadline: dateIn(10),
			merged:    models.MergedStats{"uploaded": models.StatField{Value: "50 GiB"}},
			rates:     map[string]float64{"uploaded": 4.9},
			wantState: PacingBehind, wantOK: true, wantDays: 10, wantHasRt: true,
		},
		{
			name: "done: already met, deadline irrelevant to the verdict",
			key:  "uploaded", target: "100 GiB", deadline: dateIn(10),
			merged:    models.MergedStats{"uploaded": models.StatField{Value: "150 GiB"}},
			rates:     map[string]float64{"uploaded": 1},
			wantState: PacingDone, wantOK: true, wantDays: 10,
		},
		{
			name: "overdue: deadline today, still unmet, no rate",
			key:  "uploaded", target: "100 GiB", deadline: dateIn(0),
			merged:    models.MergedStats{"uploaded": models.StatField{Value: "50 GiB"}},
			rates:     nil,
			wantState: PacingOverdue, wantOK: true, wantDays: 0,
		},
		{
			name: "overdue: deadline in the past, still unmet, WITH a rate",
			key:  "uploaded", target: "100 GiB", deadline: dateIn(-5),
			merged:    models.MergedStats{"uploaded": models.StatField{Value: "50 GiB"}},
			rates:     map[string]float64{"uploaded": 50}, // a healthy rate doesn't rescue an overdue row
			wantState: PacingOverdue, wantOK: true, wantDays: -5,
		},
		{
			name: "no_rate: time remaining but no measurable growth",
			key:  "uploaded", target: "100 GiB", deadline: dateIn(10),
			merged:    models.MergedStats{"uploaded": models.StatField{Value: "50 GiB"}},
			rates:     nil,
			wantState: PacingNoRate, wantOK: true, wantDays: 10,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := computeGoalPacing(tc.key, tc.target, tc.deadline, tc.merged, tc.rates, fixedNow)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if p.State != tc.wantState {
				t.Errorf("state = %q, want %q (pacing=%+v)", p.State, tc.wantState, p)
			}
			if p.DaysLeft != tc.wantDays {
				t.Errorf("daysLeft = %d, want %d", p.DaysLeft, tc.wantDays)
			}
			if p.HasRate != tc.wantHasRt {
				t.Errorf("hasRate = %v, want %v", p.HasRate, tc.wantHasRt)
			}
		})
	}
}

// TestComputeGoalPacingAgeExcluded confirms account age ("days") never
// produces a pacing row, even with a deadline and target present — Rule:
// reaching an age by a date is arbitrary.
func TestComputeGoalPacingAgeExcluded(t *testing.T) {
	merged := models.MergedStats{"join_date": models.StatField{Value: "2020-01-01"}}
	if _, ok := computeGoalPacing("days", "365", dateIn(30), merged, nil, fixedNow); ok {
		t.Error("expected days (account age) to never produce a pacing row")
	}
}

// TestComputeGoalPacingRatioExclusion covers the ratio branch: no verdict
// ever (state is always ratio_info), and the needed-upload figure is
// positive only when the current ratio is genuinely below target.
func TestComputeGoalPacingRatioExclusion(t *testing.T) {
	merged := models.MergedStats{
		"uploaded":   models.StatField{Value: "100 GiB"},
		"downloaded": models.StatField{Value: "100 GiB"},
	}
	// Target ratio 2.0: needs downloaded*2 - uploaded = 200-100 = 100 GiB more upload.
	p, ok := computeGoalPacing("ratio", "2.0", dateIn(10), merged, map[string]float64{"uploaded": 999}, fixedNow)
	if !ok {
		t.Fatal("expected a valid ratio_info row")
	}
	if p.State != PacingRatioInfo {
		t.Errorf("state = %q, want ratio_info (a rate must never produce a verdict for ratio)", p.State)
	}
	if p.NeededUploadGiB != 100 {
		t.Errorf("neededUploadGiB = %v, want 100", p.NeededUploadGiB)
	}

	// Already at/above the target ratio: needed upload must not go negative.
	metMerged := models.MergedStats{
		"uploaded":   models.StatField{Value: "300 GiB"},
		"downloaded": models.StatField{Value: "100 GiB"},
	}
	p2, ok := computeGoalPacing("ratio", "2.0", dateIn(10), metMerged, nil, fixedNow)
	if !ok {
		t.Fatal("expected a valid ratio_info row")
	}
	if p2.NeededUploadGiB != 0 {
		t.Errorf("neededUploadGiB = %v, want 0 when already past target", p2.NeededUploadGiB)
	}
}

// TestGoalsBehindLabelsExcludesRatioAndAge confirms the alert signal only
// ever includes behind/overdue non-ratio, non-age rows — a ratio or age
// entry (the latter shouldn't exist post-sanitize, but is defensively
// checked here too) never appears even when clearly "behind" by the numbers.
func TestGoalsBehindLabelsExcludesRatioAndAge(t *testing.T) {
	tr := models.Tracker{
		ID: "t1",
		Targets: map[string]string{
			"uploaded": "100 GiB", // behind: needs 5/day, has 1/day
			"ratio":    "5.0",     // ratio never contributes regardless of state
		},
		TargetDeadlines: map[string]string{
			"uploaded": dateIn(10),
			"ratio":    dateIn(10),
		},
	}
	merged := models.MergedStats{
		"uploaded":   models.StatField{Value: "50 GiB"},
		"downloaded": models.StatField{Value: "10 GiB"},
	}
	rates := map[string]float64{"uploaded": 1}

	got := goalsBehindLabels(tr, merged, rates, fixedNow)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 behind label (uploaded only), got %v", got)
	}
	if got[0] != "Uploaded needs 5.0 GiB/day" {
		t.Errorf("label = %q, want %q", got[0], "Uploaded needs 5.0 GiB/day")
	}
}

// TestGoalsBehindLabelsOverdueWording checks the overdue wording path and
// that on-track rows never appear in the alert signal.
func TestGoalsBehindLabelsOverdueWording(t *testing.T) {
	tr := models.Tracker{
		ID: "t1",
		Targets: map[string]string{
			"uploaded":  "100 GiB", // overdue: deadline in the past, unmet
			"seed_size": "10 TiB",  // on_track: never appears
		},
		TargetDeadlines: map[string]string{
			"uploaded":  dateIn(-3),
			"seed_size": dateIn(30),
		},
	}
	merged := models.MergedStats{
		"uploaded":  models.StatField{Value: "50 GiB"},
		"seed_size": models.StatField{Value: "5 TiB"},
	}
	rates := map[string]float64{"seed_size": 1000} // wildly ahead of pace

	got := goalsBehindLabels(tr, merged, rates, fixedNow)
	if len(got) != 1 || got[0] != "Uploaded overdue" {
		t.Fatalf("got %v, want exactly [%q]", got, "Uploaded overdue")
	}
}
