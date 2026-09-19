package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// QUI seedsize: qui's torrents endpoint reports, per announce host, the total
// size of the torrents it can see (counts.trackerTransfers[host].totalSize).
// When Settings.QUISeedsizeMode is on, that number is written into each
// matching tracker's "qui" stat layer as seed_size, where the merge slots it
// under or over scrapes per the mode (never over the tracker's own API — see
// stats.Engine.Merged). It's a client-side calculation over one (or a few)
// qBittorrent instances, so it can undercount for multi-client or seedbox
// setups; the tracker-reported figure is always the truth for progressions.

// quiTrackerTransfer is one announce host's totals in the torrents response.
type quiTrackerTransfer struct {
	TotalSize int64 `json:"totalSize"`
}

// quiTorrentsCounts is the (partial) shape of GET
// /api/instances/{id}/torrents?page=0&limit=1: the per-host byte totals that
// feed seed size, and the instance-wide status counts that say which problem
// classes are worth a filtered fetch at all.
type quiTorrentsCounts struct {
	Counts struct {
		Status           map[string]int                `json:"status"`
		TrackerTransfers map[string]quiTrackerTransfer `json:"trackerTransfers"`
	} `json:"counts"`
}

// quiSnapshot is one instance's answer for this refresh.
type quiSnapshot struct {
	seed     map[string]int64            // announce host → max totalSize
	problems map[string]quiProblemCounts // announce host → counts (alerts on only)
}

// refreshQUI polls every enabled qui instance once and rewrites the qui stat
// layer of every enabled tracker with whatever is switched on: seed size
// (Settings.QUISeedsizeMode) and the per-tracker problem counts the alert
// rules read (Settings.QUIAlertsEnabled). One writer, because the layer is
// replaced wholesale — two independent halves would each wipe the other's
// fields. No-op when neither is on or qui isn't configured. Errors are logged
// and skipped: qui being down must never disturb a refresh cycle.
//
// Absence is the honest answer whenever qui cannot be read: a rule on
// qui_unregistered must go quiet then, not read 0 and announce "all clear".
// So counts are written only when EVERY instance answered — a floor from the
// instances that did would under-report exactly when the user most needs the
// number — and the previous counts are carried across otherwise.
func refreshQUI(d *Deps) {
	set := d.Cfg.Settings()
	seedOn := set.QUISeedsizeMode == "missing" || set.QUISeedsizeMode == "prefer"
	alertsOn := set.QUIAlertsEnabled
	if (!seedOn && !alertsOn) || set.QUIURL == "" {
		return
	}
	instances := set.QUIEnabledInstances
	if len(instances) == 0 {
		instances = []int{1}
	}

	// Announce host → max totalSize across that host's entries, per instance,
	// SUMMED across instances afterwards. Within one instance a tracker's
	// mirror hosts (speedapp's three announce domains) all report the same
	// torrents, so matching takes the MAX across hosts; across instances the
	// torrent sets are genuinely different boxes, so those add.
	var snaps []quiSnapshot
	truncated := false
	for _, id := range instances {
		u := fmt.Sprintf("%s/api/instances/%d/torrents?page=0&limit=1", set.QUIURL, id)
		body, _, err := quiFetch(u, set.QUIAPIKey)
		if err != nil {
			d.logDebugf("qui: instance %d fetch failed: %v", id, err)
			continue
		}
		var data quiTorrentsCounts
		if err := json.Unmarshal(body, &data); err != nil {
			d.logDebugf("qui: instance %d parse failed: %v", id, err)
			continue
		}
		snap := quiSnapshot{seed: map[string]int64{}}
		for host, tt := range data.Counts.TrackerTransfers {
			if h := normalizeHost(host); h != "" && tt.TotalSize > snap.seed[h] {
				snap.seed[h] = tt.TotalSize
			}
		}
		if alertsOn {
			problems, trunc, err := fetchQUIProblems(set.QUIURL, set.QUIAPIKey, id, data.Counts.Status)
			if err != nil {
				d.logDebugf("qui: instance %d problem counts failed: %v", id, err)
				continue // this instance did not fully answer
			}
			snap.problems = problems
			truncated = truncated || trunc
		}
		snaps = append(snaps, snap)
	}
	if len(snaps) == 0 {
		return // qui unreachable — keep whatever layers exist rather than wiping
	}
	// If some (but not all) instances failed, a tracker legitimately seeding
	// only on a down instance would otherwise read as total==0 and get its
	// layer wiped. Treat that case as unknown rather than confirmed-empty.
	partial := len(snaps) < len(instances)
	if truncated {
		d.logWarnf("qui: more than %d torrents in a problem class — counts are a floor", quiProblemLimit)
	}

	for _, t := range d.Cfg.Trackers() {
		if !t.Enabled {
			continue
		}
		siteHosts := trackerSiteHosts(d, t.URL)
		if len(siteHosts) == 0 {
			continue
		}
		existing := map[string]store.FieldValue{}
		if layers, err := d.Stats.DB.Layers(t.ID); err == nil {
			existing = layers[string(models.SourceQUI)]
		}
		layer := map[string]any{}

		if seedOn {
			var total int64
			for _, snap := range snaps {
				// MAX across every candidate domain, not a sum: a tracker's
				// alias domains (retroflix.net / retroflix.club) and mirror
				// hosts all announce the same torrents.
				var best int64
				for h, size := range snap.seed {
					for _, site := range siteHosts {
						if hostMatches(h, site) && size > best {
							best = size
						}
					}
				}
				total += best
			}
			switch {
			case total > 0:
				layer["seed_size"] = parse.BytesToSize(total)
			case partial:
				// An instance is down and this tracker read zero — ambiguous,
				// keep what it had rather than risk wiping real data.
				if fv, ok := existing["seed_size"]; ok {
					layer["seed_size"] = fv.Value
				}
			}
			// else: every instance answered and none has it — cleared.
		}

		if alertsOn {
			if partial {
				for k, fv := range existing {
					if strings.HasPrefix(k, "qui_") {
						layer[k] = fv.Value
					}
				}
			} else {
				var c quiProblemCounts
				for _, snap := range snaps {
					s := sumQUIProblems(snap.problems, siteHosts)
					c.Unregistered += s.Unregistered
					c.TrackerDown += s.TrackerDown
					c.TrackerError += s.TrackerError
					c.Errored += s.Errored
				}
				// Zeros are written on purpose: a count that fell to zero is
				// how a "> 0" rule clears, and "qui answered, nothing wrong"
				// is a different fact from "qui did not answer".
				layer["qui_unregistered"] = c.Unregistered
				layer["qui_tracker_down"] = c.TrackerDown
				layer["qui_tracker_error"] = c.TrackerError
				layer["qui_errored"] = c.Errored
			}
		}
		_ = d.Stats.SaveQUI(t.ID, layer)
	}
	d.logDebugf("qui: refreshed from %d instance(s)", len(snaps))
}

// trackerSiteHosts returns every domain a tracker is known by: its
// configured URL plus, when a def matches, the def's canonical URL and
// aliases. Trackers announce on domains the user didn't configure —
// RetroFlix's site is retroflix.net but it announces on retroflix.club,
// which its def lists as an alias.
func trackerSiteHosts(d *Deps, rawURL string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		if h := normalizeHost(hostOfURL(u)); h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	add(rawURL)
	if td, ok := d.Reg.TrackerByURL(rawURL); ok {
		add(td.URL)
		for _, a := range td.Aliases {
			add(a)
		}
	}
	return out
}

// hostOfURL extracts the hostname of a tracker URL ("" when unparseable).
func hostOfURL(raw string) string {
	if !strings.Contains(raw, "//") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// normalizeHost lowercases and strips the noise prefixes that don't change
// identity ("www."). Ports never appear in announce-host keys.
func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	return strings.TrimPrefix(h, "www.")
}

// hostMatches reports whether an announce host belongs to a tracker site
// host: exact match, or a subdomain of it ("peer.retroflix.club" →
// retroflix.club, "ramjet.speedapp.io" → speedapp.io). Sibling mirror
// domains on a different registrable name (speedapp.to, speedappio.org)
// don't match — they duplicate a host that does, and the per-instance MAX
// makes that loss free. Unrelated trackers (public announce hosts, other
// sites) must never match.
func hostMatches(announce, site string) bool {
	return announce == site || strings.HasSuffix(announce, "."+site)
}
