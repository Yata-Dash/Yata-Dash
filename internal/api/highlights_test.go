package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// highlightsBody runs the handler and decodes the envelope.
func highlightsBody(t *testing.T, d *Deps, query string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	getHighlights(d)(rec, httptest.NewRequest(http.MethodGet, "/api/highlights"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// addTargetTracker adds an enabled tracker tracking one uploaded target, with
// stored stats that either meet it or don't.
func addTargetTracker(t *testing.T, d *Deps, id, name string, meetsIt bool) {
	t.Helper()
	if err := d.Cfg.AddTracker(models.Tracker{
		ID: id, Name: name, URL: "https://" + id + ".invalid", Enabled: true,
		Targets: map[string]string{"uploaded": "10 TiB"},
	}); err != nil {
		t.Fatal(err)
	}
	uploaded := "1 TiB"
	if meetsIt {
		uploaded = "50 TiB"
	}
	if err := d.Stats.SaveAPI(id, map[string]any{"uploaded": uploaded}); err != nil {
		t.Fatal(err)
	}
}

// TestHighlightTargetsRanksClosestFirst: the whole point of the endpoint is
// that a caller can take the first N rows and trust they are the ones worth
// showing. A tracker that has MET its targets ranks above one that hasn't —
// a finished target set is the moment to go ask for the promotion, so it is
// the most actionable row, not the least.
func TestHighlightTargetsRanksClosestFirst(t *testing.T) {
	d := testDeps(t)
	addTargetTracker(t, d, "behind", "Behind", false)
	addTargetTracker(t, d, "done", "Done", true)

	got := highlightTargets(d)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	if got[0].Name != "Done" {
		t.Errorf("first row = %q, want the met tracker %q", got[0].Name, "Done")
	}
	if got[0].Remaining != 0 || got[0].Met != got[0].Total {
		t.Errorf("met tracker reported %d/%d (remaining %d), want met == total and remaining 0",
			got[0].Met, got[0].Total, got[0].Remaining)
	}
	if got[1].Remaining == 0 {
		t.Errorf("unmet tracker reported remaining 0: %+v", got[1])
	}
}

// TestHighlightTargetsSkipsDisabled: a disabled tracker's stats are frozen at
// whatever they were when it was switched off, so any "4/6" from one is a
// claim about the past. Same reasoning the pathways engine already applies.
func TestHighlightTargetsSkipsDisabled(t *testing.T) {
	d := testDeps(t)
	addTargetTracker(t, d, "off", "Off", true)
	if err := d.Cfg.UpdateTracker("off", func(tr *models.Tracker) { tr.Enabled = false }); err != nil {
		t.Fatal(err)
	}
	if got := highlightTargets(d); len(got) != 0 {
		t.Errorf("disabled tracker was highlighted: %+v", got)
	}
}

// TestHighlightTargetsSkipsUntargeted: a tracker tracking nothing has no m/T
// to report and must not appear as "0/0", which would read as a failure.
func TestHighlightTargetsSkipsUntargeted(t *testing.T) {
	d := testDeps(t)
	if err := d.Cfg.AddTracker(models.Tracker{
		ID: "plain", Name: "Plain", URL: "https://plain.invalid", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.Stats.SaveAPI("plain", map[string]any{"uploaded": "5 TiB"}); err != nil {
		t.Fatal(err)
	}
	if got := highlightTargets(d); len(got) != 0 {
		t.Errorf("tracker with no targets was highlighted: %+v", got)
	}
}

// TestHighlightsLimitTruncatesButReportsFullCount: a card showing five rows
// still needs to say "5 of 12", so the counts must be the pre-truncation
// lengths.
func TestHighlightsLimitTruncatesButReportsFullCount(t *testing.T) {
	d := testDeps(t)
	for _, id := range []string{"a", "b", "c"} {
		addTargetTracker(t, d, id, "T"+id, false)
	}
	body := highlightsBody(t, d, "?limit=2")

	rows, _ := body["targets"].([]any)
	if len(rows) != 2 {
		t.Errorf("returned %d rows, want 2", len(rows))
	}
	if n, _ := body["target_count"].(float64); int(n) != 3 {
		t.Errorf("target_count = %v, want the full 3", body["target_count"])
	}
}

// TestHighlightsLimitIsClamped: limit is a public knob on a token-readable
// endpoint, so an absurd value must be capped rather than honoured.
func TestHighlightsLimitIsClamped(t *testing.T) {
	d := testDeps(t)
	body := highlightsBody(t, d, "?limit=100000")
	if n, _ := body["limit"].(float64); int(n) != highlightMaxLimit {
		t.Errorf("limit = %v, want it clamped to %d", body["limit"], highlightMaxLimit)
	}
}

// TestHighlightsRejectsBadLimit: a non-numeric or zero limit is a caller bug,
// and silently substituting a default hides it.
func TestHighlightsRejectsBadLimit(t *testing.T) {
	d := testDeps(t)
	for _, q := range []string{"?limit=abc", "?limit=0", "?limit=-3"} {
		rec := httptest.NewRecorder()
		getHighlights(d)(rec, httptest.NewRequest(http.MethodGet, "/api/highlights"+q, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
}

// TestHighlightsEmptyListsAreArrays: null would make a template doing
// .targets[0] fail in a way that reads as a Yata bug rather than "no data".
func TestHighlightsEmptyListsAreArrays(t *testing.T) {
	d := testDeps(t)
	rec := httptest.NewRecorder()
	getHighlights(d)(rec, httptest.NewRequest(http.MethodGet, "/api/highlights", nil))

	var raw struct {
		Targets  *[]json.RawMessage `json:"targets"`
		Pathways *[]json.RawMessage `json:"pathways"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if raw.Targets == nil || raw.Pathways == nil {
		t.Errorf("an empty list serialised as null: %s", rec.Body.String())
	}
}

// TestHighlightsPathwaysAvailableFlag: without the dataset the feature is
// hidden rather than broken, and the card needs to tell those apart.
func TestHighlightsPathwaysAvailableFlag(t *testing.T) {
	if body := highlightsBody(t, testDeps(t), ""); body["pathways_available"] != false {
		t.Errorf("pathways_available = %v with no dataset, want false", body["pathways_available"])
	}
	if body := highlightsBody(t, pathDeps(t), ""); body["pathways_available"] != true {
		t.Errorf("pathways_available = %v with the dataset loaded, want true", body["pathways_available"])
	}
}

// TestHighlightPathwaysDropsNotInterested is the reason this endpoint exists
// rather than the dashboard calling /api/pathways/targets: "not interested"
// is the user having said explicitly that they don't want to hear about a
// tracker, and a dashboard card is exactly the surface that would otherwise
// nag about it forever. The filter is applied SERVER-side so every
// integration inherits it.
func TestHighlightPathwaysDropsNotInterested(t *testing.T) {
	d := pathDeps(t)
	// AlphaRatio is a real dataset tracker; give the user a membership that
	// actually has routes out of it so the list is non-empty to begin with.
	if err := d.Cfg.AddTracker(models.Tracker{
		ID: "ar", Name: "AlphaRatio", URL: "https://alpharatio.cc", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.Stats.SaveAPI("ar", map[string]any{"uploaded": "50 TiB", "ratio": "5.0"}); err != nil {
		t.Fatal(err)
	}

	before := highlightPathways(d)
	if len(before) == 0 {
		t.Skip("dataset has no active routes out of AlphaRatio — nothing to filter")
	}
	drop := before[0].Name

	s := d.Cfg.Settings()
	s.PathwayNotInterested = []string{drop}
	if err := d.Cfg.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}

	for _, got := range highlightPathways(d) {
		if got.Name == drop {
			t.Fatalf("%q is marked not-interested but still appeared", drop)
		}
	}
}

// TestNotInterestedMatchIsCaseInsensitive: the stored list is whatever casing
// the UI wrote, and a case mismatch would silently un-hide a tracker.
func TestNotInterestedMatchIsCaseInsensitive(t *testing.T) {
	d := testDeps(t)
	s := d.Cfg.Settings()
	s.PathwayNotInterested = []string{"Upload.cx", "  ", "AITHER"}
	if err := d.Cfg.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	got := notInterestedSet(d)
	for _, want := range []string{"upload.cx", "aither"} {
		if !got[want] {
			t.Errorf("%q missing from the not-interested set: %v", want, got)
		}
	}
	if len(got) != 2 {
		t.Errorf("blank entry was kept: %v", got)
	}
}
