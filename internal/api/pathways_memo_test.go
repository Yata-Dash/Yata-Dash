package api

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/config"
	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/pathways"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// TestDefLookupsMemoisePerPair: a pair resolves each name once; a fresh pair
// sees a ladder that arrived in between.
func TestDefLookupsMemoisePerPair(t *testing.T) {
	d := pathDeps(t)
	tr := addTraxaryTracker(t, d)

	groupsFor, inviteReqsFor := defLookups(d)
	if got := groupsFor("PeerGarden"); len(got) != 0 {
		t.Fatalf("ladder before any fetch = %v, want none", got)
	}
	if err := d.DB.SaveGroupLadder(tr.ID, []byte(storedLadder), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := groupsFor("PeerGarden"); len(got) != 0 {
		t.Fatalf("same pair re-resolved the ladder (%d ranks); want the memoised answer", len(got))
	}
	groupsFor2, _ := defLookups(d)
	if got := groupsFor2("PeerGarden"); len(got) != 3 {
		t.Fatalf("fresh pair ladder = %+v, want the 3 cached ranks", got)
	}
	if got := groupsFor2("No Such Tracker"); got != nil {
		t.Errorf("unknown name = %+v, want nil", got)
	}
	// The invite lookup shares the name match and still answers.
	if aither, ok := d.Reg.TrackerByURL("https://aither.cc"); ok {
		if got, want := inviteReqsFor(aither.Name), aither.InviteRequirements; got != want {
			t.Errorf("inviteReqsFor(%q) = %v, want %v", aither.Name, got, want)
		}
	}
}

// BenchmarkDefLookupsPerRoute: one request's groupsFor calls for a
// platform-served ladder.
func BenchmarkDefLookupsPerRoute(b *testing.B) {
	dir := b.TempDir()
	cfg, err := config.Open(filepath.Join(dir, "config.json"))
	if err != nil {
		b.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	reg, err := defs.Load("../../defs")
	if err != nil {
		b.Fatal(err)
	}
	paths, err := pathways.Load("../../defs/pathways/routes.json")
	if err != nil {
		b.Fatal(err)
	}
	d := &Deps{Cfg: cfg, DB: db, Reg: reg, Paths: paths}
	if err := cfg.AddTracker(peergardenTracker); err != nil {
		b.Fatal(err)
	}
	if err := db.SaveGroupLadder(peergardenTracker.ID, []byte(storedLadder), time.Now().UTC()); err != nil {
		b.Fatal(err)
	}
	routes := len(paths.Routes)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		groupsFor, _ := defLookups(d)
		for j := 0; j < routes; j++ {
			groupsFor("PeerGarden")
		}
	}
}

// peergardenTracker matches addTraxaryTracker's, for the benchmark.
var peergardenTracker = models.Tracker{
	ID: "pg1", Name: "PeerGarden", URL: "https://peergarden.org",
	Type: "traxary", APIKey: "x", Enabled: true,
}
