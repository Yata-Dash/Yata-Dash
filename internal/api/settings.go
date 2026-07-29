package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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

// cleanAllowedHosts tidies and checks the hostname list submitted from the UI.
//
// The wildcard is refused here on purpose. Turning the host check off
// altogether is a deployment decision, and requiring --allowed-hosts or
// YATA_ALLOWED_HOSTS for it means it takes access to the machine rather than
// just a browser session — so the one setting that can disable a security
// control cannot be flipped through the API.
//
// Values that are plainly not hostnames are rejected rather than silently
// ignored: pasting a whole URL is the obvious mistake, and a list that
// quietly does nothing is worse than an error saying why.
func cleanAllowedHosts(in []string) ([]string, error) {
	var out []string
	for _, h := range in {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if h == "*" {
			return nil, fmt.Errorf("the \"*\" wildcard can only be set with --allowed-hosts " +
				"or YATA_ALLOWED_HOSTS, not from the dashboard")
		}
		if strings.Contains(h, "://") || strings.ContainsAny(h, " \t/\\?#@") {
			return nil, fmt.Errorf("%q is not a hostname — use just the name, e.g. yata.example.com", h)
		}
		out = append(out, h)
	}
	return out, nil
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
		cleaned, err := cleanAllowedHosts(s.AllowedHosts)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.AllowedHosts = cleaned
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
