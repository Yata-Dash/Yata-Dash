package stats

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// TestMergedQUISeedsizeModes: where the qui layer's seed_size lands for each
// QUISeedsizeMode — and that qui NEVER beats the tracker's own API.
func TestMergedQUISeedsizeModes(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := New(db)
	mode := "off"
	e.QUISeedMode = func() string { return mode }

	// Three layers disagree about seed_size; API also has a field qui lacks.
	_ = e.SaveAPI("t1", map[string]any{"uploaded": "5.00 TiB"})
	_ = e.SaveScrape("t1", map[string]any{"seed_size": "2.00 TiB"})
	_ = e.SaveQUI("t1", map[string]any{"seed_size": "9.00 TiB"})

	seedFrom := func(wantVal string, wantSrc models.Source) {
		t.Helper()
		m, err := e.Merged("t1")
		if err != nil {
			t.Fatal(err)
		}
		f := m["seed_size"]
		if f.Value != wantVal || f.Source != wantSrc {
			t.Errorf("mode %s: seed_size = %v from %s, want %v from %s",
				mode, f.Value, f.Source, wantVal, wantSrc)
		}
	}

	seedFrom("2.00 TiB", models.SourceScrape) // off: qui layer invisible
	mode = "missing"
	seedFrom("2.00 TiB", models.SourceScrape) // scrape has it → qui only fills gaps
	mode = "prefer"
	seedFrom("9.00 TiB", models.SourceQUI) // beats the scrape

	// Scrape loses the stat (returns zero) → "missing" now fills from qui.
	_ = e.SaveScrape("t1", map[string]any{"seed_size": "0 B"})
	mode = "missing"
	seedFrom("9.00 TiB", models.SourceQUI)

	// The tracker's API starts reporting seed_size → it wins in EVERY mode.
	_ = e.SaveAPI("t1", map[string]any{"uploaded": "5.00 TiB", "seed_size": "3.00 TiB"})
	for _, m := range []string{"off", "missing", "prefer"} {
		mode = m
		seedFrom("3.00 TiB", models.SourceAPI)
	}

	// Clearing the qui layer removes the stat when nothing else has it.
	_ = e.SaveAPI("t1", map[string]any{"uploaded": "5.00 TiB"})
	_ = e.SaveQUI("t1", map[string]any{})
	mode = "prefer"
	m, _ := e.Merged("t1")
	if f, ok := m["seed_size"]; ok && meaningful(f.Value) {
		t.Errorf("cleared qui layer + zero scrape should leave no seed_size, got %+v", f)
	}
}

func TestGrowthRatesFromDailyRollups(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := New(db)

	// 8 daily rollups, uploaded growing 10 GiB/day (1000 → 1070).
	now := time.Now().UTC()
	for i := 7; i >= 0; i-- {
		at := now.AddDate(0, 0, -i)
		_ = db.RecordDaily("t1", at, map[string]float64{
			"uploaded":     1000 + float64(7-i)*10,
			"bonus_points": 50000 + float64(7-i)*500,
			"seed_size":    2000, // flat → no rate
		})
	}
	r := e.GrowthRates("t1")
	if r["uploaded"] < 9.5 || r["uploaded"] > 10.5 {
		t.Errorf("uploaded rate = %v, want ~10 GiB/day", r["uploaded"])
	}
	if r["bonus_points"] < 490 || r["bonus_points"] > 510 {
		t.Errorf("bonus rate = %v, want ~500/day", r["bonus_points"])
	}
	if _, ok := r["seed_size"]; ok {
		t.Errorf("flat seed_size should be omitted, got %v", r["seed_size"])
	}
}

func TestGrowthRatesFallsBackToFineHistory(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "r2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := New(db)

	// Only fine history (no daily rollups yet — a fresh tracker), 6h span.
	now := time.Now().UTC()
	_ = db.AddHistory("t1", now.Add(-6*time.Hour), map[string]float64{"uploaded": 100})
	_ = db.AddHistory("t1", now, map[string]float64{"uploaded": 105}) // +5 GiB in 6h = 20/day

	r := e.GrowthRates("t1")
	if r["uploaded"] < 18 || r["uploaded"] > 22 {
		t.Errorf("fine-history fallback rate = %v, want ~20 GiB/day", r["uploaded"])
	}
}

func TestDeclineSignalsRatioDeclining(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "d1.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := New(db)

	// 8 daily rollups, ratio declining from 1.40 to 1.05 (~0.05/day).
	now := time.Now().UTC()
	for i := 7; i >= 0; i-- {
		at := now.AddDate(0, 0, -i)
		_ = db.RecordDaily("t1", at, map[string]float64{"ratio": 1.40 - float64(7-i)*0.05})
	}
	sig := e.DeclineSignals("t1")
	if sig.RatioPerDay == nil {
		t.Fatal("expected a ratio rate")
	}
	if *sig.RatioPerDay > -0.03 || *sig.RatioPerDay < -0.07 {
		t.Errorf("ratio rate = %v, want ~-0.05/day", *sig.RatioPerDay)
	}
}

// TestDeclineSignalsRatioFlatIsNil: a ratio that isn't moving (under the
// noise floor) yields no signal — same "flat stat can't be projected" rule
// GrowthRates applies to its one-directional fields.
func TestDeclineSignalsRatioFlatIsNil(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "d2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := New(db)

	now := time.Now().UTC()
	for i := 7; i >= 0; i-- {
		_ = db.RecordDaily("t1", now.AddDate(0, 0, -i), map[string]float64{"ratio": 1.20})
	}
	if sig := e.DeclineSignals("t1"); sig.RatioPerDay != nil {
		t.Errorf("flat ratio should yield no signal, got %v", *sig.RatioPerDay)
	}
}

// TestDeclineSignalsDropPct covers the seed_size/seeding drop-% math: a real
// decline over the week reports a positive percentage; a flat-to-zero
// baseline and a too-short span both stay nil; and growth (the stat going
// UP) is nil too — a drop is never negative.
func TestDeclineSignalsDropPct(t *testing.T) {
	now := time.Now().UTC()

	t.Run("decline reports a positive drop", func(t *testing.T) {
		db, err := store.Open(filepath.Join(t.TempDir(), "drop1.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		e := New(db)
		// seed_size 100 → 75 over the week (25% drop).
		for i := 7; i >= 0; i-- {
			v := 100.0 - float64(7-i)*(25.0/7)
			_ = db.RecordDaily("t1", now.AddDate(0, 0, -i), map[string]float64{"seed_size": v})
		}
		sig := e.DeclineSignals("t1")
		if sig.SeedSizeDrop7dPct == nil {
			t.Fatal("expected a seed_size drop signal")
		}
		if *sig.SeedSizeDrop7dPct < 20 || *sig.SeedSizeDrop7dPct > 30 {
			t.Errorf("seed_size drop = %v, want ~25%%", *sig.SeedSizeDrop7dPct)
		}
	})

	t.Run("zero baseline is nil", func(t *testing.T) {
		db, err := store.Open(filepath.Join(t.TempDir(), "drop2.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		e := New(db)
		for i := 7; i >= 0; i-- {
			v := 0.0
			if i < 4 {
				v = 10.0 // some seeding shows up partway through, from a zero baseline
			}
			_ = db.RecordDaily("t1", now.AddDate(0, 0, -i), map[string]float64{"seeding": v})
		}
		if sig := e.DeclineSignals("t1"); sig.SeedingDrop7dPct != nil {
			t.Errorf("zero baseline should yield no drop signal, got %v", *sig.SeedingDrop7dPct)
		}
	})

	t.Run("span under 24h is nil", func(t *testing.T) {
		db, err := store.Open(filepath.Join(t.TempDir(), "drop3.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		e := New(db)
		// Only fine history, 6h apart — well under the drop%'s 24h floor even
		// though it clears GrowthRates' 3h fine-history floor.
		_ = db.AddHistory("t1", now.Add(-6*time.Hour), map[string]float64{"seed_size": 100})
		_ = db.AddHistory("t1", now, map[string]float64{"seed_size": 50})
		if sig := e.DeclineSignals("t1"); sig.SeedSizeDrop7dPct != nil {
			t.Errorf("sub-24h span should yield no drop signal, got %v", *sig.SeedSizeDrop7dPct)
		}
	})

	t.Run("growth is nil, never a negative percentage", func(t *testing.T) {
		db, err := store.Open(filepath.Join(t.TempDir(), "drop4.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		e := New(db)
		for i := 7; i >= 0; i-- {
			v := 100.0 + float64(7-i)*5 // growing, not dropping
			_ = db.RecordDaily("t1", now.AddDate(0, 0, -i), map[string]float64{"seed_size": v})
		}
		if sig := e.DeclineSignals("t1"); sig.SeedSizeDrop7dPct != nil {
			t.Errorf("growth should yield no drop signal, got %v", *sig.SeedSizeDrop7dPct)
		}
	})
}

func TestRateFromPointsGuards(t *testing.T) {
	// Too-short span → no rate.
	pts := []store.HistoryPoint{
		{RecordedAt: 0, Value: 100},
		{RecordedAt: 3600, Value: 200}, // 1h span
	}
	if _, ok := rateFromPoints(pts, 3*3600); ok {
		t.Error("span under threshold should yield no rate")
	}
	// Declining stat → no rate.
	decl := []store.HistoryPoint{
		{RecordedAt: 0, Value: 200},
		{RecordedAt: 86400, Value: 100},
	}
	if _, ok := rateFromPoints(decl, 3600); ok {
		t.Error("declining stat should yield no rate")
	}
}

// TestNumericSnapshotSkipsNonFinite: a downloaded=0 tracker reports ratio as
// the string "Infinity" (strconv.ParseFloat("Infinity") = +Inf). That field
// must be dropped — an unrecorded ratio, not a poisoned history row — while
// every other numeric field still records normally.
func TestNumericSnapshotSkipsNonFinite(t *testing.T) {
	merged := models.MergedStats{
		"ratio":    {Value: "Infinity", Source: models.SourceAPI},
		"uploaded": {Value: "10.00 GiB", Source: models.SourceAPI},
	}
	fields := NumericSnapshot(merged)
	if _, ok := fields["ratio"]; ok {
		t.Errorf("infinite ratio should be omitted, got %v", fields["ratio"])
	}
	if fields["uploaded"] != 10 {
		t.Errorf("uploaded = %v, want 10", fields["uploaded"])
	}
}
