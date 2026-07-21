package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/Yata-Dash/Yata-Dash/internal/parse"
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
// /api/instances/{id}/torrents?page=0&limit=1 — everything but the per-host
// totals is ignored.
type quiTorrentsCounts struct {
	Counts struct {
		TrackerTransfers map[string]quiTrackerTransfer `json:"trackerTransfers"`
	} `json:"counts"`
}

// refreshQUISeedsize fetches per-tracker seeding totals from every enabled
// qui instance and rewrites the qui stat layer of every enabled tracker —
// including CLEARING it where qui has nothing, so a removed torrent set or a
// renamed announce host can't leave a stale seed_size behind. No-op when the
// mode is off or qui isn't configured. Errors are logged and skipped: qui
// being down must never disturb a refresh cycle.
func refreshQUISeedsize(d *Deps) {
	set := d.Cfg.Settings()
	if set.QUISeedsizeMode != "missing" && set.QUISeedsizeMode != "prefer" {
		return
	}
	if set.QUIURL == "" {
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
	perInstance := make([]map[string]int64, 0, len(instances))
	for _, id := range instances {
		u := fmt.Sprintf("%s/api/instances/%d/torrents?page=0&limit=1", set.QUIURL, id)
		body, _, err := quiFetch(u, set.QUIAPIKey)
		if err != nil {
			d.logDebugf("qui seedsize: instance %d fetch failed: %v", id, err)
			continue
		}
		var data quiTorrentsCounts
		if err := json.Unmarshal(body, &data); err != nil {
			d.logDebugf("qui seedsize: instance %d parse failed: %v", id, err)
			continue
		}
		hosts := map[string]int64{}
		for host, tt := range data.Counts.TrackerTransfers {
			if h := normalizeHost(host); h != "" && tt.TotalSize > hosts[h] {
				hosts[h] = tt.TotalSize
			}
		}
		perInstance = append(perInstance, hosts)
	}
	if len(perInstance) == 0 {
		return // qui unreachable — keep whatever layers exist rather than wiping
	}

	for _, t := range d.Cfg.Trackers() {
		if !t.Enabled {
			continue
		}
		siteHosts := trackerSiteHosts(d, t.URL)
		if len(siteHosts) == 0 {
			continue
		}
		var total int64
		for _, hosts := range perInstance {
			// MAX across every candidate domain, not a sum: a tracker's
			// alias domains (retroflix.net / retroflix.club) and mirror
			// hosts all announce the same torrents.
			var best int64
			for h, size := range hosts {
				for _, site := range siteHosts {
					if hostMatches(h, site) && size > best {
						best = size
					}
				}
			}
			total += best
		}
		if total > 0 {
			_ = d.Stats.SaveQUI(t.ID, map[string]any{"seed_size": parse.BytesToSize(total)})
		} else {
			_ = d.Stats.SaveQUI(t.ID, map[string]any{}) // clear — nothing seeding there now
		}
	}
	d.logDebugf("qui seedsize: refreshed from %d instance(s)", len(perInstance))
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
