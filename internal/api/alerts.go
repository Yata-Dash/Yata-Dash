package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

func registerAlerts(r chi.Router, d *Deps) {
	r.Get("/alerts", listAlerts(d))
	r.Post("/alerts/read", markAlertsRead(d))
	r.Delete("/alerts/{id}", deleteAlert(d))
}

// alertRecorder adapts the store to notify.Recorder. It lives here rather than
// in the store so the store keeps no opinion about alerts being a notification
// channel, and so a write failure is logged where the app's logger is.
type alertRecorder struct{ d *Deps }

// NewAlertRecorder returns the recorder to hand the alert engine.
func NewAlertRecorder(d *Deps) interface {
	RecordAlert(ruleID, ruleName, trackerID, trackerName, title, body string)
} {
	return alertRecorder{d}
}

func (a alertRecorder) RecordAlert(ruleID, ruleName, trackerID, trackerName, title, body string) {
	if a.d.DB == nil {
		return
	}
	err := a.d.DB.AddAlert(store.Alert{
		At: time.Now().UTC().Unix(), RuleID: ruleID, RuleName: ruleName,
		TrackerID: trackerID, TrackerName: trackerName, Title: title, Body: body,
	})
	if err != nil {
		a.d.logWarnf("alerts: recording %q failed: %v", ruleName, err)
	}
}

type alertsResponse struct {
	Alerts []store.Alert `json:"alerts"`
	Total  int           `json:"total"`
	Unread int           `json:"unread"`
	// Sources is every origin that has ever raised an alert — unfiltered, like
	// Unread, because the filter control has to describe the whole set rather
	// than the view it is currently producing.
	Sources []store.AlertSource `json:"sources"`
}

// GET /api/alerts — the panel's list, newest first.
//
// `unread` is always the unfiltered count, because it drives the header bubble:
// filtering the list must not change how many alerts the user is told they
// have unread.
func listAlerts(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		alerts, total, err := d.DB.Alerts(store.AlertQuery{
			Search:    q.Get("q"),
			TrackerID: q.Get("tracker"),
			Limit:     limit,
			Offset:    offset,
		})
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		unread, err := d.DB.UnreadAlerts()
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		sources, err := d.DB.AlertSources()
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		jsonOK(w, alertsResponse{Alerts: alerts, Total: total, Unread: unread, Sources: sources})
	}
}

// POST /api/alerts/read — mark alerts read. An empty/absent body marks every
// unread alert read, which is what "mark all read" sends.
func markAlertsRead(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			IDs []int64 `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil && !errors.Is(err, io.EOF) {
			jsonError(w, "bad_request", http.StatusBadRequest)
			return
		}
		if err := d.DB.MarkAlertsRead(p.IDs, time.Now().UTC()); err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		unread, _ := d.DB.UnreadAlerts()
		jsonOK(w, map[string]any{"unread": unread})
	}
}

// DELETE /api/alerts/{id} — clear one row the user is finished with.
func deleteAlert(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			jsonError(w, "bad_request", http.StatusBadRequest)
			return
		}
		if err := d.DB.DeleteAlert(id); err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		unread, _ := d.DB.UnreadAlerts()
		jsonOK(w, map[string]any{"unread": unread})
	}
}
