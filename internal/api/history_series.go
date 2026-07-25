package api

// History series endpoint — the data feed for the History/Growth view
// (HISTORY_VIEW_PLAN.md §3.2), the top aggregate cards, and the Tracker
// Detail page. The legacy /api/history list endpoint has been retired.

import (
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// uptimeField is a SYNTHETIC series: daily connection uptime, assembled here
// from connection_daily rather than read from history_daily like every other
// field. Keeping it out of history_daily is deliberate — that table's field
// names populate the stat lists, target pickers and alert conditions, and
// "uptime" belongs in none of them. So it is only ever emitted when a caller
// asks for it by name; an unfiltered request ("every recorded field") means
// recorded stats, not this.
const uptimeField = "uptime"

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
	case uptimeField:
		return "percent"
	default:
		return "count"
	}
}

// uptimeSeries builds one percent-valued series per tracker from the daily
// connection rollups, oldest first.
//
// Days with NO contact attempt are left out rather than plotted as 0: a paused
// tracker, or one added mid-window, has no uptime to report, and drawing that
// as a 0% day would invent an outage that never happened. The gap in the line
// says "not contacted" — which is the truth.
//
// Granularity is ignored: connection_daily is per-UTC-day and there is no finer
// record, so a 48h range shows two points rather than a smoother version of the
// same thing.
func uptimeSeries(d *Deps, trackerIDs []string, since time.Time) []*historySeries {
	days, err := d.DB.ConnectionDaily(trackerIDs, since)
	if err != nil {
		return nil
	}
	byID := map[string]*historySeries{}
	var order []string
	for _, c := range days {
		u := c.Uptime()
		if u < 0 {
			continue
		}
		s := byID[c.TrackerID]
		if s == nil {
			s = &historySeries{
				TrackerID: c.TrackerID,
				Field:     uptimeField,
				Unit:      fieldUnit(uptimeField),
				Points:    [][2]float64{},
			}
			byID[c.TrackerID] = s
			order = append(order, c.TrackerID)
		}
		// c.Day is UTC midnight, so the point lands at the start of the day it
		// summarises — the same convention the daily stat rollups use.
		s.Points = append(s.Points, [2]float64{float64(c.Day), u * 100})
	}
	out := make([]*historySeries, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
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
			// Guards against +Inf/NaN rows written before the NumericSnapshot
			// sanitization existed (a downloaded=0 ratio) — undecodable by
			// json.Encode, so a single old row would 500 the whole response.
			if math.IsInf(p.Value, 0) || math.IsNaN(p.Value) {
				continue
			}
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

		// Connection uptime rides along only when named — see uptimeField.
		if slices.Contains(fields, uptimeField) {
			series = append(series, uptimeSeries(d, trackers, since)...)
		}

		// Point-in-time events (group changes, connection up/down) for the same
		// trackers/window — the History view draws these as timeline markers and
		// the Detail page lists them.
		events, _ := d.DB.EventsSince(trackers, since)
		if events == nil {
			events = []store.TrackerEvent{}
		}

		jsonOK(w, map[string]any{
			"range": seriesRange{
				From:        since.Unix(),
				To:          now.Unix(),
				Granularity: gran,
			},
			"series": series,
			"events": events,
		})
	}
}
