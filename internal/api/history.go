package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

func registerHistory(r chi.Router, d *Deps) {
	r.Get("/history", getHistory(d))
	// History view data feed (additive — see history_series.go). Inert until
	// the flag-gated History view ships; harmless to have mounted.
	r.Get("/history/series", getHistorySeries(d))
}

// GET /api/history?hours=48 — numeric history points for all trackers,
// oldest first. The frontend groups them into per-tracker per-field series.
func getHistory(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hours := 48
		if h := r.URL.Query().Get("hours"); h != "" {
			if v, err := strconv.Atoi(h); err == nil && v > 0 && v <= 24*14 {
				hours = v
			}
		}
		points, err := d.DB.HistorySince(time.Now().UTC().Add(-time.Duration(hours) * time.Hour))
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		if points == nil {
			points = []store.HistoryPoint{} // never null in JSON
		}
		jsonOK(w, points)
	}
}
