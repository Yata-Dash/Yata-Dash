package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/scrape"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

func registerScrape(r chi.Router, d *Deps) {
	r.Post("/scrape/{id}", runScrape(d))
	r.Get("/scrape/{id}", runScrape(d)) // convenience for the frontend refresh button
	r.Get("/scrape-status", scrapeStatus(d))
}

// scrapeStatusEntry is the per-tracker policy + health snapshot for the UI.
type scrapeStatusEntry struct {
	scrape.Policy
	SupportsHTMLScrape bool `json:"supports_html_scrape"`
	// Scrape health — outcome of the tail of the scrape log.
	LastErrorKind       string `json:"last_error_kind,omitempty"` // latest attempt's error ("" = ok/none)
	LastErrorAt         int64  `json:"last_error_at,omitempty"`   // unix seconds
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
	CookieExpired       bool   `json:"cookie_expired,omitempty"`
}

// cookieExpired reports whether the health tail looks like a dead session
// cookie: the latest attempt failed with an explicit login signal, or the
// two latest attempts both came back as empty scrapes (a page that loads but
// yields zero fields is usually a login/interstitial page — but a single one
// can be anti-bot or maintenance, so one alone doesn't cry wolf).
func cookieExpired(h store.ScrapeHealth) bool {
	if h.LastOK {
		return false
	}
	switch h.LastKind {
	case "session_expired", "user_id_not_found":
		return true
	case "empty_scrape":
		return h.PrevFailKind == "empty_scrape"
	}
	return false
}

// GET /api/scrape-status — policy snapshot for every tracker (UI indicators:
// alert bar, disabled refresh buttons, next-allowed tooltips, expired-cookie
// warnings).
func scrapeStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		set := d.Cfg.Settings()
		now := time.Now()
		out := map[string]scrapeStatusEntry{}
		for _, t := range d.Cfg.Trackers() {
			rs := d.Reg.ResolveScrape(t.URL, t.Type)
			entry := scrapeStatusEntry{
				Policy:             scrape.Evaluate(set, t, rs, d.DB, now),
				SupportsHTMLScrape: !rs.SkipHTMLScrape && !rs.DisableScraping,
			}
			if h, err := d.DB.GetScrapeHealth(t.ID); err == nil && !h.LastOK {
				entry.LastErrorKind = h.LastKind
				entry.LastErrorAt = h.LastAt
				entry.ConsecutiveFailures = h.ConsecutiveFailures
				entry.CookieExpired = cookieExpired(h)
			}
			out[t.ID] = entry
		}
		jsonOK(w, out)
	}
}

// POST /api/scrape/{id} — run a profile scrape if the policy allows, persist
// the scrape layer, and return the merged stats view.
func runScrape(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, ok := d.Cfg.Tracker(id)
		if !ok {
			jsonError(w, "tracker not found", http.StatusNotFound)
			return
		}
		// Hold the per-tracker lock across evaluate→scrape→record so a
		// concurrent trigger (other tab, auto-sync, API fallback) can never
		// double-hit the tracker. Policy is evaluated INSIDE the lock.
		mu := lockScrape(t.ID)
		defer mu.Unlock()

		rs := d.Reg.ResolveScrape(t.URL, t.Type)
		pol := scrape.Evaluate(d.Cfg.Settings(), t, rs, d.DB, time.Now())
		if !pol.Allowed {
			// This is a USER-initiated scrape being refused — warn, with the
			// next-allowed time for cooldowns. (The background refresh path
			// logs its expected cooldown skips at debug, not here.)
			if pol.NextAllowedAt > 0 {
				d.logWarnf("scrape: %s (%s) blocked — %s (next allowed %s)",
					t.Name, t.ID, pol.Reason, time.Unix(pol.NextAllowedAt, 0).Format("15:04:05"))
			} else {
				d.logWarnf("scrape: %s (%s) blocked — %s", t.Name, t.ID, pol.Reason)
			}
			jsonStatus(w, http.StatusTooManyRequests, map[string]any{
				"error":  pol.Reason,
				"policy": pol,
			})
			return
		}

		spec := scrape.Spec{
			ExtraLabels:     rs.Labels,
			ProfilePath:     rs.ProfilePath,
			EventTitleClass: rs.EventTitleClass,
			StatCardClasses: rs.StatCardClasses,
			PresenceFlags:   rs.PresenceFlags,
			Identify:        rs.Identify,
			Gazelle:         d.Reg.APIKind(t.URL, t.Type) == "gazelle",
			KnownUserID:     mergedString(d, t.ID, "user_id"),
		}
		result, serr := scrape.Profile(t, spec)
		recordScrapeAttempt(d, t.ID, serr)
		if serr != nil {
			d.logWarnf("scrape: %s (%s) failed — %s", t.Name, t.ID, serr.Kind)
			jsonError(w, serr.Kind, upstreamStatus(serr.Status))
			return
		}
		d.logInfof("scrape: %s (%s) ok — %d fields", t.Name, t.ID, len(result))
		_ = d.Stats.SaveScrape(t.ID, toAnyMap(result))

		merged, err := d.Stats.Merged(t.ID)
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		jsonOK(w, models.TrackerStatsResponse{
			TrackerID: t.ID,
			OK:        true,
			Fields:    merged,
			FetchedAt: time.Now().Unix(),
		})
	}
}
