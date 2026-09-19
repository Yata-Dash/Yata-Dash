package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// quiFixture serves a qui that reports one unregistered and two tracker-down
// torrents across a tracker's mirror hosts, plus one unregistered torrent on
// a host that belongs to nobody. The counts block is what a real qui returns:
// instance-wide, and untouched by the filters.
func quiFixture(t *testing.T, down bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/instances/1/torrents" {
			http.NotFound(w, r)
			return
		}
		f := r.URL.Query().Get("filters")
		switch {
		case f == "":
			fmt.Fprint(w, `{"counts":{"status":{"unregistered":2,"tracker_down":2,"tracker_error":0,"errored":0},
				"trackerTransfers":{"ramjet.speedapp.io":{"totalSize":1000},"ramjet.speedapp.to":{"totalSize":1000}}}}`)
		case strings.Contains(f, "unregistered"):
			fmt.Fprint(w, `{"total":2,"torrents":[
				{"tracker":"https://ramjet.speedapp.io/announce/abc"},
				{"tracker":"https://tracker.nobody-knows.example/announce"}]}`)
		case strings.Contains(f, "tracker_down"):
			fmt.Fprint(w, `{"total":2,"torrents":[
				{"tracker":"https://ramjet.speedapp.io/announce/abc"},
				{"tracker":"https://ramjet.speedapp.to/announce/abc"}]}`)
		default:
			t.Errorf("unexpected filtered fetch for a class with a zero count: %s", f)
			fmt.Fprint(w, `{"total":0,"torrents":[]}`)
		}
	}))
}

func quiTracker(t *testing.T, d *Deps) models.Tracker {
	t.Helper()
	// speedapp's def lists its mirror domains, which is what makes the
	// grouping interesting.
	tr := models.Tracker{ID: "sa", Name: "SpeedApp", URL: "https://speedapp.io", Type: "custom", Enabled: true}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	return tr
}

func quiSettings(t *testing.T, d *Deps, url string) {
	t.Helper()
	s := d.Cfg.Settings()
	s.QUIURL, s.QUIAlertsEnabled, s.QUISeedsizeMode = url, true, "off"
	s.QUIEnabledInstances = []int{1}
	if err := d.Cfg.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
}

// Each torrent is counted once under its own announce host, so a tracker's
// mirrors add up rather than repeat; a host no tracker owns is dropped, not
// handed to whichever tracker happened to be nearest.
func TestQUIProblemCountsGroupByTracker(t *testing.T) {
	d := testDeps(t)
	tr := quiTracker(t, d)
	ts := quiFixture(t, false)
	defer ts.Close()
	quiSettings(t, d, ts.URL)

	refreshQUI(d)

	merged, err := d.Stats.Merged(tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"qui_unregistered": 1, "qui_tracker_down": 2, "qui_tracker_error": 0, "qui_errored": 0}
	for field, n := range want {
		f, ok := merged[field]
		if !ok {
			t.Errorf("%s missing from merged stats (seedsize mode is off — the counts must still come through)", field)
			continue
		}
		if fmt.Sprint(f.Value) != fmt.Sprint(n) {
			t.Errorf("%s = %v, want %d", field, f.Value, n)
		}
	}
	// Seed size stays out: the mode is off, and that is a different decision.
	if _, ok := merged["seed_size"]; ok {
		t.Error("seed_size leaked into the merge with the seedsize mode off")
	}
}

// qui unreachable → the fields are absent, so a rule on them goes quiet. Not
// zero: "nothing wrong" and "could not look" are different facts, and the
// second must never read as the first.
func TestQUIProblemCountsAreAbsentWhenQUIIsDown(t *testing.T) {
	d := testDeps(t)
	tr := quiTracker(t, d)
	up := quiFixture(t, false)
	quiSettings(t, d, up.URL)
	refreshQUI(d)
	up.Close()

	down := quiFixture(t, true)
	defer down.Close()
	quiSettings(t, d, down.URL)
	refreshQUI(d)

	merged, _ := d.Stats.Merged(tr.ID)
	// The previous counts survive a down poll — they were true when read and
	// nothing newer contradicts them — rather than being wiped or zeroed.
	if f, ok := merged["qui_tracker_down"]; !ok || fmt.Sprint(f.Value) != "2" {
		t.Errorf("qui_tracker_down = %v after a failed poll, want the previous 2 kept", merged["qui_tracker_down"])
	}

	// A tracker never counted at all has nothing, so a rule never matches.
	other := models.Tracker{ID: "o", Name: "Other", URL: "https://other.example", Type: "unit3d", Enabled: true}
	_ = d.Cfg.AddTracker(other)
	merged, _ = d.Stats.Merged(other.ID)
	if _, ok := merged["qui_unregistered"]; ok {
		t.Error("a tracker qui never answered for must not carry a count")
	}
}

// A quiet week with torrents sitting unregistered is not quiet. The short
// form keeps its one line and gains the outstanding work; a tracker line in
// the full digest carries its standing counts, never a delta.
func TestDigestReportsStandingQUICounts(t *testing.T) {
	d := testDeps(t)
	tr := quiTracker(t, d)
	ts := quiFixture(t, false)
	defer ts.Close()
	quiSettings(t, d, ts.URL)
	refreshQUI(d)

	text, _ := buildDigest(d, time.Now())
	if !strings.HasPrefix(text, "All quiet this week") {
		t.Fatalf("expected the quiet form (no history at all), got: %s", text)
	}
	if !strings.Contains(text, "Needs attention: 1 unregistered (SpeedApp 1), 2 tracker down (SpeedApp 2).") {
		t.Errorf("quiet week hides outstanding qui work: %s", text)
	}
	merged, _ := d.Stats.Merged(tr.ID)
	if frag := quiDigestFragment(merged); frag != "1 unregistered, 2 tracker down" {
		t.Errorf("tracker fragment = %q", frag)
	}
	if frag := quiDigestFragment(models.MergedStats{}); frag != "" {
		t.Errorf("a tracker without counts must add nothing, got %q", frag)
	}
}
