package api

import (
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// groupLadderMaxAge is how long a cached ladder is trusted before Yata asks
// again. Ladders change a couple of times in a week and then sit still for a
// year, so a daily check is already far more often than the data moves; the
// out-of-ladder trigger below is what covers the case where it matters.
const groupLadderMaxAge = 24 * time.Hour

// groupsFor returns the ranks Yata should measure a tracker against: the
// ladder the tracker's own API last served, or the def's hand-written one when
// there is no cached ladder.
//
// Every consumer of TrackerDef.Groups goes through here, so a live ladder is
// indistinguishable downstream from a shipped one.
//
// The fallback is the cached API response and NEVER a def for a platform that
// serves its own ladder — the point of the endpoint is that a build-time copy
// is the thing being replaced, so a def on such a platform ships no groups and
// this correctly returns nothing until the first fetch lands.
func groupsFor(d *Deps, td defs.TrackerDef, trackerID string) []defs.GroupDef {
	if trackerID != "" && d.DB != nil {
		if spec := d.Reg.GroupAPI(td.URL, td.Type); spec != nil {
			if cached, ok := d.DB.LatestGroupLadder(trackerID); ok {
				if groups := defs.LadderFromAPI(cached.Payload, *spec); len(groups) > 0 {
					return applyHeldGroupStyle(d, trackerID, groups)
				}
			}
		}
	}
	return td.Groups
}

// applyHeldGroupStyle colours the rung the user currently holds, from the style
// the main stats endpoint reports alongside their rank.
//
// The ladder endpoint does not serve colours or icons yet, but /api/user does —
// for one rank, the one you are in. That is enough for everything the style is
// actually used for: the group badge, the styled username, and the perk icons
// all render the group you hold.
//
// Two deliberate limits. It styles ONLY the matching rung, because a field
// describing your own rank says nothing about any other. And it never overrides
// a style the ladder itself supplied, so the day /api/user/groups serves
// colours this quietly stops mattering rather than fighting it.
func applyHeldGroupStyle(d *Deps, trackerID string, groups []defs.GroupDef) []defs.GroupDef {
	if d.Stats == nil {
		return groups
	}
	merged, err := d.Stats.Merged(trackerID)
	if err != nil {
		return groups
	}
	held := mergedFieldString(merged, "group")
	color := mergedFieldString(merged, "group_color")
	icon := mergedFieldString(merged, "group_icon")
	if held == "" || (color == "" && icon == "") {
		return groups
	}
	i := defs.LadderIndex(groups, held)
	if i < 0 {
		return groups // a rank this ladder has never heard of; the refresh notices
	}
	// Copy before mutating: the slice was built from the cached payload for
	// this call, but a caller must never find a ladder it passed in altered.
	out := make([]defs.GroupDef, len(groups))
	copy(out, groups)
	if out[i].Style.Color == "" {
		out[i].Style.Color = color
	}
	if out[i].Style.Icon == "" {
		out[i].Style.Icon = icon
	}
	return out
}

// groupsForDef answers the same question where only a def is in hand — the
// defs browser and /api/tracker-groups, neither of which is looking at one
// tracker.
//
// It resolves the first configured tracker sharing this def, which is correct
// rather than approximate: a ladder is a property of the SITE, so whichever
// account fetched it, the answer is the same one.
func groupsForDef(d *Deps, td defs.TrackerDef) []defs.GroupDef {
	if d.Reg.GroupAPI(td.URL, td.Type) != nil {
		for _, t := range d.Cfg.Trackers() {
			if own, ok := d.Reg.TrackerByURL(t.URL); ok && own.Key == td.Key {
				if groups := groupsFor(d, td, t.ID); len(groups) > 0 {
					return groups
				}
			}
		}
	}
	return td.Groups
}

// maybeRefreshGroupLadder re-fetches a tracker's ladder when the cached copy
// has aged out, or when the user holds a rank that ladder has never heard of.
//
// That second trigger is the one that earns its place: a daily cadence is
// invisible until the moment it is wrong, and the moment it is wrong is a
// promotion into a rung Yata cannot name.
//
// Best-effort throughout, like the events and extended-stats endpoints: a
// ladder nobody can read is not worth failing a stat fetch that worked.
func maybeRefreshGroupLadder(d *Deps, t models.Tracker, currentGroup string) {
	if d.DB == nil || d.Fetch == nil {
		return
	}
	spec := d.Reg.GroupAPI(t.URL, t.Type)
	if spec == nil || spec.Path == "" {
		return
	}
	now := time.Now().UTC()
	if cached, ok := d.DB.LatestGroupLadder(t.ID); ok {
		fresh := now.Sub(cached.CheckedAt) < groupLadderMaxAge
		known := currentGroup == "" ||
			defs.LadderHasGroup(defs.LadderFromAPI(cached.Payload, *spec), currentGroup)
		if fresh && known {
			return
		}
	}
	payload, ferr := d.Fetch.FetchGroups(t)
	if ferr != nil {
		d.logDebugf("group ladder: %s (%s) — %s", t.Name, t.ID, ferr.Kind)
		return
	}
	before, had := d.DB.LatestGroupLadder(t.ID)
	if err := d.DB.SaveGroupLadder(t.ID, payload, now); err != nil {
		d.logDebugf("group ladder: %s (%s) — store error: %v", t.Name, t.ID, err)
		return
	}
	after, ok := d.DB.LatestGroupLadder(t.ID)
	changed := ok && !after.FirstSeen.Equal(before.FirstSeen)
	// A daily confirmation that nothing moved is not worth a line. The other
	// two cases are worth one each, and are NOT the same event: the first
	// ladder for a tracker is Yata learning the ranks, not the tracker
	// rewriting them, and reporting it as a change reads like the site altered
	// its requirements the moment you added it.
	switch {
	case !had:
		d.logInfof("group ladder: %s (%s) — %d ranks loaded from the tracker",
			t.Name, t.ID, len(defs.LadderFromAPI(payload, *spec)))
	case changed:
		d.logInfof("group ladder: %s (%s) — requirements changed", t.Name, t.ID)
	}
}
