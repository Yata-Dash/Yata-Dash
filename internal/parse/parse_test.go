package parse

import "testing"

// TestSeedTimeToSeconds pins the unit ladder, including the case rules the
// precompiled patterns must keep: Y/W/D match either case, but M (months) and
// m (minutes) are told apart by case, and h and s are lowercase only.
func TestSeedTimeToSeconds(t *testing.T) {
	cases := []struct {
		in   string
		want float64 // 0 means nil
	}{
		{"", 0},
		{"   ", 0},
		{"0", 0},
		{"-5", 0},
		{"3600", 3600},
		{"12.5", 12.5},
		{"3M 6D 22h 23m 13s", 3*30*86400 + 6*86400 + 22*3600 + 23*60 + 13},
		{"1D  4h  30m  20s", 102620},
		{"1y 2w 3d", 365*86400 + 2*7*86400 + 3*86400},
		{"1Y 2W 3D", 365*86400 + 2*7*86400 + 3*86400},
		{"2M", 2 * 30 * 86400},
		{"2m", 2 * 60},
		{"1.5h", 1.5 * 3600},
		{"5H", 0}, // hours are lowercase only
		{"5S", 0}, // seconds are lowercase only
		{"nonsense", 0},
	}
	for _, c := range cases {
		got := SeedTimeToSeconds(c.in)
		switch {
		case c.want == 0 && got != nil:
			t.Errorf("SeedTimeToSeconds(%q) = %v, want nil", c.in, *got)
		case c.want != 0 && got == nil:
			t.Errorf("SeedTimeToSeconds(%q) = nil, want %v", c.in, c.want)
		case c.want != 0 && *got != c.want:
			t.Errorf("SeedTimeToSeconds(%q) = %v, want %v", c.in, *got, c.want)
		}
	}
}

func BenchmarkSeedTimeToSeconds(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		SeedTimeToSeconds("3M 6D 22h 23m 13s")
	}
}
