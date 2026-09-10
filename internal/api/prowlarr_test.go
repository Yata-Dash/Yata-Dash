package api

import (
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// A tracker with more than one domain is still one tracker. Prowlarr's stock
// RetroFlix definition ships retroflix.club; RetroFlix itself asks that API
// traffic use retroflix.net, which is what the def calls the URL and what a
// user configures. Matching on the host alone made every import offer the
// .club entry as new — and pre-tick it, because "already added" is what
// disables the checkbox. Removing and re-importing the tracker did not help,
// because the URL that came back was still the alias.
func TestExistingTrackerMatchedByDefAlias(t *testing.T) {
	d := testDeps(t)
	td, ok := d.Reg.TrackerByURL("https://retroflix.net")
	if !ok {
		t.Skip("retroflix def not present")
	}
	if err := d.Cfg.AddTracker(models.Tracker{
		ID: "t1", Name: "RetroFlix", URL: "https://retroflix.net", Type: td.Type,
	}); err != nil {
		t.Fatal(err)
	}

	existing := d.indexExisting()

	// The alias Prowlarr reports, resolved to the same def.
	alias, ok := d.Reg.TrackerByURL("https://retroflix.club")
	if !ok {
		t.Fatal("retroflix.club is not an alias of the retroflix def — the premise of this test is gone")
	}
	if alias.Key != td.Key {
		t.Fatalf("alias resolved to def %q, want %q", alias.Key, td.Key)
	}
	if !existing.has("https://retroflix.club", alias.Key) {
		t.Error("an alias of a configured tracker was reported as not yet added")
	}

	// The exact URL still matches, by host, with no def involved.
	if !existing.has("https://retroflix.net", "") {
		t.Error("the configured URL itself was reported as not yet added")
	}

	// And an unrelated tracker is still importable — the def check must not
	// turn into "anything with a def is already here".
	if existing.has("https://not-a-tracker.example", "") {
		t.Error("an unconfigured tracker was reported as already added")
	}
}

// A manually-added tracker has no def, so only its host can identify it.
func TestExistingTrackerWithoutDefMatchesByHost(t *testing.T) {
	d := testDeps(t)
	if err := d.Cfg.AddTracker(models.Tracker{
		ID: "t1", Name: "Homegrown", URL: "https://homegrown.example", Type: "unit3d",
	}); err != nil {
		t.Fatal(err)
	}
	existing := d.indexExisting()
	if !existing.has("https://homegrown.example/", "") {
		t.Error("trailing slash defeated the host match")
	}
	if !existing.has("HTTP://WWW.Homegrown.Example", "") {
		t.Error("scheme/www/case defeated the host match")
	}
	if existing.has("https://elsewhere.example", "") {
		t.Error("an unrelated host matched")
	}
}
