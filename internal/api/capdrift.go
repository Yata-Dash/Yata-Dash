package api

import (
	"sort"
	"strings"
	"sync"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// Capability declarations are written by hand, which is the right trade —
// most defs carry no field information to derive from, and the people who
// know what a tracker returns are the ones editing its def. The cost is that
// they can quietly stop being true: a tracker adds a field, or removes one,
// and nothing says so.
//
// So every successful fetch compares what actually arrived against what the
// def claims, and reports the difference. It never changes behaviour — the
// stats are stored either way — it just stops a stale declaration from going
// unnoticed until someone wonders why an icon is wrong.

// capDriftSeen suppresses repeats: the same tracker returning the same
// unexpected field every few minutes would bury the log. One report per
// tracker per distinct discrepancy per run.
var capDriftSeen sync.Map // trackerID + "|" + signature → struct{}

// capDriftIgnored are fields that legitimately appear without being declared,
// because they are computed by Yata rather than reported by the tracker.
var capDriftIgnored = map[string]bool{
	// Derived inside the fetchers from the byte counts.
	"buffer": true, "ratio": true,
	// Derived from active_events by normalizeActiveEvents.
	"active_event": true, "active_event_ends_at": true,
}

// checkCapabilityDrift compares a fetch result against the tracker's declared
// capabilities and logs anything that disagrees.
//
// Only the "returned but not declared" direction is reported eagerly. The
// reverse — declared but absent — is far noisier and often legitimate: an
// optional field the account simply has no value for (no warnings, no active
// event) is missing from a perfectly healthy response. Reporting that on every
// fetch would train everyone to ignore the message.
func checkCapabilityDrift(d *Deps, t models.Tracker, data map[string]any) {
	td, ok := d.Reg.TrackerByURL(t.URL)
	if !ok {
		return // no def, nothing to have declared
	}
	caps := d.Reg.ResolveCapabilities(t.URL, t.Type)
	// A def that describes its own API is derived from directly, so a mismatch
	// would be a bug in the derivation rather than a stale declaration.
	if caps.Derived || len(caps.APIStats) == 0 {
		return
	}
	declared := make(map[string]bool, len(caps.APIStats))
	for _, f := range caps.APIStats {
		declared[f] = true
	}

	var undeclared []string
	for field, v := range data {
		if declared[field] || capDriftIgnored[field] {
			continue
		}
		// An empty value is not evidence the tracker reports the field.
		if v == nil || v == "" {
			continue
		}
		undeclared = append(undeclared, field)
	}
	if len(undeclared) == 0 {
		return
	}
	sort.Strings(undeclared)

	sig := t.ID + "|" + strings.Join(undeclared, ",")
	if _, dup := capDriftSeen.LoadOrStore(sig, struct{}{}); dup {
		return
	}
	d.logWarnf("capabilities: %s returned %s, which its definition (defs/trackers/%s.json) "+
		"doesn't declare — add to capabilities.api_stats_add so the tracker's coverage is reported correctly",
		t.Name, strings.Join(undeclared, ", "), td.Key)
}

// resetCapDriftMemory clears the suppression map. Called when defs are
// reloaded: a corrected declaration should stop warning immediately, and a
// newly-broken one should be reported again rather than staying silent
// because an earlier version already warned.
func resetCapDriftMemory() {
	capDriftSeen.Range(func(k, _ any) bool {
		capDriftSeen.Delete(k)
		return true
	})
}
