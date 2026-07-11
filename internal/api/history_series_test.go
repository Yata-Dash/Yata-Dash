package api

import (
	"encoding/json"
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
