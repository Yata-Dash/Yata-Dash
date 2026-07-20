// Package stats is the unified stats engine. It stores two layers per
// tracker — "api" and "scrape" — and merges them on read with one rule:
//
//	API always wins. Scrape only fills fields the API left absent or zero.
//
// This replaces v1's two-cache split (statsCache vs profileCache) that caused
// the same stat (e.g. bonus points) to show different values in different
// parts of the UI. The merge happens server-side; the frontend receives one
// combined map with per-field provenance.
package stats

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// Engine persists stat layers and produces merged views.
type Engine struct {
	DB *store.DB
}

// New creates an Engine.
func New(db *store.DB) *Engine { return &Engine{DB: db} }

// SaveAPI replaces the API layer for a tracker with a fresh fetch result.
// Meta keys (prefixed "_") are stripped.
func (e *Engine) SaveAPI(trackerID string, data map[string]any) error {
	return e.DB.ReplaceLayer(trackerID, string(models.SourceAPI), stripMeta(data), time.Now().UTC())
}

// SaveScrape replaces the scrape layer for a tracker with a fresh scrape result.
func (e *Engine) SaveScrape(trackerID string, data map[string]any) error {
	return e.DB.ReplaceLayer(trackerID, string(models.SourceScrape), stripMeta(data), time.Now().UTC())
}

// SaveManual replaces the manual (user-entered) layer. Pass an empty map to
// clear it. These values have the lowest merge priority — they only fill
// fields that neither the API nor a scrape provides (e.g. a join date the
// tracker's API never returns).
func (e *Engine) SaveManual(trackerID string, data map[string]any) error {
	return e.DB.ReplaceLayer(trackerID, string(models.SourceManual), stripMeta(data), time.Now().UTC())
}

func stripMeta(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for k, v := range data {
		if strings.HasPrefix(k, "_") || v == nil {
			continue
		}
		out[k] = v
	}
	return out
}

// Merged returns the unified stats view for a tracker.
func (e *Engine) Merged(trackerID string) (models.MergedStats, error) {
	layers, err := e.DB.Layers(trackerID)
	if err != nil {
		return nil, err
	}
	out := models.MergedStats{}
	// Priority order: api > scrape > manual. Each layer only fills fields a
	// higher-priority layer left empty (or zero-ish).
	for _, src := range []models.Source{models.SourceAPI, models.SourceScrape, models.SourceManual} {
		for field, fv := range layers[string(src)] {
			if !meaningful(fv.Value) {
				continue
			}
			if existing, ok := out[field]; ok && meaningful(existing.Value) {
				continue // a higher-priority layer already supplied this field
			}
			out[field] = models.StatField{Value: fv.Value, Source: src, UpdatedAt: fv.UpdatedAt}
		}
	}
	return out, nil
}

// meaningful reports whether a stored value carries real data. Empty strings
// and zero-ish placeholders don't beat a value from the other layer.
func meaningful(v any) bool {
	switch val := v.(type) {
	case nil:
		return false
	case string:
		s := strings.TrimSpace(val)
		return s != "" && s != "0" && s != "0 B" && s != "0.00 B" && s != "—"
	default:
		return true
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// History
// ─────────────────────────────────────────────────────────────────────────────

// numericExtractors defines which merged fields are recorded as history and
// how each is converted to a number. Sizes are recorded in GiB, durations in
// seconds.
var numericExtractors = map[string]func(any) (float64, bool){
	"uploaded":         sizeGiB,
	"downloaded":       sizeGiB,
	"buffer":           sizeGiB,
	"seed_size":        sizeGiB,
	"ratio":            numeric,
	"seeding":          numeric,
	"leeching":         numeric,
	"hit_and_runs":     numeric,
	"bonus_points":     numeric,
	"uploads_approved": numeric,
	"avg_seed_time":    duration,
}

func sizeGiB(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	g := parse.SizeToGiB(s)
	if g == nil {
		return 0, false
	}
	return *g, true
}

func numeric(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case string:
		f := parse.AnyFloat(val)
		return f, strings.TrimSpace(val) != ""
	}
	return 0, false
}

func duration(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, val > 0
	case string:
		sec := parse.SeedTimeToSeconds(val)
		if sec == nil {
			return 0, false
		}
		return *sec, true
	}
	return 0, false
}

// NumericSnapshot converts a merged view's recordable fields to numbers using
// the same rules as history (sizes in GiB, durations in seconds). Shared by
// history recording and the read-only /api/summary endpoint so integrations
// see exactly the numbers the charts are built from.
func NumericSnapshot(merged models.MergedStats) map[string]float64 {
	fields := map[string]float64{}
	for field, extract := range numericExtractors {
		sf, ok := merged[field]
		if !ok {
			continue
		}
		if n, ok := extract(sf.Value); ok {
			fields[field] = n
		}
	}
	return fields
}

// RecordHistory snapshots the numeric fields of a merged view into both the
// fine-grained history table (48h sparklines) and the daily rollup table
// (stable long-term growth rates for trend projections).
func (e *Engine) RecordHistory(trackerID string, merged models.MergedStats) error {
	fields := NumericSnapshot(merged)
	if len(fields) == 0 {
		return nil
	}
	now := time.Now().UTC()
	if err := e.DB.AddHistory(trackerID, now, fields); err != nil {
		return err
	}
	return e.DB.RecordDaily(trackerID, now, fields)
}

// ─────────────────────────────────────────────────────────────────────────────
// Growth rates (per-day) — the single source of trend projections, shared by
// the Pathways view and the dashboard target ETAs.
// ─────────────────────────────────────────────────────────────────────────────

const (
	// rateDailyWindowDays: how far back the stable (daily-rollup) rate looks.
	// Averages over up to two weeks so one slow day can't skew the estimate.
	rateDailyWindowDays = 14
	// rateFineWindowDays / rateFineMinSpanSec: early fallback before two daily
	// rollups exist, so a fresh tracker still projects within a few hours.
	rateFineWindowDays = 7
	rateFineMinSpanSec = 3 * 3600

	// bufferNoiseFloor / ratioNoiseFloor: minimum |rate| considered real
	// movement rather than measurement noise, for the two signed-rate fields.
	// Ratio moves in hundredths, not GiB, so it needs a much finer floor.
	bufferNoiseFloor = 0.01
	ratioNoiseFloor  = 0.001

	// dropWindowDays / dropMinSpanSec: the standing-guard drop-% window — a
	// stat's oldest point within the last week vs its newest, requiring at
	// least a day of span so a young tracker stays silent instead of noisy.
	dropWindowDays = 7
	dropMinSpanSec = 24 * 3600
)

// GrowthRates returns per-day growth for projectable fields (uploaded,
// downloaded, buffer, seed_size in GiB; bonus_points, uploads_approved raw).
// Prefers the daily-rollup average; falls back to fine history for a
// brand-new tracker. A field with no (positive) measurable growth is omitted —
// a flat stat can't be projected. Buffer is the one signed exception: it can
// legitimately shrink (heavy downloading), and a negative rate is exactly what
// the hover should show; it is never used for ETA projection.
func (e *Engine) GrowthRates(trackerID string) map[string]float64 {
	now := time.Now().UTC()
	daily, _ := e.DB.DailySince(trackerID, now.AddDate(0, 0, -rateDailyWindowDays))
	fine, _ := e.DB.TrackerHistorySince(trackerID, now.Add(-rateFineWindowDays*24*time.Hour))
	byDay := groupByField(daily)
	byFine := groupByField(fine)

	out := map[string]float64{}
	for _, f := range []string{"uploaded", "downloaded", "seed_size", "bonus_points", "uploads_approved"} {
		// Daily rollups need a ≥1-day span; fine history a ≥3h span.
		if r, ok := rateFromPoints(byDay[f], 86400); ok {
			out[f] = r
		} else if r, ok := rateFromPoints(byFine[f], rateFineMinSpanSec); ok {
			out[f] = r
		}
	}
	if r, ok := signedRatePerDay(byDay, byFine, "buffer", bufferNoiseFloor); ok {
		out["buffer"] = r
	}
	return out
}

// DeclineSignals are per-day/windowed decline measurements for the alert
// engine's standing guards. Nil pointer = no measurable signal (not enough
// history, or the stat isn't moving in the guarded direction).
type DeclineSignals struct {
	RatioPerDay       *float64 // signed ratio change per day (14d daily window, fine fallback)
	BufferPerDay      *float64 // signed GiB/day (same computation GrowthRates uses)
	SeedSizeDrop7dPct *float64 // % drop vs the oldest point in the last 7d (>=24h span required)
	SeedingDrop7dPct  *float64 // same for the seeding count
}

// DeclineSignals computes the standing-guard trend signals from the same two
// history queries GrowthRates uses (one DailySince + one TrackerHistorySince,
// grouped by field once) so a rule evaluation pass never issues a query per
// signal.
func (e *Engine) DeclineSignals(trackerID string) DeclineSignals {
	now := time.Now().UTC()
	daily, _ := e.DB.DailySince(trackerID, now.AddDate(0, 0, -rateDailyWindowDays))
	fine, _ := e.DB.TrackerHistorySince(trackerID, now.Add(-rateFineWindowDays*24*time.Hour))
	byDay := groupByField(daily)
	byFine := groupByField(fine)

	var out DeclineSignals
	if r, ok := signedRatePerDay(byDay, byFine, "ratio", ratioNoiseFloor); ok {
		out.RatioPerDay = &r
	}
	if r, ok := signedRatePerDay(byDay, byFine, "buffer", bufferNoiseFloor); ok {
		out.BufferPerDay = &r
	}

	// Truncate to UTC midnight before subtracting whole days: history_daily
	// buckets are stored at UTC midnight (see utcDay), so comparing against a
	// plain "now minus 7×24h" instant would inconsistently include/exclude the
	// 7-day-old bucket depending on what time of day it is right now.
	dropSince := now.Truncate(24*time.Hour).AddDate(0, 0, -dropWindowDays)
	if p, ok := dropPct(windowed(byDay["seed_size"], dropSince), byFine["seed_size"]); ok {
		out.SeedSizeDrop7dPct = &p
	}
	if p, ok := dropPct(windowed(byDay["seeding"], dropSince), byFine["seeding"]); ok {
		out.SeedingDrop7dPct = &p
	}
	return out
}

// windowed filters an oldest-first point slice down to those recorded at or
// after since.
func windowed(points []store.HistoryPoint, since time.Time) []store.HistoryPoint {
	cut := since.Unix()
	for i, p := range points {
		if p.RecordedAt >= cut {
			return points[i:]
		}
	}
	return nil
}

// dropPct computes a decline % from oldest→newest, preferring the (already
// 7d-windowed) daily points and falling back to fine history for a tracker
// too young to have daily rollups yet.
func dropPct(daily, fine []store.HistoryPoint) (float64, bool) {
	if p, ok := dropPctFromPoints(daily); ok {
		return p, true
	}
	return dropPctFromPoints(fine)
}

// dropPctFromPoints is dropPct's single-slice math: baseline (oldest) must be
// positive and the span must cover at least a day, or the tracker is too
// young/thin to trust. Growth (a negative "drop") is nil, never a negative
// percentage.
func dropPctFromPoints(points []store.HistoryPoint) (float64, bool) {
	if len(points) < 2 {
		return 0, false
	}
	first, last := points[0], points[len(points)-1]
	if last.RecordedAt-first.RecordedAt < dropMinSpanSec || first.Value <= 0 {
		return 0, false
	}
	drop := (first.Value - last.Value) / first.Value * 100
	return drop, drop > 0
}

func groupByField(points []store.HistoryPoint) map[string][]store.HistoryPoint {
	out := map[string][]store.HistoryPoint{}
	for _, p := range points {
		out[p.Field] = append(out[p.Field], p)
	}
	return out
}

// rateFromPoints computes per-day growth from the oldest and newest points.
// Points are assumed ordered oldest→newest. Returns ok=false when the span is
// under minSpanSec or growth is non-positive (a flat/declining stat).
func rateFromPoints(points []store.HistoryPoint, minSpanSec int64) (float64, bool) {
	r, ok := spanRate(points, minSpanSec)
	return r, ok && r > 0
}

// signedRateFromPoints is the signed-rate variant for fields that can
// legitimately shrink (buffer, ratio): negative rates pass through, only
// near-flat movement (|r| < floor — measurement noise) is omitted.
func signedRateFromPoints(points []store.HistoryPoint, minSpanSec int64, floor float64) (float64, bool) {
	r, ok := spanRate(points, minSpanSec)
	return r, ok && math.Abs(r) >= floor
}

// signedRatePerDay is the shared daily-preferred/fine-fallback signed-rate
// lookup: GrowthRates' buffer entry and DeclineSignals' ratio/buffer signals
// both project a field's per-day trend the same way, only the noise floor
// differs.
func signedRatePerDay(byDay, byFine map[string][]store.HistoryPoint, field string, floor float64) (float64, bool) {
	if r, ok := signedRateFromPoints(byDay[field], 86400, floor); ok {
		return r, true
	}
	return signedRateFromPoints(byFine[field], rateFineMinSpanSec, floor)
}

// spanRate is the shared oldest→newest per-day delta (no sign filtering).
func spanRate(points []store.HistoryPoint, minSpanSec int64) (float64, bool) {
	if len(points) < 2 {
		return 0, false
	}
	first, last := points[0], points[len(points)-1]
	span := last.RecordedAt - first.RecordedAt
	if span < minSpanSec {
		return 0, false
	}
	return (last.Value - first.Value) / (float64(span) / 86400), true
}

// PruneHistory removes points older than the retention window.
func (e *Engine) PruneHistory(retention time.Duration) error {
	return e.DB.PruneHistory(time.Now().UTC().Add(-retention))
}

// String renders a stat value for logs/debugging.
func String(v any) string { return fmt.Sprintf("%v", v) }
