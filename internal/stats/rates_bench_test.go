package stats

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// benchFields mirrors what a real refresh records: the projectable stats plus
// the rest of the numeric fields that share the history index, so the
// benchmark's index has the shape a live database does.
var benchFields = []string{
	"uploaded", "downloaded", "buffer", "ratio", "seed_size", "seeding",
	"leeching", "bonus_points", "seeding_points", "uploads_approved",
	"snatched", "hnr", "freeleech_tokens",
}

// seedRateBench fills a temp database with a fortnight of 15-minute fine
// history (the full retention window) plus daily rollups for one tracker —
// the worst case GrowthRates/DeclineSignals see on every call.
func seedRateBench(b *testing.B) *Engine {
	b.Helper()
	db, err := store.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	now := time.Now().UTC()
	start := now.Add(-14 * 24 * time.Hour)
	for i, at := 0, start; !at.After(now); i, at = i+1, at.Add(15*time.Minute) {
		fields := make(map[string]float64, len(benchFields))
		for j, f := range benchFields {
			fields[f] = float64(1000*(j+1) + i)
		}
		if err := db.AddHistory("t1", at, fields); err != nil {
			b.Fatal(err)
		}
		if err := db.RecordDaily("t1", at, fields); err != nil {
			b.Fatal(err)
		}
	}
	return New(db)
}

func BenchmarkGrowthRates(b *testing.B) {
	e := seedRateBench(b)
	b.ReportAllocs()
	for b.Loop() {
		if r := e.GrowthRates("t1"); len(r) == 0 {
			b.Fatal("no rates")
		}
	}
}

func BenchmarkDeclineSignals(b *testing.B) {
	e := seedRateBench(b)
	b.ReportAllocs()
	for b.Loop() {
		_ = e.DeclineSignals("t1")
	}
}
