package scrape

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// TestIntervalCascade encodes the user-specified rule:
// global default 60 < tracker def 120 → 120; user setting 240 → 240.
func TestIntervalCascade(t *testing.T) {
	cases := []struct {
		name    string
		global  int
		def     int
		user    int
		want    int
	}{
		{"hard floor", 0, 0, 0, 60},
		{"global below tracker def", 60, 120, 0, 120},
		{"user above tracker def", 60, 120, 240, 240},
		{"global above everything", 360, 120, 240, 360},
		{"global cannot go under floor", 30, 0, 0, 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := models.Settings{ScrapeIntervalMinutes: tc.global}
			tr := models.Tracker{MinScrapeIntervalMinutes: tc.user}
			rs := defs.ResolvedScrape{MinIntervalMinutes: tc.def}
			if got := EffectiveInterval(set, tr, rs); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDailyCapMostRestrictive(t *testing.T) {
	set := models.Settings{MaxScrapesPerDay: 10}
	tr := models.Tracker{}
	rs := defs.ResolvedScrape{MaxScrapesPerDay: 4}
	if got := EffectiveMaxPerDay(set, tr, rs); got != 4 {
		t.Errorf("got %d, want 4 (tracker request wins when lower)", got)
	}
	set.MaxScrapesPerDay = 2
	if got := EffectiveMaxPerDay(set, tr, rs); got != 2 {
		t.Errorf("got %d, want 2 (global wins when lower)", got)
	}
	set.MaxScrapesPerDay = 0
	if got := EffectiveMaxPerDay(set, tr, rs); got != 4 {
		t.Errorf("got %d, want 4 (unlimited global defers to tracker)", got)
	}
	// Per-tracker override is the most restrictive non-zero value.
	tr.MaxScrapesPerDay = 1
	if got := EffectiveMaxPerDay(set, tr, rs); got != 1 {
		t.Errorf("got %d, want 1 (per-tracker cap wins)", got)
	}
}

func TestPolicyBlocksAndCooldown(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	set := models.Settings{ScrapeIntervalMinutes: 60}
	tr := models.Tracker{ID: "x", Username: "user", SessionCookie: "session=abc"}
	// Anchor to mid-day UTC so the daily-cap check (now+2h) can't cross UTC
	// midnight and reset the day counter — otherwise the test flakes near 00:00 UTC.
	n := time.Now().UTC()
	now := time.Date(n.Year(), n.Month(), n.Day(), 12, 0, 0, 0, time.UTC)

	// Opt-out is the hardest stop — it outranks even api_only.
	p := Evaluate(models.Settings{APIOnlyMode: true, ScrapeIntervalMinutes: 60}, tr, defs.ResolvedScrape{OptedOut: true}, db, now)
	if p.Allowed || p.Reason != "opted_out" {
		t.Errorf("opted_out: %+v", p)
	}
	// API-only mode blocks everything.
	p = Evaluate(models.Settings{APIOnlyMode: true, ScrapeIntervalMinutes: 60}, tr, defs.ResolvedScrape{}, db, now)
	if p.Allowed || p.Reason != "api_only" {
		t.Errorf("api_only: %+v", p)
	}
	// Operator disable cannot be overridden.
	p = Evaluate(set, tr, defs.ResolvedScrape{DisableScraping: true}, db, now)
	if p.Allowed || p.Reason != "scrape_disabled" {
		t.Errorf("scrape_disabled: %+v", p)
	}
	// Missing credentials block BEFORE any request could be made.
	p = Evaluate(set, models.Tracker{ID: "x"}, defs.ResolvedScrape{}, db, now)
	if p.Allowed || p.Reason != "no_username" {
		t.Errorf("no_username: %+v", p)
	}
	p = Evaluate(set, models.Tracker{ID: "x", Username: "user"}, defs.ResolvedScrape{}, db, now)
	if p.Allowed || p.Reason != "no_cookie" {
		t.Errorf("no_cookie: %+v", p)
	}
	// Fresh tracker with full credentials: allowed.
	p = Evaluate(set, tr, defs.ResolvedScrape{}, db, now)
	if !p.Allowed {
		t.Errorf("fresh tracker should be allowed: %+v", p)
	}
	// After a scrape: cooldown with next_allowed_at.
	if err := db.RecordScrape("x", now, true, ""); err != nil {
		t.Fatal(err)
	}
	p = Evaluate(set, tr, defs.ResolvedScrape{}, db, now.Add(time.Minute))
	if p.Allowed || p.Reason != "cooldown" || p.NextAllowedAt == 0 {
		t.Errorf("cooldown: %+v", p)
	}
	// Daily cap.
	p = Evaluate(models.Settings{ScrapeIntervalMinutes: 60, MaxScrapesPerDay: 1}, tr, defs.ResolvedScrape{}, db, now.Add(2*time.Hour))
	if p.Allowed || p.Reason != "daily_limit" {
		t.Errorf("daily_limit: %+v", p)
	}
}
