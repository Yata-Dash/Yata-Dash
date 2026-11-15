package stats

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// benchFields mirrors a real refresh, so the history index has a live shape.
var benchFields = []string{
	"uploaded", "downloaded", "buffer", "ratio", "seed_size", "seeding",
	"leeching", "bonus_points", "seeding_points", "uploads_approved",
	"snatched", "hnr", "freeleech_tokens",
}

// seedRateBench fills a full fortnight of 15-minute history plus daily
// rollups: the worst case for GrowthRates/DeclineSignals.
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
