package api

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
// required fields — that's what keeps the cookie input visible in the
// add/edit modal even with scraping off.
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

// TestValidatePayloadSanitizesManualStats: typed-in stats are stored in the
// same shapes a fetch produces, so nothing downstream can tell a typed number
// from a fetched one. Sizes get two decimals and a cased unit, durations
// become the canonical seed-time form, blanks are dropped rather than stored
// as an answer, and an unrecognised field is kept as typed — the canonical set
// grows, and refusing a value merely because this list hasn't caught up would
// lose the user's data.
func TestValidatePayloadSanitizesManualStats(t *testing.T) {
	stats := map[string]string{
		"uploaded":        " 5.5 tb ",    // size → 2dp, trimmed, unit cased
		"seed_size":       "800.129 gib", // size → 2dp, "gib" → "GiB"
		"real_downloaded": "512 b",       // bare byte unit → "B"
		"downloaded":      "200g",        // a bare letter is a unit too
		"avg_seed_time":   "90000",       // raw seconds → canonical duration
		"total_seedtime":  "2 years 3 months",
		"ratio":           "4.58", // plain value, untouched
		"leeching":        "",     // empty → dropped entirely
		"future_stat":     "7",    // unknown field → kept as typed
	}
	p := trackerPayload{ManualStats: &stats}
	if err := validateTrackerPayload(&p); err != nil {
		t.Fatalf("validate: %v", err)
	}
	tr := &models.Tracker{}
	applyPayload(tr, p)

	want := map[string]string{
		"uploaded":        "5.50 TB",
		"seed_size":       "800.13 GiB",
		"real_downloaded": "512.00 B",
		"downloaded":      "200.00 GB",
		"avg_seed_time":   "1D 1h",
		"total_seedtime":  "2Y 3M",
		"ratio":           "4.58",
		"future_stat":     "7",
	}
	if len(tr.ManualStats) != len(want) {
		t.Fatalf("manual stats = %#v, want %d entries", tr.ManualStats, len(want))
	}
	for k, w := range want {
		if got := tr.ManualStats[k]; got != w {
			t.Errorf("%s = %q, want %q", k, got, w)
		}
	}
}

// TestValidatePayloadRefusesBadShapes: a value that is not the shape its field
// needs is refused with the field named, and nothing is applied. Before this
// "This shouldn't be allowed" saved as Real Downloaded and "200" (200 what?)
// as an upload.
func TestValidatePayloadRefusesBadShapes(t *testing.T) {
	cases := []struct {
		name    string
		manual  map[string]string
		targets map[string]string
		wantErr string
	}{
		{"words in a size", map[string]string{"real_downloaded": "This shouldn't be allowed"}, nil, `manual stat "real_downloaded"`},
		{"size without a unit", map[string]string{"uploaded": "200"}, nil, "not a size"},
		{"nonsense duration", map[string]string{"avg_seed_time": "3 monkeys"}, nil, "not a duration"},
		{"words in a ratio", map[string]string{"ratio": "about four"}, nil, "not a number"},
		{"target size without a unit", nil, map[string]string{"seed_size": "5"}, `target "seed_size"`},
		{"target ratio words", nil, map[string]string{"ratio": "high"}, "not a number"},
		{"target age words", nil, map[string]string{"days": "a while"}, "not an account age"},
	}
	for _, c := range cases {
		p := trackerPayload{}
		if c.manual != nil {
			m := c.manual
			p.ManualStats = &m
		}
		if c.targets != nil {
			m := c.targets
			p.Targets = &m
		}
		err := validateTrackerPayload(&p)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want one containing %q", c.name, err, c.wantErr)
		}
	}
}

// TestValidatePayloadCanonicalisesTargets: targets take the same treatment.
// Sizes are canonical, the seconds/days the form has always sent stay as they
// are, and a duration or age typed straight into the API is converted to that
// form rather than kept as text the pacing code cannot read. Requirement
// counters and unknown keys pass through.
func TestValidatePayloadCanonicalisesTargets(t *testing.T) {
	targets := map[string]string{
		"uploaded":       "1.5 tib",
		"seed_size":      "5 TB", // a def's decimal label is kept as written
		"ratio":          "2.0",
		"avg_seed":       "7776000", // seconds, as the form sends
		"days":           "1y 6m",   // typed age → days (no minutes in an age)
		"count:Uploader": "3",       // requirement counter, untouched
		"something_new":  "whatever",
	}
	p := trackerPayload{Targets: &targets}
	if err := validateTrackerPayload(&p); err != nil {
		t.Fatalf("validate: %v", err)
	}
	want := map[string]string{
		"uploaded": "1.50 TiB", "seed_size": "5.00 TB", "ratio": "2.0",
		"avg_seed": "7776000", "days": "545", "count:Uploader": "3", "something_new": "whatever",
	}
	for k, w := range want {
		if got := (*p.Targets)[k]; got != w {
			t.Errorf("%s = %q, want %q", k, got, w)
		}
	}
	// A duration typed for avg_seed becomes seconds.
	targets = map[string]string{"avg_seed": "3M"}
	p = trackerPayload{Targets: &targets}
	if err := validateTrackerPayload(&p); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := (*p.Targets)["avg_seed"]; got != "7776000" {
		t.Errorf("avg_seed 3M = %q, want 7776000", got)
	}
}

// TestManualLayerJoinDateWins: join_date has its own dedicated input, so the
// value from that field must beat one typed into the stats list. Otherwise two
// controls would edit one value and the winner would depend on map ordering.
func TestManualLayerJoinDateWins(t *testing.T) {
	tr := models.Tracker{
		JoinDate:    "2020-01-01",
		ManualStats: map[string]string{"join_date": "1999-09-09", "ratio": "2.5"},
	}
	layer := tr.ManualLayer()
	if layer["join_date"] != "2020-01-01" {
		t.Errorf("join_date = %v, want the dedicated field's value", layer["join_date"])
	}
	if layer["ratio"] != "2.5" {
		t.Errorf("ratio = %v, want it carried through", layer["ratio"])
	}
}

// TestManualLayerEmptyClears: an empty layer is a real instruction — it is what
// clears the stats after the last row is removed. Returning something non-empty
// here (or nil-guarding at the call site) would leave deleted values standing.
func TestManualLayerEmptyClears(t *testing.T) {
	if layer := (models.Tracker{}).ManualLayer(); len(layer) != 0 {
		t.Errorf("empty tracker layer = %#v, want empty", layer)
	}
}

// TestJSONOKFailsLoudly: a value encoding/json cannot marshal must produce a
// real 500, not a 200 with an empty body.
//
// Encoding straight to the ResponseWriter sent the 200 header with the first
// byte, so a mid-encode failure left the client with a successful-looking
// empty response and the discarded error meant nothing reached the log. That
// combination hid issue #40 for days: the access log's only trace was a
// status of 0.
func TestJSONOKFailsLoudly(t *testing.T) {
	rec := httptest.NewRecorder()
	jsonOK(rec, map[string]float64{"ratio": math.Inf(1)})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "encoding_error") {
		t.Errorf("body = %q, want an encoding_error payload", rec.Body.String())
	}
}

// TestJSONOKWritesNormalValues guards the buffering rewrite: ordinary
// responses must be unchanged.
func TestJSONOKWritesNormalValues(t *testing.T) {
	rec := httptest.NewRecorder()
	jsonOK(rec, map[string]any{"ok": true, "n": 42})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not valid JSON: %v (%q)", err, rec.Body.String())
	}
	if got["ok"] != true || got["n"] != float64(42) {
		t.Errorf("body = %#v", got)
	}
}

// TestResolveLoginTime pins the three shapes of the "I've logged in" payload,
// and the future-rejection that keeps a mis-set clock from HIDING a warning.
func TestResolveLoginTime(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	str := func(s string) *string { return &s }

	// No body: the button's normal case records now.
	at, cleared, err := resolveLoginTime(loginPayload{}, now)
	if err != nil || cleared || at != "2026-09-02T12:00:00Z" {
		t.Errorf("empty payload = (%q, %v, %v), want now and no clear", at, cleared, err)
	}

	// Explicit empty string clears — the way back from a mistaken tap.
	at, cleared, err = resolveLoginTime(loginPayload{At: str("")}, now)
	if err != nil || !cleared || at != "" {
		t.Errorf("empty at = (%q, %v, %v), want a clear", at, cleared, err)
	}

	// A past timestamp is kept, normalised to UTC.
	at, _, err = resolveLoginTime(loginPayload{At: str("2026-08-30T01:12:50+02:00")}, now)
	if err != nil || at != "2026-08-29T23:12:50Z" {
		t.Errorf("past at = (%q, %v), want the UTC equivalent", at, err)
	}

	// Junk is rejected rather than silently becoming "now".
	if _, _, err = resolveLoginTime(loginPayload{At: str("yesterday")}, now); err == nil {
		t.Error("expected an error for an unparseable timestamp")
	}

	// The future is rejected: a login dated ahead pushes the deadline out and
	// suppresses the very warning this feature exists to give.
	if _, _, err = resolveLoginTime(loginPayload{At: str("2026-09-09T12:00:00Z")}, now); err == nil {
		t.Error("expected an error for a future timestamp")
	}
	// …but ordinary clock skew between browser and server passes.
	if _, _, err = resolveLoginTime(loginPayload{At: str("2026-09-02T12:00:30Z")}, now); err != nil {
		t.Errorf("30s of clock skew rejected: %v", err)
	}
}

// TestManualLayerCarriesRecordedLogin: the recorded login has to reach the
// manual stat layer, or the countdown it exists to drive never sees it.
func TestManualLayerCarriesRecordedLogin(t *testing.T) {
	tr := models.Tracker{
		LastLoginAt: "2026-08-30T00:00:00Z",
		JoinDate:    "2026-01-01",
		ManualStats: map[string]string{"ratio": "1.50"},
	}
	layer := tr.ManualLayer()
	if layer["last_login"] != "2026-08-30T00:00:00Z" {
		t.Errorf("last_login = %v, want the recorded login", layer["last_login"])
	}
	// The other manual sources must be untouched by the addition.
	if layer["join_date"] != "2026-01-01" || layer["ratio"] != "1.50" {
		t.Errorf("manual layer lost a field: %#v", layer)
	}
	// Never recorded = absent, not empty. An empty string would parse as no
	// timestamp anyway, but storing one puts a meaningless row in the layer.
	if _, ok := (models.Tracker{}).ManualLayer()["last_login"]; ok {
		t.Error("last_login present when nothing was ever recorded")
	}
}

// TestRecordLoginDecodesUnknownLengthBody: the body has to be read by DECODING
// it, never by trusting Content-Length. A chunked request reports -1, and the
// length check that preceded this read that as "no body" — so a clear
// ({"at":""}) silently recorded a login instead of removing one, the exact
// opposite of the request. An absent body must still mean "now".
func TestRecordLoginDecodesUnknownLengthBody(t *testing.T) {
	decode := func(body string, chunked bool) (loginPayload, error) {
		var p loginPayload
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
		if chunked {
			r.ContentLength = -1 // what a chunked request actually looks like
		}
		err := json.NewDecoder(r.Body).Decode(&p)
		if err != nil && errors.Is(err, io.EOF) {
			err = nil // empty body — the "record now" case
		}
		return p, err
	}

	// Chunked body carrying a clear must survive as a clear.
	p, err := decode(`{"at":""}`, true)
	if err != nil {
		t.Fatalf("chunked clear rejected: %v", err)
	}
	if p.At == nil || *p.At != "" {
		t.Fatalf("chunked clear decoded as %+v, want an empty At", p.At)
	}
	at, cleared, err := resolveLoginTime(p, time.Now())
	if err != nil || !cleared || at != "" {
		t.Errorf("chunked clear resolved to (%q, %v, %v), want a clear", at, cleared, err)
	}

	// No body at all still means "now", not an error.
	p, err = decode("", false)
	if err != nil {
		t.Fatalf("empty body rejected: %v", err)
	}
	if p.At != nil {
		t.Errorf("empty body decoded At = %v, want nil", *p.At)
	}

	// Malformed JSON is still rejected rather than silently becoming "now".
	if _, err = decode("{oops", false); err == nil {
		t.Error("expected malformed JSON to be rejected")
	}
}
