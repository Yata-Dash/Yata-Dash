package api

// History series endpoint — the data feed for the History/Growth view
// (HISTORY_VIEW_PLAN.md §3.2). Purely additive: /api/history (sparklines)
// is untouched, and nothing calls this until the view ships.

import (
	"net/http"
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// seriesRange describes the window the response covers.
type seriesRange struct {
	From        int64  `json:"from"`
	To          int64  `json:"to"`
	Granularity string `json:"granularity"` // fine | daily
}

// historySeries is one tracker/field line: lean [unixSec, value] tuples so
// long ranges with many trackers stay a small payload.
type historySeries struct {
	TrackerID string       `json:"tracker_id"`
	Field     string       `json:"field"`
	Unit      string       `json:"unit"` // GiB | count | ratio | seconds — drives axis formatting
	Points    [][2]float64 `json:"points"`
}

// rangeWindows maps the supported range keys to a lookback duration.
// 0 = everything retained ("all").
var rangeWindows = map[string]time.Duration{
	"48h":  48 * time.Hour,
	"7d":   7 * 24 * time.Hour,
	"14d":  14 * 24 * time.Hour,
	"30d":  30 * 24 * time.Hour,
	"90d":  90 * 24 * time.Hour,
	"365d": 365 * 24 * time.Hour,
	"all":  0,
}

// fieldUnit classifies a recorded field for axis formatting (mirrors the
// units used by stats.RecordHistory's extractors).
func fieldUnit(field string) string {
	switch field {
	case "uploaded", "downloaded", "buffer", "seed_size":
		return "GiB"
	case "ratio":
		return "ratio"
	case "avg_seed_time":
		return "seconds"
	default:
		return "count"
	}
}

// csvParam splits a comma-separated query param into trimmed non-empty parts.
func csvParam(r *http.Request, key string) []string {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// GET /api/history/series?trackers=a,b&fields=uploaded,ratio&range=90d&granularity=auto
//
// Returns per-tracker per-field series over the requested window. The server
// picks the table: fine points (5-min cadence, 14-day retention) for short
// ranges, daily rollups beyond — so payloads stay bounded however long the
// range. Omitted trackers/fields = no filter (all recorded).
func getHistorySeries(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rangeKey := r.URL.Query().Get("range")
		window, ok := rangeWindows[rangeKey]
		if !ok {
			rangeKey, window = "30d", rangeWindows["30d"]
		}

		now := time.Now().UTC()
		since := time.Unix(0, 0)
		if window > 0 {
			since = now.Add(-window)
		}

		// fine only has 14 days — auto uses it for short ranges (intraday
		// smoothness), daily beyond (long-range trends).
		gran := r.URL.Query().Get("granularity")
		if gran != "fine" && gran != "daily" {
			gran = "daily"
			if window > 0 && window <= 14*24*time.Hour {
				gran = "fine"
			}
		}

		trackers := csvParam(r, "trackers")
		fields := csvParam(r, "fields")

		var points []store.HistoryPoint
		var err error
		if gran == "fine" {
			points, err = d.DB.SeriesFine(trackers, fields, since)
		} else {
			points, err = d.DB.SeriesDaily(trackers, fields, since)
		}
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}

		// Group into series. Points arrive oldest-first, so each series'
		// tuple list is already sorted.
		byKey := map[[2]string]*historySeries{}
		var order [][2]string
		for _, p := range points {
			k := [2]string{p.TrackerID, p.Field}
			s := byKey[k]
			if s == nil {
				s = &historySeries{
					TrackerID: p.TrackerID,
					Field:     p.Field,
					Unit:      fieldUnit(p.Field),
					Points:    [][2]float64{},
				}
				byKey[k] = s
				order = append(order, k)
			}
			s.Points = append(s.Points, [2]float64{float64(p.RecordedAt), p.Value})
		}
		series := make([]*historySeries, 0, len(order))
		for _, k := range order {
			series = append(series, byKey[k])
		}

		jsonOK(w, map[string]any{
			"range": seriesRange{
				From:        since.Unix(),
				To:          now.Unix(),
				Granularity: gran,
			},
			"series": series,
		})
	}
}
