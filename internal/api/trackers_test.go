package api

import (
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

func TestRequiredFieldsIncludesCustomAPIPathInputs(t *testing.T) {
	api := &defs.CustomAPI{
		Path:     "/api.php?action=user&user={username}",
		FieldMap: map[string]string{"response.JoinDate": "join_date"},
	}
	got := requiredFieldsFor([]string{"join_date"}, api)
	if len(got) != 1 || got[0] != "username" {
		t.Fatalf("required fields = %v, want [username]", got)
	}
}

// TestRequiredFieldsIncludesSessionCookieForCustomAuthMethod: a custom def
// whose API authenticates with a user-supplied session cookie
// (auth_method: "session_cookie") must resolve "session_cookie" into its
// required fields, the same way gazelle_json_cookie-typed trackers do —
// that's what keeps the cookie input visible in the add/edit modal even
// with scraping off.
func TestRequiredFieldsIncludesSessionCookieForCustomAuthMethod(t *testing.T) {
	api := &defs.CustomAPI{
		Path:       "/api.php?action=user",
		AuthMethod: "session_cookie",
	}
	got := requiredFieldsFor(nil, api)
	found := false
	for _, f := range got {
		found = found || f == "session_cookie"
	}
	if !found {
		t.Fatalf("required fields = %v, want to include session_cookie", got)
	}
}

// TestApplyPayloadSanitizesTargetDeadlines covers target_deadlines' save-time
// rules: an entry for a field with no target value is dropped, a "days"
// (account age) entry is always dropped even if one somehow arrives, and a
// legitimate entry backed by a real target survives.
func TestApplyPayloadSanitizesTargetDeadlines(t *testing.T) {
	targets := map[string]string{"uploaded": "10 TiB"}
	deadlines := map[string]string{
		"uploaded":   "2026-06-01", // kept — backed by a real target
		"downloaded": "2026-06-01", // dropped — no matching target value
		"days":       "2026-06-01", // dropped — account age can never take a deadline
	}
	tr := &models.Tracker{}
	applyPayload(tr, trackerPayload{
		URL:             strp("https://example.org"),
		Targets:         &targets,
		TargetDeadlines: &deadlines,
	})

	if got := len(tr.TargetDeadlines); got != 1 {
		t.Fatalf("len(TargetDeadlines) = %d, want 1 (only 'uploaded' should survive): %+v", got, tr.TargetDeadlines)
	}
	if tr.TargetDeadlines["uploaded"] != "2026-06-01" {
		t.Errorf("uploaded deadline = %q, want 2026-06-01", tr.TargetDeadlines["uploaded"])
	}
	if _, ok := tr.TargetDeadlines["downloaded"]; ok {
		t.Error("downloaded deadline must be dropped — no matching target value")
	}
	if _, ok := tr.TargetDeadlines["days"]; ok {
		t.Error("days (account age) deadline must always be dropped")
	}
}

// TestApplyPayloadSanitizeDropsDeadlineWhenTargetRemoved covers the
// remove-the-target case: a later payload that clears the target for a key
// must drop its stale deadline too, even though this payload only touches
// Targets (not TargetDeadlines) — sanitize runs on every apply.
func TestApplyPayloadSanitizeDropsDeadlineWhenTargetRemoved(t *testing.T) {
	tr := &models.Tracker{
		Targets:         map[string]string{"uploaded": "10 TiB"},
		TargetDeadlines: map[string]string{"uploaded": "2026-06-01"},
	}
	emptyTargets := map[string]string{} // the user removed the uploaded target row
	applyPayload(tr, trackerPayload{Targets: &emptyTargets})

	if len(tr.TargetDeadlines) != 0 {
		t.Errorf("expected the stale deadline to be dropped once its target is gone, got %+v", tr.TargetDeadlines)
	}
}

// TestToViewRoundTripsTargetDeadlines confirms toView carries TargetDeadlines
// through to the view (nil normalized to {}, like Targets).
func TestToViewRoundTripsTargetDeadlines(t *testing.T) {
	d := testDeps(t)

	withDeadlines := models.Tracker{
		ID:              "t1",
		URL:             "//test.local",
		Targets:         map[string]string{"uploaded": "10 TiB"},
		TargetDeadlines: map[string]string{"uploaded": "2026-06-01"},
	}
	v := toView(d, withDeadlines)
	if v.TargetDeadlines["uploaded"] != "2026-06-01" {
		t.Errorf("view TargetDeadlines = %+v, want uploaded=2026-06-01", v.TargetDeadlines)
	}

	noDeadlines := models.Tracker{ID: "t2", URL: "//test.local"}
	v2 := toView(d, noDeadlines)
	if v2.TargetDeadlines == nil {
		t.Error("expected TargetDeadlines to normalize nil to an empty map, like Targets")
	}
}

func TestToViewIncludesCategorySpecificSeedRules(t *testing.T) {
	d := testDeps(t)
	v := toView(d, models.Tracker{URL: "https://nebulance.io"})
	if v.MinSeedDaysEpisode != 1 || v.MinSeedDaysSeason != 5 {
		t.Fatalf("seed rules = episode %d, season %d; want 1 and 5",
			v.MinSeedDaysEpisode, v.MinSeedDaysSeason)
	}
}

func TestToViewIncludesTrackerRuleNote(t *testing.T) {
	d := testDeps(t)
	v := toView(d, models.Tracker{URL: "https://animebytes.tv"})
	if v.MinSeedHours != 72 {
		t.Fatalf("seed hours = %d, want 72", v.MinSeedHours)
	}
	if v.RuleNote == "" {
		t.Fatal("expected AnimeBytes rule note in tracker view")
	}
}

func strp(s string) *string { return &s }
