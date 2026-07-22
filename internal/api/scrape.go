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
	// Connection health — could Yata REACH this tracker (API or scrape),
	// as opposed to whether the numbers it returned look good. Uptime runs
	// oldest→newest over connectionDays, one entry per UTC day: 0..1 = the
	// fraction of that day's contacts that succeeded, -1 = no contact
	// attempted (a paused or newly-added tracker must not read as down).
	Uptime []float64 `json:"uptime,omitempty"`
	// Unreachable is the CURRENT verdict: the most recent day with any
	// contact ended with every attempt failing. Drives the Health card count.
	Unreachable  bool   `json:"unreachable,omitempty"`
	LastDownKind string `json:"last_down_kind,omitempty"` // failure kind behind Unreachable
	// APIDown means every API call on the last day the API was tried failed,
	// while something still got through (usually the scrape fallback). The
	// tracker is not dark, but half of how Yata reaches it is broken and its
	// stats are running on the fallback — worth showing, not worth alarming.
	APIDown     bool   `json:"api_down,omitempty"`
	APIDownKind string `json:"api_down_kind,omitempty"`
}

// connectionDays is the width of the uptime strip in the expanded row and the
// span of the Health card's sparkline. Seven days reads as "this week" at a
// glance and stays legible as discrete blocks on a phone.
const connectionDays = 7

// buildUptime turns one tracker's daily rollups into a fixed-width strip
// ending today, filling days with no recorded contact with -1 ("no data").
// A fixed width matters: the strip is rendered as blocks, so every tracker's
// row must line up regardless of when it was added.
// connVerdict is the current state derived from the tail of the strip.
type connVerdict struct {
	Uptime      []float64
	Unreachable bool
	LastKind    string
	APIDown     bool
	APIDownKind string
}

func buildUptime(days []store.ConnectionDay, today int64) connVerdict {
	byDay := make(map[int64]store.ConnectionDay, len(days))
	for _, c := range days {
		byDay[c.Day] = c
	}
	v := connVerdict{Uptime: make([]float64, connectionDays)}
	seen := false
	for i := range connectionDays {
		day := today - int64((connectionDays-1-i))*86400
		c, ok := byDay[day]
		if !ok {
			v.Uptime[i] = -1
			continue
		}
		v.Uptime[i] = c.Uptime()
		// Each verdict follows the most recent day that actually exercised
		// that channel, so a tracker last contacted two days ago still
		// reports that outcome rather than being silently forgiven by
		// today's empty row. The two are tracked independently: the API can
		// be down for days while the scrape fallback keeps the tracker
		// reachable, which is precisely the case the combined count hides.
		if c.Attempts() > 0 {
			v.Unreachable, v.LastKind, seen = c.OKCount == 0, c.LastKind, true
		}
		if c.APIAttempts() > 0 {
			v.APIDown = c.APIDown()
			if v.APIDown {
				v.APIDownKind = c.LastKind
			}
		}
	}
	if !seen {
		return connVerdict{Uptime: v.Uptime}
	}
	// Fully dark already says everything; don't also report the API half.
	if v.Unreachable {
		v.APIDown, v.APIDownKind = false, ""
	}
	return v
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
		// One query for every tracker's rollups, then indexed per tracker —
		// the per-tracker alternative is N queries on every status poll.
		since := now.UTC().AddDate(0, 0, -(connectionDays - 1))
		conns := map[string][]store.ConnectionDay{}
		if rows, err := d.DB.ConnectionDaily(nil, since); err == nil {
			for _, c := range rows {
				conns[c.TrackerID] = append(conns[c.TrackerID], c)
			}
		}
		today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC).Unix()

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
			v := buildUptime(conns[t.ID], today)
			entry.Uptime, entry.Unreachable, entry.LastDownKind = v.Uptime, v.Unreachable, v.LastKind
			entry.APIDown, entry.APIDownKind = v.APIDown, v.APIDownKind
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
		recordScrapeAttempt(d, t, serr)
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
