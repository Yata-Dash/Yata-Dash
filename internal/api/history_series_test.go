package api

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"
	"time"
)

type seriesResp struct {
	Range struct {
		From        int64  `json:"from"`
		To          int64  `json:"to"`
		Granularity string `json:"granularity"`
	} `json:"range"`
	Series []struct {
		TrackerID string       `json:"tracker_id"`
		Field     string       `json:"field"`
		Unit      string       `json:"unit"`
		Points    [][2]float64 `json:"points"`
	} `json:"series"`
}

func callSeries(t *testing.T, d *Deps, query string) seriesResp {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/history/series"+query, nil)
	rec := httptest.NewRecorder()
	getHistorySeries(d)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var out seriesResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// TestHistorySeriesEndpoint seeds fine + daily points and asserts the
// automatic granularity selection, the tracker/field filtering, and the
// series-oriented tuple payload shape.
func TestHistorySeriesEndpoint(t *testing.T) {
	d := testDeps(t)
	now := time.Now().UTC()

	// 20 days of daily rollups + 2 days of fine points for two trackers.
	for day := 20; day >= 1; day-- {
		at := now.Add(-time.Duration(day) * 24 * time.Hour)
		for _, tr := range []string{"tr-a", "tr-b"} {
			fields := map[string]float64{"uploaded": float64(100 - day), "ratio": 2.5}
			if err := d.DB.RecordDaily(tr, at, fields); err != nil {
				t.Fatal(err)
			}
			if day <= 2 {
				if err := d.DB.AddHistory(tr, at, fields); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	// Short range → fine granularity, both trackers, one field filter.
	fine := callSeries(t, d, "?range=48h&fields=uploaded")
	if fine.Range.Granularity != "fine" {
		t.Errorf("48h granularity = %s, want fine", fine.Range.Granularity)
	}
	if len(fine.Series) != 2 {
		t.Fatalf("48h series = %d, want 2 (one per tracker)", len(fine.Series))
	}
	for _, s := range fine.Series {
		if s.Field != "uploaded" || s.Unit != "GiB" {
			t.Errorf("series %s: field=%s unit=%s, want uploaded/GiB", s.TrackerID, s.Field, s.Unit)
		}
	}

	// Long range → daily granularity; tracker filter; ~20 daily points.
	daily := callSeries(t, d, "?range=90d&trackers=tr-a&fields=uploaded")
	if daily.Range.Granularity != "daily" {
		t.Errorf("90d granularity = %s, want daily", daily.Range.Granularity)
	}
	if len(daily.Series) != 1 || daily.Series[0].TrackerID != "tr-a" {
		t.Fatalf("90d series = %+v, want single tr-a series", daily.Series)
	}
	pts := daily.Series[0].Points
	if len(pts) < 19 || len(pts) > 21 {
		t.Errorf("90d points = %d, want ~20", len(pts))
	}
	// Oldest-first tuples with rising values.
	for i := 1; i < len(pts); i++ {
		if pts[i][0] <= pts[i-1][0] {
			t.Fatal("points not oldest-first")
		}
		if pts[i][1] < pts[i-1][1] {
			t.Fatal("seeded uploaded values should rise")
		}
	}

	// ratio unit classification + explicit granularity override.
	ratio := callSeries(t, d, "?range=7d&granularity=daily&fields=ratio&trackers=tr-b")
	if ratio.Range.Granularity != "daily" {
		t.Errorf("explicit granularity ignored: %s", ratio.Range.Granularity)
	}
	if len(ratio.Series) != 1 || ratio.Series[0].Unit != "ratio" {
		t.Fatalf("ratio series = %+v, want unit ratio", ratio.Series)
	}

	// Unknown range key falls back to the 30d default (daily).
	def := callSeries(t, d, "?range=bogus")
	if def.Range.Granularity != "daily" {
		t.Errorf("default granularity = %s, want daily", def.Range.Granularity)
	}
	if got := def.Range.To - def.Range.From; got < 29*86400 || got > 31*86400 {
		t.Errorf("default window = %ds, want ~30d", got)
	}
}

// TestHistorySeriesUptime: the synthetic uptime series is assembled from the
// connection rollups, arrives as a percentage, and — crucially — only when the
// caller names the field. Everything else in the app asks for "the recorded
// fields", and uptime is not one of them.
func TestHistorySeriesUptime(t *testing.T) {
	d := testDeps(t)
	now := time.Now().UTC()
	day := func(n int) time.Time { return now.Add(-time.Duration(n) * 24 * time.Hour) }

	// 3 days back: two contacts, one failed → 50%.
	if err := d.DB.RecordConnection("tr-a", day(3), true, "", "api"); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.RecordConnection("tr-a", day(3), false, "timeout", "api"); err != nil {
		t.Fatal(err)
	}
	// 2 days back: nothing attempted at all — no row, so no point (see below).
	// 1 day back: one contact, succeeded → 100%.
	if err := d.DB.RecordConnection("tr-a", day(1), true, "", "api"); err != nil {
		t.Fatal(err)
	}
	// A second tracker so the per-tracker split is exercised.
	if err := d.DB.RecordConnection("tr-b", day(1), false, "http_500", "scrape"); err != nil {
		t.Fatal(err)
	}

	// Not requested → absent, even though the rollups exist.
	plain := callSeries(t, d, "?range=30d&fields=uploaded")
	for _, s := range plain.Series {
		if s.Field == "uptime" {
			t.Fatalf("uptime series returned without being asked for: %+v", s)
		}
	}
	// An unfiltered request means "every recorded stat", which uptime is not.
	all := callSeries(t, d, "?range=30d")
	for _, s := range all.Series {
		if s.Field == "uptime" {
			t.Fatalf("uptime series leaked into an unfiltered request: %+v", s)
		}
	}

	resp := callSeries(t, d, "?range=30d&fields=uptime")
	if len(resp.Series) != 2 {
		t.Fatalf("series = %d, want 2 (one per tracker): %+v", len(resp.Series), resp.Series)
	}
	byID := map[string][][2]float64{}
	for _, s := range resp.Series {
		if s.Field != "uptime" || s.Unit != "percent" {
			t.Errorf("series %s: field=%s unit=%s, want uptime/percent", s.TrackerID, s.Field, s.Unit)
		}
		byID[s.TrackerID] = s.Points
	}

	a := byID["tr-a"]
	// Two points, not three: the untouched day contributes nothing rather than
	// a fabricated 0%, so a gap in the line reads as "not contacted".
	if len(a) != 2 {
		t.Fatalf("tr-a points = %+v, want 2 (the no-contact day omitted)", a)
	}
	if a[0][1] != 50 {
		t.Errorf("tr-a day -3 = %v%%, want 50", a[0][1])
	}
	if a[1][1] != 100 {
		t.Errorf("tr-a day -1 = %v%%, want 100", a[1][1])
	}
	if a[1][0] <= a[0][0] {
		t.Error("uptime points not oldest-first")
	}
	if b := byID["tr-b"]; len(b) != 1 || b[0][1] != 0 {
		t.Errorf("tr-b points = %+v, want a single 0%% day", b)
	}

	// Tracker filter applies to the synthetic series too.
	one := callSeries(t, d, "?range=30d&fields=uptime&trackers=tr-b")
	if len(one.Series) != 1 || one.Series[0].TrackerID != "tr-b" {
		t.Errorf("filtered uptime = %+v, want tr-b only", one.Series)
	}
}

// TestHistorySeriesSkipsNonFiniteRows: a +Inf row (a downloaded=0 ratio,
// recorded before NumericSnapshot filtered non-finite values) must not break
// json.Encode for the rest of the response — the point is dropped, the finite
// points around it still come through.
func TestHistorySeriesSkipsNonFiniteRows(t *testing.T) {
	d := testDeps(t)
	now := time.Now().UTC()

	if err := d.DB.AddHistory("tr-c", now.Add(-2*time.Hour), map[string]float64{"ratio": 2.0}); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.AddHistory("tr-c", now.Add(-time.Hour), map[string]float64{"ratio": math.Inf(1)}); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.AddHistory("tr-c", now, map[string]float64{"ratio": 3.0}); err != nil {
		t.Fatal(err)
	}

	resp := callSeries(t, d, "?range=48h&trackers=tr-c&fields=ratio")
	if len(resp.Series) != 1 {
		t.Fatalf("series = %d, want 1", len(resp.Series))
	}
	pts := resp.Series[0].Points
	if len(pts) != 2 {
		t.Fatalf("points = %d, want 2 (the +Inf row skipped)", len(pts))
	}
	for _, p := range pts {
		if math.IsInf(p[1], 0) || math.IsNaN(p[1]) {
			t.Errorf("non-finite value leaked into response: %v", p)
		}
	}
}
