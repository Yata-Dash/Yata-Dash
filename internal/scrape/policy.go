package scrape

import (
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// HardFloorMinutes is the absolute minimum scrape interval. Nothing — not
// settings, not defs — can go below this.
const HardFloorMinutes = 60

// Policy is the result of evaluating the scrape rate-limit cascade for one
// tracker at a point in time.
type Policy struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"` // opted_out | api_only | scrape_disabled | no_scrape_support | daily_limit | cooldown
	// NextAllowedAt is set for reason "cooldown" (unix seconds).
	NextAllowedAt int64 `json:"next_allowed_at,omitempty"`
	// EffectiveIntervalMinutes is the resolved minimum gap between scrapes.
	EffectiveIntervalMinutes int `json:"effective_interval_minutes"`
	// EffectiveMaxPerDay is the resolved UTC-day cap (0 = unlimited).
	EffectiveMaxPerDay int `json:"effective_max_per_day"`
	// ScrapesToday is the count so far this UTC day.
	ScrapesToday int `json:"scrapes_today"`
	// LastScrapeAt is the unix time of the last scrape (0 = never).
	LastScrapeAt int64 `json:"last_scrape_at,omitempty"`
	// TrackerMinInterval reports the def-level operator request so the UI can
	// explain why the effective interval exceeds the user's setting.
	TrackerMinInterval int `json:"tracker_min_interval,omitempty"`
}

// EffectiveInterval resolves the interval cascade:
//
//	max(hard floor 60, global setting, type/tracker def request, per-tracker user setting)
//
// When auto_interval is on and a daily cap exists, the global layer becomes
// 1440/cap (floored to 60) — matching v1 behaviour.
func EffectiveInterval(set models.Settings, t models.Tracker, rs defs.ResolvedScrape) int {
	global := set.ScrapeIntervalMinutes
	if set.AutoInterval && set.MaxScrapesPerDay > 0 {
		global = 1440 / set.MaxScrapesPerDay
	}
	// Per-tracker auto-calc derives the user layer from the per-tracker cap.
	perTracker := t.MinScrapeIntervalMinutes
	if t.AutoInterval && t.MaxScrapesPerDay > 0 {
		if d := 1440 / t.MaxScrapesPerDay; d > perTracker {
			perTracker = d
		}
	}
	iv := HardFloorMinutes
	for _, v := range []int{global, rs.MinIntervalMinutes, perTracker} {
		if v > iv {
			iv = v
		}
	}
	return iv
}

// EffectiveMaxPerDay resolves the daily cap: most restrictive non-zero value
// across the global setting, the def request, and the per-tracker override.
func EffectiveMaxPerDay(set models.Settings, t models.Tracker, rs defs.ResolvedScrape) int {
	capDay := 0
	for _, v := range []int{set.MaxScrapesPerDay, rs.MaxScrapesPerDay, t.MaxScrapesPerDay} {
		if v > 0 && (capDay == 0 || v < capDay) {
			capDay = v
		}
	}
	return capDay
}

// Evaluate runs the full policy check for a tracker. Counters and last-scrape
// times come from the persistent scrape log in SQLite.
func Evaluate(set models.Settings, t models.Tracker, rs defs.ResolvedScrape, db *store.DB, now time.Time) Policy {
	p := Policy{
		EffectiveIntervalMinutes: EffectiveInterval(set, t, rs),
		EffectiveMaxPerDay:       EffectiveMaxPerDay(set, t, rs),
		TrackerMinInterval:       rs.MinIntervalMinutes,
	}

	utcDayStart := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if n, err := db.ScrapesSince(t.ID, utcDayStart); err == nil {
		p.ScrapesToday = n
	}
	if last, err := db.LastScrape(t.ID); err == nil {
		p.LastScrapeAt = last
	}

	switch {
	case rs.OptedOut:
		// Operator asked not to be supported at all — hard stop, above every
		// other reason. Enforced here so an existing tracker that lands on the
		// opt-out list stops being scraped immediately, not just at add-time.
		p.Reason = "opted_out"
	case rs.Retired:
		// The site has shut down. Above the generic disabled/API-only reasons
		// so the UI can say what actually happened rather than implying the
		// operator switched something off or the user configured it that way.
		p.Reason = "retired"
	case set.APIOnlyMode || t.APIOnly:
		p.Reason = "api_only"
	case rs.SkipHTMLScrape:
		p.Reason = "no_scrape_support"
	case rs.DisableScraping:
		p.Reason = "scrape_disabled"
	case strings.TrimSpace(t.Username) == "":
		// No username → no profile URL to scrape.
		p.Reason = "no_username"
	case strings.TrimSpace(t.SessionCookie) == "":
		// Without a session cookie the tracker serves its login page — the
		// scraper would extract garbage from it. Scraping stays disabled
		// until the user provides a cookie.
		p.Reason = "no_cookie"
	case p.EffectiveMaxPerDay > 0 && p.ScrapesToday >= p.EffectiveMaxPerDay:
		p.Reason = "daily_limit"
	default:
		if p.LastScrapeAt > 0 {
			next := p.LastScrapeAt + int64(p.EffectiveIntervalMinutes)*60
			if now.Unix() < next {
				p.Reason = "cooldown"
				p.NextAllowedAt = next
				return p
			}
		}
		p.Allowed = true
	}
	return p
}
