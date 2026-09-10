package api

import (
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// The daily-limit and expired-cookie signals must be derived on the SERVER, in
// the same pass that evaluates the tracker's rules — they used to exist only as
// a browser-side banner read from /api/scrape-status.
func TestScrapeSignalsFromRealState(t *testing.T) {
	d := testDeps(t)
	// A tracker whose def allows scraping, capped at one scrape a day.
	tr := models.Tracker{
		ID: "t1", Name: "OnlyEncodes+", URL: "https://onlyencodes.cc",
		Type: "unit3d", Enabled: true, MaxScrapesPerDay: 1,
		// Scrapeable in every other respect: the policy reports no_username or
		// no_cookie BEFORE daily_limit, and rightly — a tracker missing those
		// is unconfigured, not capped.
		Username: "someone", SessionCookie: "x=y",
	}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}

	var tc = struct{ before, after bool }{}
	ctx := buildTrendContextFor(d, tr)
	tc.before = ctx.ScrapeLimited
	if tc.before {
		t.Fatal("limited before any scrape was logged")
	}

	// One successful scrape today uses up the cap.
	if err := d.DB.RecordScrape(tr.ID, time.Now().UTC(), true, ""); err != nil {
		t.Fatal(err)
	}
	ctx = buildTrendContextFor(d, tr)
	if !ctx.ScrapeLimited {
		t.Error("scrape_limited stayed false after the daily cap was used")
	}
	if ctx.CookieExpired() {
		t.Error("cookie reported expired after a SUCCESSFUL scrape")
	}
}

// A tracker the operator forbids scraping must report neither signal: it can
// never hit a cap or wear out a cookie, and saying otherwise would warn about
// something Yata does not do.
func TestScrapeSignalsSilentWhenScrapingDisabled(t *testing.T) {
	d := testDeps(t)
	tr := models.Tracker{
		ID: "t2", Name: "PeerGarden", URL: "https://peergarden.org",
		Type: "traxary", Enabled: true, MaxScrapesPerDay: 1,
	}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.RecordScrape(tr.ID, time.Now().UTC(), true, ""); err != nil {
		t.Fatal(err)
	}
	ctx := buildTrendContextFor(d, tr)
	if ctx.ScrapeLimited || ctx.CookieExpired() {
		t.Errorf("signals set for a tracker that is never scraped: %+v", ctx)
	}
}
