package api

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// qui problem counts per tracker (QUI_ALERTS_PLAN.md §3.2).
//
// qui's `counts` block is computed over the whole torrent set and ignores
// filters, so a per-tracker breakdown cannot come from it. It comes from
// fetching the torrents that match each status class and grouping them by
// announce host — 15 of 10,000 on the reference instance, so cheap. A class
// with a zero instance-wide count is skipped without a request.

// quiProblemCounts is what a rule can condition on. Torrents, not bytes: an
// unregistered torrent is one the tracker dropped, and it stays that way until
// the user acts, so a standing count is outstanding work.
type quiProblemCounts struct {
	Unregistered int
	TrackerDown  int
	TrackerError int
	Errored      int
}

// quiProblemClasses maps a qui status filter to the counter it feeds. The
// order fixes the order requests go out in.
var quiProblemClasses = []struct {
	status string
	add    func(*quiProblemCounts)
}{
	{"unregistered", func(c *quiProblemCounts) { c.Unregistered++ }},
	{"tracker_down", func(c *quiProblemCounts) { c.TrackerDown++ }},
	{"tracker_error", func(c *quiProblemCounts) { c.TrackerError++ }},
	{"errored", func(c *quiProblemCounts) { c.Errored++ }},
}

// quiProblemLimit is qui's documented maximum page size.
const quiProblemLimit = 2000

// quiFilteredTorrents is the (partial) shape of a filtered torrents fetch.
// `tracker` is the torrent's primary announce URL — one per torrent, so
// grouping by it never double-counts a torrent across a tracker's mirrors
// the way per-host byte totals do.
type quiFilteredTorrents struct {
	Total    int `json:"total"`
	Torrents []struct {
		Tracker string `json:"tracker"`
	} `json:"torrents"`
}

// fetchQUIProblems returns, for one instance, problem counts keyed by
// normalised announce host. status is the instance-wide counts.status block
// from the counts fetch, used to skip classes that are empty. truncated is set
// when any class had more matches than one page holds: the counts are then a
// floor, and the caller decides what a floor is worth.
func fetchQUIProblems(quiURL, apiKey string, inst int, status map[string]int) (map[string]quiProblemCounts, bool, error) {
	out := map[string]quiProblemCounts{}
	truncated := false
	for _, class := range quiProblemClasses {
		if status[class.status] == 0 {
			continue
		}
		filters, _ := json.Marshal(map[string]any{"status": []string{class.status}})
		u := fmt.Sprintf("%s/api/instances/%d/torrents?page=0&limit=%d&filters=%s",
			quiURL, inst, quiProblemLimit, url.QueryEscape(string(filters)))
		body, _, err := quiFetch(u, apiKey)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", class.status, err)
		}
		var data quiFilteredTorrents
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, false, fmt.Errorf("%s: parse: %w", class.status, err)
		}
		if data.Total > len(data.Torrents) {
			truncated = true
		}
		for _, tor := range data.Torrents {
			h := normalizeHost(hostOfURL(tor.Tracker))
			if h == "" {
				continue
			}
			c := out[h]
			class.add(&c)
			out[h] = c
		}
	}
	return out, truncated, nil
}

// sumQUIProblems totals the counts for one tracker across every announce host
// that belongs to it, in one instance. A sum, unlike seed size's max: each
// torrent appears once under its primary announce host, so a tracker's mirror
// hosts hold different torrents, not the same ones twice.
func sumQUIProblems(hosts map[string]quiProblemCounts, siteHosts []string) quiProblemCounts {
	var total quiProblemCounts
	for h, c := range hosts {
		for _, site := range siteHosts {
			if hostMatches(h, site) {
				total.Unregistered += c.Unregistered
				total.TrackerDown += c.TrackerDown
				total.TrackerError += c.TrackerError
				total.Errored += c.Errored
				break
			}
		}
	}
	return total
}
