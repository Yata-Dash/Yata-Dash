package api

import (
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// A ladder as PeerGarden's /api/user/groups serves it, with the per-user
// progress already stripped the way FetchGroups stores it.
const storedLadder = `{"auto":[
  {"id":8,"title":"Seed","requirements":[]},
  {"id":9,"title":"Seedling","requirements":[{"type":"upload","value":53687091200}]},
  {"id":16,"title":"Seed Vault","requirements":[
    {"type":"seedsize","value":10995116277760},{"type":"age","value":7776000}]}
]}`

// addTraxaryTracker registers PeerGarden — a real def on a type that serves its
// own ladder, and therefore one that ships no groups of its own.
func addTraxaryTracker(t *testing.T, d *Deps) models.Tracker {
	t.Helper()
	tr := models.Tracker{
		ID: "pg1", Name: "PeerGarden", URL: "https://peergarden.org",
		Type: "traxary", APIKey: "x", Enabled: true,
	}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	return tr
}

func peergardenDef(t *testing.T, d *Deps) defs.TrackerDef {
	t.Helper()
	td, ok := d.Reg.TrackerByURL("https://peergarden.org")
	if !ok {
		t.Fatal("PeerGarden def missing")
	}
	return td
}

// The def must ship no ladder: a build-time copy is precisely what the
// endpoint replaces, and one left behind as a safety net recreates the drift.
func TestTraxaryDefShipsNoLadder(t *testing.T) {
	d := testDeps(t)
	if td := peergardenDef(t, d); len(td.Groups) != 0 {
		t.Errorf("def carries %d groups; a traxary def must ship none", len(td.Groups))
	}
	if spec := d.Reg.GroupAPI("https://peergarden.org", "traxary"); spec == nil || spec.Ladder != "auto" {
		t.Fatalf("group API spec = %+v, want the auto ladder", spec)
	}
}

func TestGroupsForUsesCachedLadder(t *testing.T) {
	d := testDeps(t)
	tr := addTraxaryTracker(t, d)
	td := peergardenDef(t, d)

	// Nothing cached yet: no ladder, and specifically not a confident empty
	// one — the capability chip hides itself rather than reporting zero.
	if got := groupsFor(d, td, tr.ID); len(got) != 0 {
		t.Fatalf("ladder before any fetch = %v, want none", got)
	}

	if err := d.DB.SaveGroupLadder(tr.ID, []byte(storedLadder), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got := groupsFor(d, td, tr.ID)
	if len(got) != 3 || got[0].Name != "Seed" || got[2].Name != "Seed Vault" {
		t.Fatalf("ladder = %+v, want the three cached ranks in order", got)
	}
	if v := got[1].Requirements.MinUploaded; v != "50.00 GiB" {
		t.Errorf("Seedling min_uploaded = %q, want 50.00 GiB", v)
	}
}

// /api/tracker-groups and the defs browser hold a def but no tracker. A ladder
// belongs to the SITE, so resolving it through any account that has it is
// correct rather than approximate.
func TestGroupsForDefResolvesThroughAConfiguredTracker(t *testing.T) {
	d := testDeps(t)
	td := peergardenDef(t, d)
	if got := groupsForDef(d, td); len(got) != 0 {
		t.Fatalf("ladder with no configured tracker = %v, want none", got)
	}
	tr := addTraxaryTracker(t, d)
	if err := d.DB.SaveGroupLadder(tr.ID, []byte(storedLadder), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := groupsForDef(d, td); len(got) != 3 {
		t.Fatalf("ladder = %+v, want 3 ranks resolved via the configured tracker", got)
	}
}

// A def that ships its own ladder must be untouched by any of this.
func TestGroupsForFallsBackToDefLadder(t *testing.T) {
	d := testDeps(t)
	td, ok := d.Reg.TrackerByURL("https://aither.cc")
	if !ok || len(td.Groups) == 0 {
		t.Skip("no shipped ladder to compare against")
	}
	got := groupsFor(d, td, "no-such-tracker")
	if len(got) != len(td.Groups) {
		t.Errorf("ladder = %d ranks, want the def's %d", len(got), len(td.Groups))
	}
}

// Capability coverage is derived from the ladder in force, so a platform-served
// ladder has to restore the "N of M requirements" figure that a def carrying no
// groups would otherwise reduce to zero.
func TestCapabilityCoverageFollowsTheLiveLadder(t *testing.T) {
	d := testDeps(t)
	tr := addTraxaryTracker(t, d)
	td := peergardenDef(t, d)

	if v := buildCapabilityView(d, td, groupsFor(d, td, tr.ID)); v.LadderTotal != 0 {
		t.Fatalf("ladder_total before any fetch = %d, want 0 (chip hides itself)", v.LadderTotal)
	}
	if err := d.DB.SaveGroupLadder(tr.ID, []byte(storedLadder), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	v := buildCapabilityView(d, td, groupsFor(d, td, tr.ID))
	// upload / seed size / age → uploaded, seed_size, join_date.
	if v.LadderTotal != 3 {
		t.Errorf("ladder_total = %d, want 3", v.LadderTotal)
	}
	if v.MetAPI != 3 {
		t.Errorf("met_api = %d, want 3 — traxary reports all three", v.MetAPI)
	}
}

// The refresh is a no-op for every type that doesn't serve a ladder, and must
// never touch the store for one.
func TestMaybeRefreshGroupLadderIgnoresOtherTypes(t *testing.T) {
	d := testDeps(t)
	tr := models.Tracker{ID: "a1", Name: "Aither", URL: "https://aither.cc", Type: "unit3d", APIKey: "x"}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	maybeRefreshGroupLadder(d, tr, "Seeder")
	if _, ok := d.DB.LatestGroupLadder(tr.ID); ok {
		t.Error("stored a ladder for a type that serves none")
	}
}

// A fetch failure leaves the cached ladder standing: the fallback is what the
// tracker last said, and a bad key must not blank the ranks.
func TestMaybeRefreshGroupLadderKeepsCacheOnFailure(t *testing.T) {
	d := testDeps(t)
	tr := addTraxaryTracker(t, d)
	stale := time.Now().UTC().Add(-72 * time.Hour)
	if err := d.DB.SaveGroupLadder(tr.ID, []byte(storedLadder), stale); err != nil {
		t.Fatal(err)
	}
	// No API key, so the fetch fails before any request is built — the failure
	// path without touching the network. Nothing about the stored revision may
	// change.
	if err := d.Cfg.UpdateTracker(tr.ID, func(x *models.Tracker) { x.APIKey = "" }); err != nil {
		t.Fatal(err)
	}
	tr.APIKey = ""
	maybeRefreshGroupLadder(d, tr, "Seed")
	got, ok := d.DB.LatestGroupLadder(tr.ID)
	if !ok {
		t.Fatal("cached ladder lost after a failed refresh")
	}
	if string(got.Payload) != storedLadder {
		t.Errorf("payload changed after a failed refresh")
	}
	if len(groupsFor(d, peergardenDef(t, d), tr.ID)) != 3 {
		t.Error("ladder no longer resolves after a failed refresh")
	}
}

// The ladder endpoint serves no colours yet, but /api/user reports the style of
// the rank the user holds — which is what the badge, the styled username and
// the perk icons all read.
func TestHeldGroupStyleColoursTheCurrentRank(t *testing.T) {
	d := testDeps(t)
	tr := addTraxaryTracker(t, d)
	td := peergardenDef(t, d)
	if err := d.DB.SaveGroupLadder(tr.ID, []byte(storedLadder), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := d.Stats.SaveAPI(tr.ID, map[string]any{
		"group":       "Seed",
		"group_color": "#a8e6a3",
		"group_icon":  "fa-solid fa-seedling",
	}); err != nil {
		t.Fatal(err)
	}
	got := groupsFor(d, td, tr.ID)
	if got[0].Name != "Seed" {
		t.Fatalf("ladder[0] = %q, want Seed", got[0].Name)
	}
	if got[0].Style.Color != "#a8e6a3" || got[0].Style.Icon != "fa-solid fa-seedling" {
		t.Errorf("held rank style = %+v, want the colour and icon from /api/user", got[0].Style)
	}
	// Only the rank actually held: a field describing your own rank says
	// nothing about any other.
	for _, g := range got[1:] {
		if g.Style.Color != "" || g.Style.Icon != "" {
			t.Errorf("%s picked up a style it cannot know: %+v", g.Name, g.Style)
		}
	}
}

// A style the ladder itself supplies wins, so the overlay stops mattering the
// day the platform serves colours rather than fighting them.
func TestLadderStyleBeatsHeldGroupStyle(t *testing.T) {
	d := testDeps(t)
	tr := addTraxaryTracker(t, d)
	td := peergardenDef(t, d)
	styled := `{"auto":[{"id":8,"title":"Seed","color":"#111111","icon":"fas fa-leaf","requirements":[]}]}`
	if err := d.DB.SaveGroupLadder(tr.ID, []byte(styled), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := d.Stats.SaveAPI(tr.ID, map[string]any{
		"group": "Seed", "group_color": "#a8e6a3", "group_icon": "fa-solid fa-seedling",
	}); err != nil {
		t.Fatal(err)
	}
	got := groupsFor(d, td, tr.ID)
	if got[0].Style.Color != "#111111" || got[0].Style.Icon != "fas fa-leaf" {
		t.Errorf("style = %+v, want the ladder's own", got[0].Style)
	}
}

// No style reported (every other traxary tracker, and every rank before the
// platform serves colours) must leave the ladder exactly as it was.
func TestHeldGroupStyleAbsentLeavesLadderAlone(t *testing.T) {
	d := testDeps(t)
	tr := addTraxaryTracker(t, d)
	td := peergardenDef(t, d)
	if err := d.DB.SaveGroupLadder(tr.ID, []byte(storedLadder), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := d.Stats.SaveAPI(tr.ID, map[string]any{"group": "Seed"}); err != nil {
		t.Fatal(err)
	}
	for _, g := range groupsFor(d, td, tr.ID) {
		if g.Style.Color != "" || g.Style.Icon != "" {
			t.Errorf("%s gained a style from nowhere: %+v", g.Name, g.Style)
		}
	}
}
