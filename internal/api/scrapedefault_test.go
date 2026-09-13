package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// add posts one tracker through the real create handler and returns the view
// it answers with.
func add(t *testing.T, d *Deps, body string) models.TrackerView {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/trackers", strings.NewReader(body))
	w := httptest.NewRecorder()
	createTracker(d)(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create tracker -> %d: %s", w.Code, w.Body.String())
	}
	var v models.TrackerView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return v
}

// A tracker Yata has never had a conversation about must not start by scraping
// it. That covers both ways one arrives without a def: typed in by hand, and
// imported from Prowlarr or Jackett matching nothing.
func TestNewTrackerStartsAPIOnlyUntilStaffHaveSaidYes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "no def, type chosen by hand",
			body: `{"url":"https://nobody-has-a-def-for-this.example","type":"unit3d"}`,
			want: true,
		},
		{
			name: "no def, no type — a Prowlarr import of an unknown tracker",
			body: `{"url":"https://imported-from-prowlarr.example"}`,
			want: true,
		},
		{
			name: "staff approved and allow scraping",
			body: `{"url":"https://seedpool.org"}`,
			want: false,
		},
		{
			name: "the user said scrape it — an explicit choice always wins",
			body: `{"url":"https://checked-the-rules.example","type":"unit3d","api_only":false}`,
			want: false,
		},
		{
			name: "manual entry never contacts anything, so the flag would be noise",
			body: `{"url":"https://typed-in-by-hand.example","type":"manual"}`,
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := testDeps(t)
			if got := add(t, d, c.body).APIOnly; got != c.want {
				t.Errorf("api_only = %v, want %v", got, c.want)
			}
		})
	}
}

// Having a def is not the same as having permission. "Nobody has asked" and
// "asked, still waiting" are the same position from the tracker's side as
// having no def at all; only an answer — official or informal — changes it.
func TestScrapeDefaultFollowsApprovalStatus(t *testing.T) {
	d := testDeps(t)
	cases := []struct {
		status  string
		apiOnly bool
	}{
		{defs.ApprovalApproved, false},
		{defs.ApprovalInformal, false},
		{defs.ApprovalPending, true},
		{defs.ApprovalUnknown, true},
	}
	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			tr := models.Tracker{URL: "https://has-a-def.example", Type: "unit3d"}
			applyScrapeDefault(d, &tr, c.status, false)
			if tr.APIOnly != c.apiOnly {
				t.Errorf("api_only = %v, want %v", tr.APIOnly, c.apiOnly)
			}
		})
	}
}

// The default is a default. Once a tracker exists, turning scraping on has to
// stick — nothing may reapply the add-time decision on the next write.
func TestScrapeDefaultDoesNotComeBackOnUpdate(t *testing.T) {
	d := testDeps(t)
	v := add(t, d, `{"url":"https://checked-with-staff.example","type":"unit3d"}`)
	if !v.APIOnly {
		t.Fatal("precondition: a new tracker with no def should start API-only")
	}

	off := false
	if err := d.Cfg.UpdateTracker(v.ID, func(tr *models.Tracker) {
		applyPayload(tr, trackerPayload{APIOnly: &off})
	}); err != nil {
		t.Fatal(err)
	}
	if stored, _ := d.Cfg.Tracker(v.ID); stored.APIOnly {
		t.Error("api_only came back after the user turned it off")
	}
}
