package scrape

import "testing"

// A UNIT3D-style page names several users with the same user-tag markup: the
// logged-in user in the top nav, other members in comments. Only the link to
// the scraped user's own profile may set the group.
const groupTitlePage = `<html><body>
<nav class="top-nav">
  <a class="user-tag__link fas fa-user" href="https://tracker.example.com/users/Alice" title="Power User">Alice</a>
</nav>
<section class="comments">
  <a class="user-tag__link fas fa-code" href="https://tracker.example.com/users/bob" title="SysOp">bob</a>
</section>
<dl><div class="key-value__group"><dt>Ratio</dt><dd>1.50</dd></div></dl>
</body></html>`

func TestGroupTitleClass(t *testing.T) {
	cases := []struct {
		name     string
		page     string
		cls      string
		username string
		want     string
	}{
		{"own link wins, case-insensitive", groupTitlePage, "user-tag__link", "alice", "Power User"},
		{"another user's link is ignored", groupTitlePage, "user-tag__link", "bob", "SysOp"},
		{"no class configured", groupTitlePage, "", "alice", ""},
		{"no username", groupTitlePage, "user-tag__link", "", ""},
		{"user not linked on the page", groupTitlePage, "user-tag__link", "carol", ""},
		{"relative href with trailing slash",
			`<a class="user-tag__link" href="/users/alice/" title="  Elite  ">alice</a>`,
			"user-tag__link", "alice", "Elite"},
		{"escaped username in href",
			`<a class="user-tag__link" href="/users/al%20ice" title="Member">al ice</a>`,
			"user-tag__link", "al ice", "Member"},
		{"empty title is skipped",
			`<a class="user-tag__link" href="/users/alice" title="">alice</a>
			 <a class="user-tag__link" href="/users/alice" title="User">alice</a>`,
			"user-tag__link", "alice", "User"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractFromHTML(tc.page, Spec{GroupTitleClass: tc.cls, Username: tc.username})["group"]
			if got != tc.want {
				t.Errorf("group = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLinksToUser(t *testing.T) {
	cases := []struct {
		href, user string
		want       bool
	}{
		{"https://tracker.example.com/users/alice", "alice", true},
		{"/users/Alice?tab=stats", "alice", true},
		{"/users/alice/torrents", "alice", false},
		{"/users/alicex", "alice", false},
		{"", "alice", false},
	}
	for _, tc := range cases {
		if got := linksToUser(tc.href, tc.user); got != tc.want {
			t.Errorf("linksToUser(%q, %q) = %v, want %v", tc.href, tc.user, got, tc.want)
		}
	}
}
