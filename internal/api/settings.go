package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/config"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

func registerSettings(r chi.Router, d *Deps) {
	r.Get("/settings", getSettings(d))
	r.Put("/settings", putSettings(d))
	r.Patch("/settings", patchSettings(d))
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

// putSettings refuses the old full-object write. A page that loaded settings
// earlier would send every field back at the value it loaded, undoing changes
// made since in another tab or by a restart with an edited config (#94). A
// page still running the old script gets this and is told to reload.
func putSettings(_ *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		jsonError(w, "settings_reload_required: this page is out of date, reload it to save settings", http.StatusConflict)
	}
}

// patchSettings changes only the settings named in the body; everything else
// keeps its current value, so two tabs editing different settings can't undo
// each other. Read, change and save happen under one lock (PatchSettings).
func patchSettings(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		var present map[string]json.RawMessage
		if err := json.Unmarshal(body, &present); err != nil || present == nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		stored, ns, err := d.Cfg.PatchSettings(func(s *models.Settings) error {
			prev := *s
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.DisallowUnknownFields()
			if err := dec.Decode(s); err != nil {
				return err
			}
			// A secret left out of the patch counts as the mask ("keep it"),
			// so moving qui/Prowlarr/Jackett to another host still clears it.
			secret := func(key, sent, kept, newURL, oldURL string) string {
				if _, ok := present[key]; !ok {
					sent = maskedKey
				}
				return resolveMaskedSecret(sent, kept, newURL, oldURL)
			}
			s.QUIAPIKey = secret("qui_api_key", s.QUIAPIKey, prev.QUIAPIKey, s.QUIURL, prev.QUIURL)
			s.ProwlarrAPIKey = secret("prowlarr_api_key", s.ProwlarrAPIKey, prev.ProwlarrAPIKey, s.ProwlarrURL, prev.ProwlarrURL)
			s.JackettAdminPassword = secret("jackett_admin_password", s.JackettAdminPassword, prev.JackettAdminPassword, s.JackettURL, prev.JackettURL)
			return nil
		})
		if errors.Is(err, config.ErrInvalidSettings) {
			jsonError(w, strings.TrimPrefix(err.Error(), config.ErrInvalidSettings.Error()+": "), http.StatusBadRequest)
			return
		}
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Debug, not info — auto-save writes on every toggled setting.
		d.logDebugf("settings: saved")
		// Seedsize mode switched on (or its qui config changed): populate the
		// qui layers now instead of waiting up to a refresh cycle. Async —
		// settings saves must not block on a qui round-trip.
		quiChanged := ns.QUIURL != stored.QUIURL || ns.QUIAPIKey != stored.QUIAPIKey
		seedTurnedOn := ns.QUISeedsizeMode != "off" && (ns.QUISeedsizeMode != stored.QUISeedsizeMode || quiChanged)
		alertsTurnedOn := ns.QUIAlertsEnabled && (!stored.QUIAlertsEnabled || quiChanged)
		if seedTurnedOn || alertsTurnedOn {
			go refreshQUI(d)
		}
		// Switched off: stored counts would otherwise stay merged — and keep
		// matching rules — until something else rewrote the layer.
		if stored.QUIAlertsEnabled && !ns.QUIAlertsEnabled {
			go clearQUIProblemCounts(d)
		}
		jsonOK(w, maskSettings(ns))
	}
}
