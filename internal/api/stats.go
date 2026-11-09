package api

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
	"github.com/Yata-Dash/Yata-Dash/internal/pathways"
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
	return refreshTrackerInCycle(d, t, force, nil)
}

// refreshTrackerInCycle is refreshTracker as one tracker of a pass over many
// (the background loop, a bulk /api/stats request): cyc carries what every
// tracker in that pass can share — see refreshCycle. nil is a lone refresh.
func refreshTrackerInCycle(d *Deps, t models.Tracker, force bool, cyc *refreshCycle) models.TrackerStatsResponse {
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
		resp.APIUpdatedAt = d.Stats.LayerUpdatedAt(t.ID, models.SourceAPI)
		return resp
	}
	logOptOutTransition(d, t, false)

	// A shut-down tracker: nothing is contacted, so there is nothing to
	// refresh. Its stored stats are returned unchanged and no failure is
	// recorded — a dead site is not an outage, and reporting one would leave
	// Connection Health permanently red over a tracker that is simply gone.
	// The history stays exactly where it is; only new data has stopped.
	if spec, retired := d.Reg.Retired(t.URL); retired {
		if merged, err := d.Stats.Merged(t.ID); err == nil {
			resp.Fields = merged
		}
		resp.OK = true
		resp.Retired = true
		resp.RetiredDate = spec.Date
		resp.RetiredNote = spec.Note
		// Same rule as the manual branch below and the opt-out branch above:
		// nothing was contacted, so there is no fetch time to report and the
		// default "now" would date dead data to this second. APIUpdatedAt is
		// carried through instead, which on a shut-down tracker is the more
		// useful number of the two — it says how old the last real data is.
		resp.FetchedAt = 0
		resp.APIUpdatedAt = d.Stats.LayerUpdatedAt(t.ID, models.SourceAPI)
		return resp
	}

	// Manual-entry trackers are never contacted, so there is nothing to
	// refresh: the stats ARE the manual layer, written when the user saved
	// them. Returning early rather than falling through the normal path
	// matters for honesty, not just speed — that path would record a
	// successful "connection" to a site no request was sent to, and stamp an
	// API-updated time on a tracker with no API, so the card would claim
	// contact that never happened.
	//
	// History still runs: each save is a real datapoint, and charting them is
	// how a hand-maintained tracker builds a trend at all.
	if d.Reg.APIKind(t.URL, t.Type) == "none" && t.Type == models.TypeManual {
		// FetchedAt defaults to "now" because for every other tracker it means
		// "when this refresh ran". Nothing ran here, so leaving it stamped a
		// fresh time on a tracker Yata never contacted — the Last API Update
		// column read 09:52 for numbers typed in weeks ago, and the Detail
		// header claimed it had just updated.
		//
		// Zero, not the manual layer's write time: that layer is rewritten on
		// every startup (see cmd/yata/main.go), so it means "last restart or
		// save" rather than "when you typed these in" — a subtler version of
		// the same lie. A tracker that is never contacted has no fetch time,
		// and 0 is exactly how the UI already renders "never".
		resp.FetchedAt = 0
		if merged, err := d.Stats.Merged(t.ID); err == nil {
			resp.Fields = merged
			resp.OK = true
			_ = d.Stats.RecordHistory(t.ID, merged)
			if r := d.Stats.GrowthRates(t.ID); len(r) > 0 {
				resp.Rates = r
			}
		}
		resp.ManualEntry = true
		return resp
	}

	// Held from the min-age guard to the last record below. Without it the
	// guard is a check-then-act: the background loop and a dashboard poll (or
	// two open tabs) arriving together both find no recent fetch, both hit
	// the tracker, and both see the group move — two group_change events and
	// two "promoted" alerts for one promotion. Serialised, the second caller
	// waits for the first and then finds its fetch inside the guard window.
	// A forced refresh takes the lock too: it still always fetches, but reads
	// the group the previous refresh stored, so it cannot replay that one's
	// promotion either. Same pattern as scrapeLocks, for the same reason.
	mu := lockRefresh(t.ID)
	defer mu.Unlock()

	// Min-age guard — coalesce non-forced callers. If this tracker's API was
	// tried within the guard window, skip the network call and serve the
	// last-stored merged stats instead, with that attempt's outcome: a failed
	// attempt counts as much as a successful one, or every open dashboard
	// would re-hit a tracker that is down on every poll. Manual (forced)
	// refreshes bypass it.
	if !force {
		if v, ok := lastFetchAt.Load(t.ID); ok {
			last := v.(fetchAttempt)
			if time.Since(last.at) < autoRefreshMinAge(d) {
				resp.ErrorKind, resp.Error = last.errKind, last.err
				if merged, err := d.Stats.Merged(t.ID); err == nil {
					resp.Fields = merged
					resp.OK = last.errKind == ""
					resp.FetchedAt = last.at.Unix() // reflect the real last fetch
					if r := d.Stats.GrowthRates(t.ID); len(r) > 0 {
						resp.Rates = r
					}
				}
				resp.APIUpdatedAt = d.Stats.LayerUpdatedAt(t.ID, models.SourceAPI)
				return resp
			}
		}
	}

	// Capture the group before this fetch so we can record a promotion/demotion
	// event if it changes. Read from stored state (not memory) so it survives
	// restarts and only fires on a real transition.
	oldGroup := mergedString(d, t.ID, "group")

	data, ferr := d.Fetch.Fetch(t)
	// The Trackers table's status column: this fetch is as much an answer as
	// a Test would be. Only where a request is actually made — a scrape-only
	// tracker's empty API layer is a static "N/A" on read, not a success.
	if d.Reg.APIKind(t.URL, t.Type) != "none" {
		kind, n := "", len(data)
		if ferr != nil {
			kind = ferr.Kind
		}
		recordRefreshCheck(d, t.ID, "api", kind, n)
	}
	if ferr == nil {
		lastFetchAt.Store(t.ID, fetchAttempt{at: time.Now()}) // gate future non-forced fetches
		_ = d.Stats.SaveAPI(t.ID, data)
		resp.OK = true
		logFetchTransition(d, t, "")
		recordConnection(d, t, "api", "")
		// Keeps hand-written capability declarations honest — reports only,
		// never alters what was stored.
		checkCapabilityDrift(d, t, data)

		// The tracker's own group ladder, where the platform serves one — the
		// ranks and thresholds Yata measures against then come from the site
		// instead of from a copy frozen into a def at build time. Cheap no-op
		// for every type that doesn't, and best-effort: see
		// maybeRefreshGroupLadder.
		group, _ := data["group"].(string)
		maybeRefreshGroupLadder(d, t, group)

		// Auto-save username/join date the first time the API reveals them.
		if u, ok := data["username"].(string); ok && u != "" && t.Username == "" {
			_ = d.Cfg.UpdateTracker(t.ID, func(tr *models.Tracker) { tr.Username = u })
		}
	} else {
		resp.ErrorKind = ferr.Kind
		resp.Error = ferr.Error()
		// A failure that reached the tracker gates the next non-forced fetch
		// just as a success does. A pre-flight one (no key, no def) sent
		// nothing, so there is nothing to protect — and it must not leave an
		// earlier success standing in the guard, which would report this
		// broken tracker as fine until the window ran out.
		if isPreflightKind(ferr.Kind) {
			lastFetchAt.Delete(t.ID)
		} else {
			lastFetchAt.Store(t.ID, fetchAttempt{at: time.Now(), errKind: ferr.Kind, err: resp.Error})
		}
		logFetchTransition(d, t, ferr.Kind)
		recordConnection(d, t, "api", ferr.Kind)
		// API failed — try a policy-respecting scrape fallback for main stats.
		tryScrapeFallback(d, t)
	}

	// Read AFTER the fetch above, so a success this pass is reflected.
	resp.APIUpdatedAt = d.Stats.LayerUpdatedAt(t.ID, models.SourceAPI)

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
			evaluateTrackerPins(d, t, resp.Fields, resp.Rates, trends, cyc)
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
	ladder := groupsFor(d, td, t.ID)
	oldIdx, newIdx := defs.LadderIndex(ladder, oldGroup), defs.LadderIndex(ladder, newGroup)
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

// evaluateTrackerTargets computes the tracker's current target rows (base
// targets + its target group's min_counts/any_of, see targeteval.go) and
// feeds them to the alert engine's per-row edge tracker, which fires a
// target_met event for each row crossing unmet→met.
//
// A tracker with nothing to track still gets an (empty) pass, never an early
// return: that is what makes the engine forget its previous rows. Skipped,
// the old rows survived removing every target, so re-adding one that had
// been met in the meantime diffed against the stale "unmet" and fired a
// target_met for a target added already met — the case a new target must
// prime silently for. Same rule evaluateTrackerPins follows.
func evaluateTrackerTargets(d *Deps, t models.Tracker, merged models.MergedStats, trends notify.TrendContext) {
	if len(t.Targets) == 0 && t.TargetGroup == "" {
		d.Alerts.EvaluateTargets(t, merged, nil, 0, 0, trends)
		return
	}
	var groups []defs.GroupDef
	if td, ok := d.Reg.TrackerByURL(t.URL); ok {
		groups = groupsFor(d, td, t.ID)
	}
	rows, met, total := evaluateTargetRows(t, merged, groups)
	d.Alerts.EvaluateTargets(t, merged, rows, met, total, trends)
}

// evaluateTrackerPins re-measures every pinned path and hands the engine one
// row per pin, so a pin whose next hop just became fully met fires
// pathway_ready. All pins go in every time — the engine only diffs the ones
// measured from THIS tracker (that is whose numbers just moved) and uses the
// rest to know what is still pinned. Nothing to do when nothing is pinned,
// which is the common case and skips the user-tracker mapping entirely.
//
// rates is the tracker's GrowthRates as this refresh read them, and cyc the
// pass it belongs to (nil for a lone refresh): see refreshCycle.pinUsers.
func evaluateTrackerPins(d *Deps, t models.Tracker, merged models.MergedStats, rates map[string]float64, trends notify.TrendContext, cyc *refreshCycle) {
	pins := d.Cfg.Settings().PathwayPins
	if len(pins) == 0 || d.Paths == nil {
		// An empty pass, so the engine forgets every chain: a pin removed and
		// later re-pinned must prime afresh, not inherit a state from before.
		d.Alerts.EvaluatePins(t, merged, nil, trends)
		return
	}
	users := cyc.pinUsers(d, t, merged, rates)
	groupsFor, inviteReqsFor := defLookups(d)
	rows := make([]notify.PinRow, 0, len(pins))
	for _, pin := range pins {
		r := pathways.EvalPin(d.Paths, pin.Hops, users, groupsFor, inviteReqsFor)
		if r.State != pathways.PinOK || len(r.Steps) == 0 {
			continue
		}
		open := r.Steps[0]
		rows = append(rows, notify.PinRow{
			Key:       strings.Join(pin.Hops, " → "),
			Label:     "path to " + r.Destination + ", " + open.From + " → " + open.To,
			TrackerID: r.StartTrackerID,
			Ready:     !r.StartDisabled && open.ETADays == 0 && !open.HasUnknown,
		})
	}
	d.Alerts.EvaluatePins(t, merged, rows, trends)
}

// refreshCycle is what the trackers refreshed in one pass (a RunRefreshCycle,
// or one bulk /api/stats request) share. A nil *refreshCycle is a lone refresh
// and shares nothing.
type refreshCycle struct {
	mu    sync.Mutex
	built bool
	users []pathways.UserTracker
}

// pinUsers is the pathway view of every tracker (mapUserTrackers) for
// evaluating pins after t's refresh.
//
// Built once per pass and then kept current, instead of rebuilt per tracker:
// mapUserTrackers reads Merged and GrowthRates for EVERY tracker, so calling it
// from each refresh made a pass over N trackers cost N² of those reads — 400
// GrowthRates queries for 20 trackers, seconds of single-connection SQLite time
// that blocked every other request. The answer is unchanged: within a pass the
// only stats that move are those of the tracker just refreshed, and its entry
// is overwritten with the same Merged and GrowthRates values mapUserTrackers
// would have read for it now. (The first call builds from the store after t's
// refresh, so t's entry is current already.)
func (c *refreshCycle) pinUsers(d *Deps, t models.Tracker, merged models.MergedStats, rates map[string]float64) []pathways.UserTracker {
	if c == nil {
		return mapUserTrackers(d)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.built {
		c.users = mapUserTrackers(d)
		c.built = true
	} else {
		for i := range c.users {
			// A disabled entry carries no stats by design; refreshed trackers
			// are enabled, so this only guards against a mid-pass toggle.
			if c.users[i].TrackerID == t.ID && !c.users[i].Disabled {
				c.users[i].Stats = statsFromMerged(merged)
				c.users[i].Rates = pathwayRates(rates)
			}
		}
	}
	// A copy, because bulk refreshes run concurrently: entries are replaced
	// whole under the lock, never mutated, so a copy taken here stays stable
	// while another tracker's refresh patches the shared slice.
	return slices.Clone(c.users)
}

// RefreshFloorMinutes is the lowest the automatic API-refresh interval can be
// set to. The manual refresh button and Tracker Test bypass the interval
// entirely; this floor only bounds the unattended background cadence.
const RefreshFloorMinutes = 15

// lastFetchAt records the last API fetch attempt per tracker that reached the
// tracker, successful or not. In memory only — a restart clears it, which just
// means the first cycle after boot refetches everyone (desirable). It powers
// the min-age guard that keeps the background loop, open dashboards, and page
// reloads from stacking into many API calls per interval. Read and written
// only under the tracker's refresh lock.
var lastFetchAt sync.Map // trackerID -> fetchAttempt

// fetchAttempt is one lastFetchAt entry. errKind/err are "" for a success;
// for a failure they are what that refresh reported, so a caller served from
// the guard sees the same outcome rather than a false "ok".
type fetchAttempt struct {
	at           time.Time
	errKind, err string
}

// refreshLocks serialises refreshTracker per tracker, from the min-age guard
// through the fetch to the events and alerts it records — see the comment
// where it is taken.
var refreshLocks sync.Map // trackerID → *sync.Mutex

func lockRefresh(trackerID string) *sync.Mutex {
	m, _ := refreshLocks.LoadOrStore(trackerID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu
}

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

// connectionKind is the tracker_events kind for one contact CHANNEL.
//
// The two channels get their own kinds, and therefore their own de-dup state,
// because they fail INDEPENDENTLY. A tracker whose API returns 500s while its
// profile scrape still works alternates api-fail, scrape-ok, api-fail, … on
// every single refresh; against one shared state machine that reads as a
// down/up pair per cycle — hundreds of rows a day that say nothing except that
// the tracker is half-working. Split, the same week records exactly one event:
// "API unreachable — Server error (500)", and nothing more until it recovers.
//
// Events written before the split carry the bare kind "connection" with no
// channel. Nothing migrates them; the readers accept all three.
func connectionKind(source string) string { return "connection_" + source }

// recordConnection persists one contact attempt's outcome: the day's rollup
// (uptime strip / Health card) plus, on a state CHANGE of that channel, a
// timeline event.
//
// De-duping goes through the stored event rather than lastFetchState, which
// lives only in memory: after a restart that map is empty, so a tracker that
// was already down would otherwise record a second "went down" event on the
// first cycle. Comparing against the last persisted event makes restarts and
// repeat failures both no-ops, the same trick recordGroupChange uses.
func recordConnection(d *Deps, t models.Tracker, source, errKind string) {
	// Pre-flight failures never touched the network, so they are a
	// CONFIGURATION state, not a connection one. Recording them would pin
	// every scrape-only tracker (no API key by design) permanently
	// "unreachable" and hold the Health card red forever. Same exemption
	// recordScrapeAttempt applies on its side.
	if isPreflightKind(errKind) {
		return
	}
	now := time.Now().UTC()
	_ = d.DB.RecordConnection(t.ID, now, errKind == "", errKind, source)

	detail := "up"
	if errKind != "" {
		detail = "down:" + errKind
	}
	kind := connectionKind(source)
	last, err := d.DB.LatestEventDetail(t.ID, kind)
	if err != nil || last == detail {
		return
	}
	// Never open a channel's timeline with a recovery: with no prior event
	// there was no outage to recover FROM, just a channel that has always
	// worked. This is what keeps the healthy half of a half-broken tracker
	// silent — the scrape succeeding is not news.
	if last == "" && detail == "up" {
		return
	}
	if err := d.DB.AddEvent(t.ID, now, kind, detail); err == nil {
		d.logDebugf("event: %s (%s) %s %s", t.Name, t.ID, kind, detail)
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
		Gazelle:         d.Reg.TypeKeyFor(t.URL, t.Type) == gazelleANTNEBType,
		KnownUserID:     mergedString(d, t.ID, "user_id"),
	}
	result, serr := scrape.Profile(t, spec)
	recordScrapeAttempt(d, t, serr, len(result))
	if serr != nil || len(result) == 0 {
		return
	}
	_ = d.Stats.SaveScrape(t.ID, toAnyMap(result))
}

// isPreflightKind reports whether an error kind means "we never sent a
// request" — missing credentials or no definition to fetch with. These say
// nothing about whether the tracker is up.
func isPreflightKind(kind string) bool {
	switch kind {
	case "no_key", "no_username", "no_cookie", "no_def":
		return true
	}
	return false
}

// recordScrapeAttempt logs a scrape in the rate-limit ledger whenever an HTTP
// request actually reached the tracker — including failed ones. A profile
// page that errors must not get re-hit on every refresh cycle; only
// pre-flight failures (no username/key — nothing was sent) are exempt.
// The outcome (ok / error kind) feeds the scrape-health surface: failure
// streaks and the expired-session-cookie warning, plus the connection-health
// record shared with the API fetch path.
func recordScrapeAttempt(d *Deps, t models.Tracker, serr *scrape.Error, fieldsScraped int) {
	if serr != nil && (serr.Kind == "no_username" || serr.Kind == "no_cookie" || serr.Kind == "no_key") {
		return // pre-flight failure — no request reached the tracker
	}
	kind := ""
	if serr != nil {
		kind = serr.Kind
	}
	_ = d.DB.RecordScrape(t.ID, time.Now().UTC(), serr == nil, kind)
	recordConnection(d, t, "scrape", kind)
	recordRefreshCheck(d, t.ID, "scrape", kind, fieldsScraped)
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
	// One qui pull feeds every tracker's seedsize fallback for this cycle
	// (cheap no-op when the mode is off; see quiseed.go).
	refreshQUI(d)
	cyc := &refreshCycle{}
	for _, t := range d.Cfg.Trackers() {
		if !t.Enabled {
			continue
		}
		_ = refreshTrackerInCycle(d, t, false, cyc) // background loop → guarded
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
		cyc := &refreshCycle{}
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
				res := refreshTrackerInCycle(d, t, force, cyc)
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
