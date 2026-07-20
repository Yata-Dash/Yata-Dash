// digest.go builds and delivers the weekly digest notification: per-tracker
// deltas over the trailing 7 days, target/goal progress fragments, this
// week's group promotions/demotions, and newly requirements-met pathway
// targets. Scheduling (digestDueAt) is a pure function so catch-up/boundary
// behaviour is table-testable without touching the clock; RunDigestIfDue is
// the thin wrapper the 5-minute goroutine in cmd/yata/main.go calls.
package api

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// The HTTP handlers below (previewDigest, sendDigestNow) are registered by
// registerNotifications in notifications.go, alongside the rest of the
// notifications API.

// ─────────────────────────────────────────────────────────────────────────────
// Scheduling
// ─────────────────────────────────────────────────────────────────────────────

// digestDueAt computes the most recent scheduled instant (this week's
// weekday+hour in server-local time, or last week's if today's hasn't
// arrived yet) and whether the digest is due: enabled AND never sent since
// that instant. This gives catch-up for free — a server that was off at the
// scheduled moment sends on the next tick after boot, still reporting "this
// week" (buildDigest's window is always the trailing 7 days from now, not
// from the missed instant).
func digestDueAt(cfg models.DigestConfig, now time.Time) (time.Time, bool) {
	if !cfg.Enabled {
		return time.Time{}, false
	}
	loc := now.Location()
	scheduled := time.Date(now.Year(), now.Month(), now.Day(), cfg.Hour, 0, 0, 0, loc)
	delta := int(scheduled.Weekday()) - cfg.Weekday
	if delta < 0 {
		delta += 7
	}
	scheduled = scheduled.AddDate(0, 0, -delta)
	if scheduled.After(now) {
		scheduled = scheduled.AddDate(0, 0, -7)
	}
	return scheduled, cfg.LastSentAt < scheduled.Unix()
}

// RunDigestIfDue sends the weekly digest when it's due (see digestDueAt) —
// called from its own 5-minute goroutine in cmd/yata/main.go, deliberately
// NOT piggybacked on the refresh loop's cadence: that cadence is user-
// configurable, so tying the digest to it would either delay delivery by up
// to that interval or require reasoning about drift.
func RunDigestIfDue(d *Deps) {
	now := time.Now()
	cfg := d.Cfg.Notifications()
	if _, due := digestDueAt(cfg.Digest, now); !due {
		return
	}
	sentTo, err := deliverDigest(d, now)
	switch {
	case err != nil:
		d.logWarnf("digest: %v", err)
	case sentTo == 0:
		d.logInfof("digest: due but no destinations resolved — skipping until one is enabled")
	default:
		d.logInfof("digest: sent to %d destination(s)", sentTo)
	}
}

// deliverDigest builds, sends, and — on any successful delivery — persists
// the digest state. Shared by the scheduler (RunDigestIfDue) and the manual
// "Send now" button (sendDigestNow) so both behave identically. sentTo==0
// with a nil error means no destinations resolved (nothing to send to, state
// left untouched so enabling a destination later still gets the overdue digest).
func deliverDigest(d *Deps, now time.Time) (sentTo int, err error) {
	cfg := d.Cfg.Notifications()
	text, readyNow := buildDigest(d, now)
	dests := notify.ResolveDestinations(cfg, cfg.Digest.Destinations)
	if len(dests) == 0 {
		return 0, nil
	}
	for _, dest := range dests {
		if sendErr := notify.SendChunked(dest, "Yata weekly digest", text); sendErr != nil {
			d.logWarnf("digest: send to %s failed: %v", dest.Name, sendErr)
			continue
		}
		sentTo++
	}
	if sentTo == 0 {
		return 0, fmt.Errorf("delivery failed to every destination")
	}
	if err := d.Cfg.UpdateDigestState(now.Unix(), readyNow); err != nil {
		return sentTo, fmt.Errorf("sent but failed to persist digest state: %w", err)
	}
	return sentTo, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Digest builder
// ─────────────────────────────────────────────────────────────────────────────

// digestWindowDays is the fixed reporting window — a late catch-up digest
// still reports "this week", not a stretched 9-day mash.
const digestWindowDays = 7

// buildDigest composes the digest text and returns the FULL current
// requirements-met pathway target set as readyNow — not just the newly-met
// ones — so the caller's snapshot reflects reality even when nothing new
// happened. A target flapping ready/unready between sends would otherwise
// re-announce every week it happens to be ready.
func buildDigest(d *Deps, now time.Time) (text string, readyNow []string) {
	since := now.Add(-digestWindowDays * 24 * time.Hour)
	cfg := d.Cfg.Notifications()

	allTrackers := d.Cfg.Trackers()
	trackersByID := make(map[string]models.Tracker, len(allTrackers))
	for _, t := range allTrackers {
		trackersByID[t.ID] = t
	}

	var trackerLines []string
	watched := 0
	anyMovement := false
	for _, t := range allTrackers {
		if !t.Enabled {
			continue
		}
		watched++
		merged, err := d.Stats.Merged(t.ID)
		if err != nil {
			merged = models.MergedStats{}
		}
		rates := d.Stats.GrowthRates(t.ID)
		line, moved := trackerDigestLine(d, t, merged, rates, since, now)
		trackerLines = append(trackerLines, line)
		if moved {
			anyMovement = true
		}
	}

	events := digestEventLines(d, trackersByID, since)

	readyNow = readyPathwayTargetNames(d)
	oldReady := make(map[string]bool, len(cfg.Digest.LastReadyTargets))
	for _, n := range cfg.Digest.LastReadyTargets {
		oldReady[n] = true
	}
	var newlyMet []string
	for _, n := range readyNow {
		if !oldReady[n] {
			newlyMet = append(newlyMet, n)
		}
	}

	if !anyMovement && len(events) == 0 && len(newlyMet) == 0 {
		text = fmt.Sprintf("All quiet this week — no stat movement, no group changes. %d tracker%s watched.",
			watched, plural(watched))
		return text, readyNow
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Yata weekly digest — %s → %s\n", since.Format("Jan 2"), now.Format("Jan 2"))
	for _, line := range trackerLines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if len(events) > 0 {
		b.WriteString("\nThis week:\n")
		for _, e := range events {
			b.WriteString(e)
			b.WriteString("\n")
		}
	}
	if len(newlyMet) > 0 {
		b.WriteString("\nNewly requirements-met: " + strings.Join(newlyMet, ", ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), readyNow
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// trackerDigestLine builds one tracker's digest line: newest-minus-oldest
// deltas over the window, then any target/goal fragments. moved reports
// whether any field actually changed, for the caller's quiet-week check.
func trackerDigestLine(d *Deps, t models.Tracker, merged models.MergedStats, rates map[string]float64, since, now time.Time) (line string, moved bool) {
	daily, _ := d.DB.DailySince(t.ID, since)
	fine, _ := d.DB.TrackerHistorySince(t.ID, since)
	byDay := groupHistoryByField(daily)
	byFine := groupHistoryByField(fine)

	var parts []string
	if delta, ok := fieldDelta(byDay["uploaded"], byFine["uploaded"]); ok {
		parts = append(parts, fmt.Sprintf("↑%s up", fmtGiBDelta(delta)))
		if delta != 0 {
			moved = true
		}
	}
	if delta, ok := fieldDelta(byDay["downloaded"], byFine["downloaded"]); ok {
		parts = append(parts, fmt.Sprintf("↓%s down", fmtGiBDelta(delta)))
		if delta != 0 {
			moved = true
		}
	}
	if delta, ok := fieldDelta(byDay["buffer"], byFine["buffer"]); ok {
		parts = append(parts, "buffer "+fmtGiBDeltaSigned(delta))
		if delta != 0 {
			moved = true
		}
	}
	if oldR, newR, ok := fieldOldNew(byDay["ratio"], byFine["ratio"]); ok {
		parts = append(parts, fmt.Sprintf("ratio %.2f→%.2f", oldR, newR))
		if oldR != newR {
			moved = true
		}
	}

	base := strings.Join(parts, " · ")
	if base == "" {
		base = "no change"
	}
	line = t.Name + ": " + base

	if frag := digestTargetFragment(d, t, merged); frag != "" {
		line += " · " + frag
	}
	if frag := digestGoalFragment(t, merged, rates, now); frag != "" {
		line += " · " + frag
	}
	return line, moved
}

// groupHistoryByField mirrors internal/stats's unexported groupByField for
// the store.HistoryPoint slices DailySince/TrackerHistorySince return.
func groupHistoryByField(points []store.HistoryPoint) map[string][]store.HistoryPoint {
	out := map[string][]store.HistoryPoint{}
	for _, p := range points {
		out[p.Field] = append(out[p.Field], p)
	}
	return out
}

// fieldDelta returns newest-minus-oldest for one field, preferring daily
// rollups and falling back to fine-grained history for a tracker too young
// to have 2 daily points yet (same daily/fine preference as
// stats.Engine.GrowthRates, but a raw delta rather than a per-day rate — a
// late catch-up digest still reports the week's actual movement). ok is
// false when neither source has at least 2 points (no data to diff).
func fieldDelta(daily, fine []store.HistoryPoint) (float64, bool) {
	if len(daily) >= 2 {
		return daily[len(daily)-1].Value - daily[0].Value, true
	}
	if len(fine) >= 2 {
		return fine[len(fine)-1].Value - fine[0].Value, true
	}
	return 0, false
}

// fieldOldNew is fieldDelta's old/new-value variant, for ratio's "old→new"
// display (a delta figure alone wouldn't read meaningfully for a ratio).
func fieldOldNew(daily, fine []store.HistoryPoint) (oldV, newV float64, ok bool) {
	if len(daily) >= 2 {
		return daily[0].Value, daily[len(daily)-1].Value, true
	}
	if len(fine) >= 2 {
		return fine[0].Value, fine[len(fine)-1].Value, true
	}
	return 0, 0, false
}

// fmtGiBDelta renders an unsigned-magnitude GiB delta — mirrors
// web/src/utils/format.ts's fmtGiBRate exactly (same unit thresholds), minus
// the per-day framing: this is a 7-day delta, not a rate.
func fmtGiBDelta(gib float64) string {
	sign := ""
	if gib < 0 {
		sign = "-"
	}
	g := math.Abs(gib)
	switch {
	case g >= 1024:
		return fmt.Sprintf("%s%.2f TiB", sign, g/1024)
	case g >= 1:
		return fmt.Sprintf("%s%.1f GiB", sign, g)
	case g >= 1.0/1024:
		return fmt.Sprintf("%s%.1f MiB", sign, g*1024)
	default:
		return fmt.Sprintf("%s%.0f KiB", sign, g*1024*1024)
	}
}

// fmtGiBDeltaSigned is fmtGiBDelta with an explicit "+" for non-negative
// values — used for buffer, which (unlike uploaded/downloaded) can shrink,
// so the sign carries real information instead of a fixed arrow.
func fmtGiBDeltaSigned(gib float64) string {
	if gib >= 0 {
		return "+" + fmtGiBDelta(gib)
	}
	return fmtGiBDelta(gib)
}

// digestTargetFragment renders the "targets 3/5 met" fragment (evaluateTargetRows'
// logical m/T), empty when the tracker tracks nothing.
func digestTargetFragment(d *Deps, t models.Tracker, merged models.MergedStats) string {
	if len(t.Targets) == 0 && t.TargetGroup == "" {
		return ""
	}
	var groups []defs.GroupDef
	if td, ok := d.Reg.TrackerByURL(t.URL); ok {
		groups = td.Groups
	}
	rows, met, total := evaluateTargetRows(t, merged, groups)
	if len(rows) == 0 || total == 0 {
		return ""
	}
	return fmt.Sprintf("targets %d/%d met", met, total)
}

// digestGoalFragment renders one goal-pacing verdict fragment per the
// worst state present, reusing computeGoalPacing/targetLabel/
// formatGoalRequired (pacing.go) the same way goalsBehindLabels does:
// any overdue row → "goal: overdue (Field, …)"; else any behind row →
// "goal: behind (Field needs X/day, …)"; else any on-track row →
// "goal: on track"; else "" (no dated rows, or only done/no_rate/ratio_info).
func digestGoalFragment(t models.Tracker, merged models.MergedStats, rates map[string]float64, now time.Time) string {
	if len(t.TargetDeadlines) == 0 {
		return ""
	}
	var overdue, behind []string
	anyOnTrack := false
	for _, key := range sortedTargetKeys(t.TargetDeadlines) {
		deadline := t.TargetDeadlines[key]
		tgt, hasTarget := t.Targets[key]
		if !hasTarget || deadline == "" {
			continue
		}
		p, ok := computeGoalPacing(key, tgt, deadline, merged, rates, now)
		if !ok {
			continue
		}
		switch p.State {
		case PacingOverdue:
			overdue = append(overdue, targetLabel(key))
		case PacingBehind:
			behind = append(behind, fmt.Sprintf("%s needs %s", targetLabel(key), formatGoalRequired(key, p.Required)))
		case PacingOnTrack:
			anyOnTrack = true
		}
	}
	switch {
	case len(overdue) > 0:
		return "goal: overdue (" + strings.Join(overdue, ", ") + ")"
	case len(behind) > 0:
		return "goal: behind (" + strings.Join(behind, ", ") + ")"
	case anyOnTrack:
		return "goal: on track"
	default:
		return ""
	}
}

// digestEventLines renders this window's group_change events as "This
// week:" lines: "▲ Name promoted Old → New (Day)" / "▼ … demoted …" / a
// neutral "•" when the def's group ladder can't rank the two groups.
func digestEventLines(d *Deps, trackersByID map[string]models.Tracker, since time.Time) []string {
	evs, _ := d.DB.EventsSince(nil, since)
	var lines []string
	for _, e := range evs {
		if e.Kind != "group_change" {
			continue
		}
		t, ok := trackersByID[e.TrackerID]
		if !ok {
			continue
		}
		oldGroup, newGroup, ok := strings.Cut(e.Detail, "→")
		if !ok {
			continue
		}
		var groups []defs.GroupDef
		if td, ok := d.Reg.TrackerByURL(t.URL); ok {
			groups = td.Groups
		}
		oldIdx, newIdx := groupLadderIndex(groups, oldGroup), groupLadderIndex(groups, newGroup)
		symbol, verb := "•", "changed"
		switch {
		case oldIdx >= 0 && newIdx >= 0 && newIdx > oldIdx:
			symbol, verb = "▲", "promoted"
		case oldIdx >= 0 && newIdx >= 0 && newIdx < oldIdx:
			symbol, verb = "▼", "demoted"
		}
		day := time.Unix(e.At, 0).Format("Mon")
		lines = append(lines, fmt.Sprintf("%s %s %s %s → %s (%s)", symbol, t.Name, verb, oldGroup, newGroup, day))
	}
	return lines
}

// readyPathwayTargetNames returns the sorted names of pathway targets the
// user currently meets all listed requirements for (≥1 active direct route)
// and doesn't already own — the same "reqs_met" evaluation
// GET /api/pathways/targets uses, via pathwayReadiness (pathways.go). nil
// when the pathways dataset isn't loaded (feature hidden).
func readyPathwayTargetNames(d *Deps) []string {
	if d.Paths == nil {
		return nil
	}
	mine, ready := pathwayReadiness(d)
	out := make([]string, 0, len(ready))
	for name := range ready {
		if !mine[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP handlers (registered by registerNotifications in notifications.go)
// ─────────────────────────────────────────────────────────────────────────────

// previewDigest builds the digest text without sending or mutating any
// state — the Alerts tab's "Preview" button.
func previewDigest(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		text, _ := buildDigest(d, time.Now())
		jsonOK(w, map[string]any{"text": text})
	}
}

// sendDigestNow builds and sends the digest right now, independent of the
// schedule — the Alerts tab's "Send now" button. Behaves exactly like a
// scheduled send: same builder, same destination resolution, same state
// persisted on success.
func sendDigestNow(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		sentTo, err := deliverDigest(d, time.Now())
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadGateway)
			return
		}
		if sentTo == 0 {
			jsonError(w, "no destinations enabled", http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]any{"ok": true, "sent_to": sentTo})
	}
}
