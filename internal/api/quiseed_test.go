package api

import "testing"

// TestHostMatches: announce hosts must map to the right tracker site and
// ONLY that tracker — a public tracker's host or a sibling mirror domain
// must never leak a seedsize onto the wrong tracker.
func TestHostMatches(t *testing.T) {
	cases := []struct {
		announce, site string
		want           bool
	}{
		{"oldtoons.world", "oldtoons.world", true},
		{"peer.retroflix.club", "retroflix.club", true},
		{"ramjet.speedapp.io", "speedapp.io", true},
		// Mirror domains with a different registrable name don't match —
		// they duplicate a host that does, and per-instance MAX absorbs it.
		{"ramjet.speedapp.to", "speedapp.io", false},
		{"ramjet.speedappio.org", "speedapp.io", false},
		// Public/unrelated hosts must never match a configured tracker.
		{"nyaa.tracker.wf", "oldtoons.world", false},
		// Suffix matching is on dot boundaries only.
		{"notoldtoons.world", "oldtoons.world", false},
		{"oldtoons.world.evil.com", "oldtoons.world", false},
	}
	for _, tc := range cases {
		if got := hostMatches(tc.announce, tc.site); got != tc.want {
			t.Errorf("hostMatches(%q, %q) = %v, want %v", tc.announce, tc.site, got, tc.want)
		}
	}
}

// TestTrackerSiteHosts: a tracker configured under one domain must also
// match announce hosts on its def's alias domains — RetroFlix's site is
// retroflix.net but it announces on peer.retroflix.club, and its def lists
// retroflix.club as an alias.
func TestTrackerSiteHosts(t *testing.T) {
	d := testDeps(t)
	hosts := trackerSiteHosts(d, "https://retroflix.net")
	want := map[string]bool{"retroflix.net": false, "retroflix.club": false}
	for _, h := range hosts {
		if _, ok := want[h]; ok {
			want[h] = true
		}
	}
	for h, found := range want {
		if !found {
			t.Errorf("trackerSiteHosts should include %s, got %v", h, hosts)
		}
	}
	// The announce host resolves through the alias.
	matched := false
	for _, h := range hosts {
		if hostMatches("peer.retroflix.club", h) {
			matched = true
		}
	}
	if !matched {
		t.Error("peer.retroflix.club should match via the retroflix.club alias")
	}
	// A tracker with no def still matches on its own URL.
	if hosts := trackerSiteHosts(d, "https://example-no-def.org"); len(hosts) != 1 || hosts[0] != "example-no-def.org" {
		t.Errorf("def-less tracker should keep its own host, got %v", hosts)
	}
}

func TestHostOfURLAndNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://oldtoons.world", "oldtoons.world"},
		{"https://www.speedapp.io/", "speedapp.io"},
		{"http://retroflix.club:8080/path", "retroflix.club"},
		{"lst.gg", "lst.gg"}, // schemeless config URL
	}
	for _, tc := range cases {
		if got := normalizeHost(hostOfURL(tc.in)); got != tc.want {
			t.Errorf("normalizeHost(hostOfURL(%q)) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
