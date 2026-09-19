package models

import "testing"

func TestDerivedManualStats(t *testing.T) {
	cases := []struct {
		name  string
		in    map[string]string
		ratio string
		buf   string
	}{
		{"both typed, nothing else", map[string]string{"uploaded": "5.50 TiB", "downloaded": "1.20 TiB"}, "4.58", "4.30 TiB"},
		{"typed ratio wins", map[string]string{"uploaded": "5.50 TiB", "downloaded": "1.20 TiB", "ratio": "9.99"}, "", "4.30 TiB"},
		{"typed buffer wins", map[string]string{"uploaded": "5.50 TiB", "downloaded": "1.20 TiB", "buffer": "1 GiB"}, "4.58", ""},
		{"negative buffer keeps its sign", map[string]string{"uploaded": "800 GiB", "downloaded": "1 TiB"}, "0.78", "-224.00 GiB"},
		{"nothing downloaded", map[string]string{"uploaded": "800 GiB", "downloaded": "0 B"}, "Infinity", "800.00 GiB"},
		{"nothing at all", map[string]string{"uploaded": "0 B", "downloaded": "0 B"}, "", "0.00 GiB"},
		{"only one side typed", map[string]string{"uploaded": "800 GiB"}, "", ""},
		{"unparseable", map[string]string{"uploaded": "lots", "downloaded": "1 TiB"}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DerivedManualStats(c.in)
			if got["ratio"] != c.ratio {
				t.Errorf("ratio = %q, want %q", got["ratio"], c.ratio)
			}
			if got["buffer"] != c.buf {
				t.Errorf("buffer = %q, want %q", got["buffer"], c.buf)
			}
		})
	}
}

// The layer is what the rest of the app reads, so the derivation has to land
// there — and a typed value has to survive it.
func TestManualLayerCarriesDerivedStats(t *testing.T) {
	tr := Tracker{ManualStats: map[string]string{"uploaded": "2 TiB", "downloaded": "1 TiB", "ratio": "1.50"}}
	layer := tr.ManualLayer()
	if layer["ratio"] != "1.50" {
		t.Errorf("typed ratio overridden: %v", layer["ratio"])
	}
	if layer["buffer"] != "1.00 TiB" {
		t.Errorf("buffer = %v, want 1.00 TiB", layer["buffer"])
	}
}
