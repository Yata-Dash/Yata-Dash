package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

func registerSettings(r chi.Router, d *Deps) {
	r.Get("/settings", getSettings(d))
	r.Put("/settings", putSettings(d))
}

// getSettings returns the settings with secrets masked. The QUI API key is
// never sent to the browser — clients see the mask sentinel when a key is
// stored and send it back unchanged to mean "keep the existing key".
func getSettings(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonOK(w, maskSettings(d.Cfg.Settings()))
	}
}

func maskSettings(s models.Settings) models.Settings {
	if s.QUIAPIKey != "" {
		s.QUIAPIKey = maskedKey
	}
	if s.ProwlarrAPIKey != "" {
		s.ProwlarrAPIKey = maskedKey
	}
	if s.JackettAdminPassword != "" {
		s.JackettAdminPassword = maskedKey
	}
	return s
}

func putSettings(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var s models.Settings
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		// Mask sentinel = "keep the stored key". Empty string clears it.
		stored := d.Cfg.Settings()
		if s.QUIAPIKey == maskedKey {
			s.QUIAPIKey = stored.QUIAPIKey
		}
		if s.ProwlarrAPIKey == maskedKey {
			s.ProwlarrAPIKey = stored.ProwlarrAPIKey
		}
		if s.JackettAdminPassword == maskedKey {
			s.JackettAdminPassword = stored.JackettAdminPassword
		}
		if err := d.Cfg.UpdateSettings(s); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Debug, not info — auto-save PUTs on every toggled setting.
		d.logDebugf("settings: saved")
		// Seedsize mode switched on (or its qui config changed): populate the
		// qui layers now instead of waiting up to a refresh cycle. Async —
		// settings saves must not block on a qui round-trip.
		if ns := d.Cfg.Settings(); ns.QUISeedsizeMode != "off" &&
			(ns.QUISeedsizeMode != stored.QUISeedsizeMode || ns.QUIURL != stored.QUIURL || ns.QUIAPIKey != stored.QUIAPIKey) {
			go refreshQUISeedsize(d)
		}
		jsonOK(w, maskSettings(d.Cfg.Settings()))
	}
}
