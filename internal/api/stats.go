package api

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
	"github.com/Yata-Dash/Yata-Dash/internal/scrape"
)

func registerStats(r chi.Router, d *Deps) {
	r.Get("/stats", bulkStats(d))
	r.Get("/stats/{id}", singleStats(d))
}

// refreshTracker fetches fresh API data for one tracker, persists it to the
// API layer, and returns the merged view. On API failure it attempts a
// profile-scrape fallback for the main stats — but only when the scrape
// policy allows it (v2 fix: the fallback respects rate limits and disable
// flags, and is logged in scrape_log like any other scrape).
//
// IMPORTANT (v1 lesson): this runs inside goroutines for bulk refresh.
// The config manager is mutex-safe; never add file reloads here.
//
// force=true (manual refresh button / Tracker Test / per-tracker refresh)
// always hits the API. force=false (background loop, open-dashboard polls,
// page reloads) goes through the min-age guard below so those redundant
// callers coalesce into ~one API call per configured refresh interval.
func refreshTracker(d *Deps, t models.Tracker, force bool) models.TrackerStatsResponse {
	resp := models.TrackerStatsResponse{
		TrackerID: t.ID,
		FetchedAt: time.Now().Unix(),
	}

	// Opt-out gate — a tracker whose operator has asked not to be supported
	// gets NO API fetch and NO scrape. This is checked here (not just at
	// add-time) because a tracker can land on defs/optout.json after it was
	// already configured; without this it would keep being polled. We still
	// return the last-stored fields so the UI can show why it stopped, and we
	// skip alert evaluation so opting out never fires a "tracker down" alert.
	if _, opted := d.Reg.OptOut(t.URL); opted {
		logOptOutTransition(d, t, true)
		resp.ErrorKind = "opted_out"
		resp.Error = "opted_out"
		if merged, err := d.Stats.Merged(t.ID); err == nil {
			resp.Fields = merged
		}
		return resp
	}
	logOptOutTransition(d, t, false)

	// Min-age guard — coalesce non-forced callers. If we fetched this tracker's
	// API successfully within the guard window, skip the network call and serve
	// the last-stored merged stats instead. Manual (forced) refreshes bypass it.
	if !force {
		if v, ok := lastFetchAt.Load(t.ID); ok {
			if time.Since(v.(time.Time)) < autoRefreshMinAge(d) {
				if merged, err := d.Stats.Merged(t.ID); err == nil {
					resp.Fields = merged
					resp.OK = true
					resp.FetchedAt = v.(time.Time).Unix() // reflect the real last fetch
					if r := d.Stats.GrowthRates(t.ID); len(r) > 0 {
						resp.Rates = r
					}
				}
				return resp
			}
		}
	}

	// Capture the group before this fetch so we can record a promotion/demotion
	// event if it changes. Read from stored state (not memory) so it survives
	// restarts and only fires on a real transition.
	oldGroup := mergedString(d, t.ID, "group")

	data, ferr := d.Fetch.Fetch(t)
	if ferr == nil {
		lastFetchAt.Store(t.ID, time.Now()) // gate future non-forced fetches
		_ = d.Stats.SaveAPI(t.ID, data)
		resp.OK = true
		logFetchTransition(d, t, "")

		// Auto-save username/join date the first time the API reveals them.
		if u, ok := data["username"].(string); ok && u != "" && t.Username == "" {
			_ = d.Cfg.UpdateTracker(t.ID, func(tr *models.Tracker) { tr.Username = u })
		}
	} else {
		resp.ErrorKind = ferr.Kind
		resp.Error = ferr.Error()
		logFetchTransition(d, t, ferr.Kind)
		// API failed — try a policy-respecting scrape fallback for main stats.
		tryScrapeFallback(d, t)
	}

	merged, err := d.Stats.Merged(t.ID)
	if err == nil {
		resp.Fields = merged
		if resp.OK {
			_ = d.Stats.RecordHistory(t.ID, merged)
		}
		// Growth rates (daily-rollup average) for target/promotion ETAs.
		if r := d.Stats.GrowthRates(t.ID); len(r) > 0 {
			resp.Rates = r
		}
	} else if resp.Error == "" {
		resp.OK = false
		resp.Error = "store_error"
	}

	// Standing-guard trend signals for this pass — built once (cheap no-op
	// with zero alert rules, see buildTrendContext) and threaded through every
	// evaluation call below, so a mixed rule like "promoted AND ratio < 1"
	// sees the same numbers whether it fires from the event path or the poll.
	var trends notify.TrendContext
	if d.Alerts != nil {
		trends = buildTrendContext(d, t, resp.Fields, resp.Rates)
	}
	if err == nil && resp.OK {
		recordGroupChange(d, t, resp.Fields, oldGroup, mergedFieldString(resp.Fields, "group"), trends)
	}

	// Alert evaluation — fires webhooks on rising-edge rule matches. Runs on
	// every refresh path (frontend poll, single, or the background loop);
	// edge-triggering + per-tracker priming keep it from spamming.
	if d.Alerts != nil {
		d.Alerts.Evaluate(t, resp.Fields, resp.OK, trends)
		// Target-met events need fresh data to mean anything — a failed
		// refresh serves the last stored fields, which would just re-diff the
		// same values against themselves.
		if resp.OK {
			evaluateTrackerTargets(d, t, resp.Fields, trends)
		}
	}
	return resp
}

// mergedFieldString reads one merged field as a trimmed string ("" if absent),
// from a map already in hand (no extra query).
func mergedFieldString(merged models.MergedStats, field string) string {
	if f, ok := merged[field]; ok {
		if s, ok := f.Value.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// recordGroupChange writes a group_change event when a tracker's group moves
// between two known values (a promotion or demotion), and — when the def's
// group ladder can classify the direction — fires the matching promoted/
// demoted alert event. Styled badges etc. still classify direction
// independently client-side from the same ordered group defs. Only real
// transitions are recorded: both ends non-empty and different, and not an
// exact repeat of the last recorded change.
func recordGroupChange(d *Deps, t models.Tracker, merged models.MergedStats, oldGroup, newGroup string, trends notify.TrendContext) {
	oldGroup, newGroup = strings.TrimSpace(oldGroup), strings.TrimSpace(newGroup)
	if oldGroup == "" || newGroup == "" || strings.EqualFold(oldGroup, newGroup) {
		return
	}
	detail := oldGroup + "→" + newGroup
	if last, err := d.DB.LatestEventDetail(t.ID, "group_change"); err == nil && last == detail {
		return
	}
	if err := d.DB.AddEvent(t.ID, time.Now().UTC(), "group_change", detail); err == nil {
		d.logInfof("event: %s (%s) group change — %s", t.Name, t.ID, detail)
	}

	if d.Alerts == nil {
		return
	}
	td, ok := d.Reg.TrackerByURL(t.URL)
	if !ok {
		return
	}
	oldIdx, newIdx := groupLadderIndex(td.Groups, oldGroup), groupLadderIndex(td.Groups, newGroup)
	if oldIdx < 0 || newIdx < 0 || oldIdx == newIdx {
		// Unranked (not in this def's ladder) or same rung either way — the
		// neutral "group changed" rule still catches this via polling.
		return
	}
	if newIdx > oldIdx {
		d.Alerts.EvaluateEvent(t, merged, notify.EventContext{Kind: "promoted", Detail: "promoted: " + oldGroup + " → " + newGroup}, trends)
	} else {
		d.Alerts.EvaluateEvent(t, merged, notify.EventContext{Kind: "demoted", Detail: "demoted: " + oldGroup + " → " + newGroup}, trends)
	}
}

// groupLadderIndex returns a group's position in the def's ascending ladder
// (Groups is lowest-first — see defs.TrackerDef.Groups), matched case-
// insensitively, or -1 if the name isn't one of this def's ranks.
func groupLadderIndex(groups []defs.GroupDef, name string) int {
	for i, g := range groups {
		if strings.EqualFold(g.Name, name) {
			return i
		}
	}
	return -1
}

// evaluateTrackerTargets computes the tracker's current target rows (base
// targets + its target group's min_counts/any_of, see targeteval.go) and
// feeds them to the alert engine's per-row edge tracker, which fires a
// target_met event for each row crossing unmet→met. Cheap no-op when the
// tracker isn't tracking anything.
func evaluateTrackerTargets(d *Deps, t models.Tracker, merged models.MergedStats, trends notify.TrendContext) {
	if len(t.Targets) == 0 && t.TargetGroup == "" {
		return
	}
	var groups []defs.GroupDef
	if td, ok := d.Reg.TrackerByURL(t.URL); ok {
		groups = td.Groups
	}
	rows, met, total := evaluateTargetRows(t, merged, groups)
	if len(rows) == 0 {
		return
	}
	d.Alerts.EvaluateTargets(t, merged, rows, met, total, trends)
}

// RefreshFloorMinutes is the lowest the automatic API-refresh interval can be
// set to. The manual refresh button and Tracker Test bypass the interval
// entirely; this floor only bounds the unattended background cadence.
const RefreshFloorMinutes = 15

// lastFetchAt records the last SUCCESSFUL API fetch time per tracker. In
// memory only — a restart clears it, which just means the first cycle after
// boot refetches everyone (desirable). It powers the min-age guard that keeps
// the background loop, open dashboards, and page reloads from stacking into
// many API calls per interval.
var lastFetchAt sync.Map // trackerID -> time.Time

// autoRefreshMinAge is the guard window: a shade under the configured refresh
// interval (90%), so the scheduled tick still fires while off-phase redundant
// pollers are skipped. 0/short stored values fall back to sane defaults.
func autoRefreshMinAge(d *Deps) time.Duration {
	iv := d.Cfg.Settings().RefreshIntervalMinutes
	if iv <= 0 {
		iv = 30 // unset (e.g. upgraded config) → default
	}
	if iv < RefreshFloorMinutes {
		iv = RefreshFloorMinutes
	}
	return time.Duration(iv) * time.Minute * 9 / 10
}

// lastFetchState remembers each tracker's previous API-fetch outcome so the
// refresh loop logs TRANSITIONS (ok→fail at warn, fail→ok at info) instead of
// re-warning on every cycle — a tracker that stays down would otherwise flood
// the log once per refresh. Keyed by tracker ID; value = last error kind
// ("" = ok). Repeat failures of the same kind log at debug.
var lastFetchState sync.Map

func logFetchTransition(d *Deps, t models.Tracker, errKind string) {
	prev, seen := lastFetchState.Load(t.ID)
	lastFetchState.Store(t.ID, errKind)
	switch {
	case errKind == "" && seen && prev != "":
		d.logInfof("fetch: %s (%s) recovered — API reachable again", t.Name, t.ID)
	case errKind != "" && (!seen || prev == ""):
		d.logWarnf("fetch: %s (%s) failed — %s", t.Name, t.ID, errKind)
	case errKind != "" && prev != errKind:
		d.logWarnf("fetch: %s (%s) still failing — now %s (was %s)", t.Name, t.ID, errKind, prev)
	case errKind != "":
		d.logDebugf("fetch: %s (%s) still failing — %s", t.Name, t.ID, errKind)
	}
}

// lastOptOutState remembers whether each tracker was opted-out on the previous
// refresh so the loop logs the TRANSITION once (a warn when it starts being
// skipped, an info if it later comes off the list) instead of every cycle.
var lastOptOutState sync.Map // trackerID → bool

func logOptOutTransition(d *Deps, t models.Tracker, opted bool) {
	prev, seen := lastOptOutState.Load(t.ID)
	lastOptOutState.Store(t.ID, opted)
	switch {
	case opted && (!seen || prev == false):
		d.logWarnf("fetch: %s (%s) skipped — tracker is on the opt-out list (defs/optout.json); not contacting it", t.Name, t.ID)
	case !opted && seen && prev == true:
		d.logInfof("fetch: %s (%s) no longer opted out — resuming", t.Name, t.ID)
	}
}

// scrapeLocks serialises all scrape activity per tracker. Every path that
// can trigger an HTTP request to a tracker's profile page (manual scrape
// endpoint, auto-sync, API-failure fallback — possibly from multiple browser
// tabs at once) must hold the tracker's lock across the ENTIRE
// evaluate→scrape→record sequence. Without it, two concurrent requests could
// both pass the policy check before either records, double-hitting the
// tracker. Rate limits protect users' accounts — they must be airtight.
var scrapeLocks sync.Map // trackerID → *sync.Mutex

func lockScrape(trackerID string) *sync.Mutex {
	m, _ := scrapeLocks.LoadOrStore(trackerID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu
}

// tryScrapeFallback scrapes the profile page when the API is down, writing
// to the scrape layer. Unlike v1, it goes through the full policy check so a
// dead API can never cause scrape-hammering of a tracker.
func tryScrapeFallback(d *Deps, t models.Tracker) {
	if strings.TrimSpace(t.Username) == "" {
		return
	}
	mu := lockScrape(t.ID)
	defer mu.Unlock()

	// Policy MUST be evaluated inside the lock — a concurrent scrape may have
	// just recorded an attempt that puts us in cooldown.
	rs := d.Reg.ResolveScrape(t.URL, t.Type)
	pol := scrape.Evaluate(d.Cfg.Settings(), t, rs, d.DB, time.Now())
	if !pol.Allowed {
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
	if serr != nil || len(result) == 0 {
		return
	}
	_ = d.Stats.SaveScrape(t.ID, toAnyMap(result))
}

// recordScrapeAttempt logs a scrape in the rate-limit ledger whenever an HTTP
// request actually reached the tracker — including failed ones. A profile
// page that errors must not get re-hit on every refresh cycle; only
// pre-flight failures (no username/key — nothing was sent) are exempt.
func recordScrapeAttempt(d *Deps, trackerID string, serr *scrape.Error) {
	if serr != nil && (serr.Kind == "no_username" || serr.Kind == "no_cookie" || serr.Kind == "no_key") {
		return // pre-flight failure — no request reached the tracker
	}
	_ = d.DB.RecordScrape(trackerID, time.Now().UTC())
}

func toAnyMap(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// RunRefreshCycle refreshes every enabled tracker once. It's used by the
// server-side scheduler so stats stay fresh and alert rules are evaluated even
// when no browser/homelab client is polling /api/stats. Sequential by design
// (gentle on tracker APIs).
func RunRefreshCycle(d *Deps) {
	for _, t := range d.Cfg.Trackers() {
		if !t.Enabled {
			continue
		}
		_ = refreshTracker(d, t, false) // background loop → guarded
	}
}

// bulkRefreshConcurrency bounds how many trackers are refreshed at once by the
// bulk endpoint, so a user with many trackers doesn't fan out into one
// simultaneous outbound request per tracker. Each is a different host, so this
// is about total load, not per-host politeness (that's the rate limiter's job).
const bulkRefreshConcurrency = 8

// GET /api/stats — refresh all enabled trackers, bounded-concurrently.
// ?force=1 (the manual refresh button / post-import) bypasses the min-age
// guard; the plain auto-poll omits it so idle load stays low.
func bulkStats(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		force := r.URL.Query().Get("force") != ""
		trackers := d.Cfg.Trackers()
		results := make(map[string]models.TrackerStatsResponse, len(trackers))
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, bulkRefreshConcurrency)
		for _, t := range trackers {
			if !t.Enabled {
				mu.Lock()
				results[t.ID] = models.TrackerStatsResponse{TrackerID: t.ID, ErrorKind: "disabled", Error: "disabled"}
				mu.Unlock()
				continue
			}
			wg.Add(1)
			go func(t models.Tracker) {
				defer wg.Done()
				sem <- struct{}{}        // acquire a slot (blocks past the cap)
				defer func() { <-sem }() // release
				res := refreshTracker(d, t, force)
				mu.Lock()
				results[t.ID] = res
				mu.Unlock()
			}(t)
		}
		wg.Wait()
		jsonOK(w, results)
	}
}

// GET /api/stats/{id} — refresh one tracker. This is only ever hit by explicit
// user actions (per-tracker refresh / Retry / post-edit), so it always forces.
func singleStats(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, ok := d.Cfg.Tracker(id)
		if !ok {
			jsonError(w, "tracker not found", http.StatusNotFound)
			return
		}
		if !t.Enabled {
			jsonOK(w, models.TrackerStatsResponse{TrackerID: t.ID, ErrorKind: "disabled", Error: "disabled"})
			return
		}
		jsonOK(w, refreshTracker(d, t, true))
	}
}
