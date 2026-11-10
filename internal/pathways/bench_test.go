package pathways

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
)

func loadRealRoutes(tb testing.TB) *Data {
	tb.Helper()
	d, err := Load(filepath.Join("..", "..", "defs", "pathways", "routes.json"))
	if err != nil {
		tb.Skipf("routes.json not available: %v", err)
	}
	return d
}

// TestParsedReqsMatchesParseReqs: the load-time cache equals a fresh parse for
// every shipped route.
func TestParsedReqsMatchesParseReqs(t *testing.T) {
	d := loadRealRoutes(t)
	for _, r := range d.Routes {
		if got, want := d.parsedReqs(r.Reqs), ParseReqs(r.Reqs); !reflect.DeepEqual(got, want) {
			t.Fatalf("route %s→%s %q: cached %+v, parsed %+v", r.From, r.To, r.Reqs, got, want)
		}
	}
	// Text the index never saw (or a nil Data) still parses.
	var nilData *Data
	if got := nilData.parsedReqs("Elite, 6 months"); len(got) != 2 {
		t.Fatalf("nil Data fallback: got %+v", got)
	}
	if got := d.parsedReqs("Never Seen Class, 3 months"); len(got) != 2 {
		t.Fatalf("uncached text fallback: got %+v", got)
	}
}

// TestParsedReqsNotAliased: a caller's append must not touch the shared slice.
func TestParsedReqsNotAliased(t *testing.T) {
	d := &Data{Routes: []Route{{From: "A", To: "B", Reqs: "Elite, 6 months", Active: true}}}
	d.index()
	a := d.parsedReqs("Elite, 6 months")
	_ = append(a, Req{Kind: "unknown", Raw: "x"})
	b := d.parsedReqs("Elite, 6 months")
	if len(b) != 2 || cap(b) != 2 {
		t.Fatalf("memoised slice was disturbed: len %d cap %d", len(b), cap(b))
	}
}

// BenchmarkEvalAllRoutes: every shipped route as a first hop, about one page
// load.
func BenchmarkEvalAllRoutes(b *testing.B) {
	d := loadRealRoutes(b)
	u := UserTracker{PathwayName: "x", Stats: Stats{AgeDays: 400, Ratio: 2, UploadedGiB: 500}}
	noGroups := func(string) []defs.GroupDef { return nil }
	noInvite := func(string) *defs.InviteReqs { return nil }
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, r := range d.Routes {
			evalRouteReqs(r, u, true, d, noGroups, noInvite)
		}
	}
}

// BenchmarkParseAllReqs: the one-off parse cost index() pays at load.
func BenchmarkParseAllReqs(b *testing.B) {
	d := loadRealRoutes(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, r := range d.Routes {
			ParseReqs(r.Reqs)
		}
	}
}
