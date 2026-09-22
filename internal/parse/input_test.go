package parse

import "testing"

func TestNormalizeSizeInput(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"200g", "200.00 GB", true},
		{"200 GB", "200.00 GB", true},
		{"2.26 gb", "2.26 GB", true},
		{"1.5 tib", "1.50 TiB", true},
		{"800gi", "800.00 GiB", true},
		{"3,000 GB", "3000.00 GB", true},
		{"-12.5 GiB", "-12.50 GiB", true}, // buffer can be negative
		{"512 b", "512.00 B", true},
		{"200", "", false},                       // no unit — ambiguous
		{"This shouldn't be allowed", "", false}, // the screenshot
		{"200 gigs", "", false},
		{"1.5 XB", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeSizeInput(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeSizeInput(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeDurationInput(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"3M 6D", "3M 6D", true},
		{"90d", "3M", true}, // re-expressed in the canonical ladder (30-day months)
		{"3 months 6 days", "3M 6D", true},
		{"2 Years", "2Y", true},
		{"90", "1m 30s", true}, // plain number = seconds, as stored today
		{"3 monkeys", "", false},
		{"soon", "", false},
		{"0", "", false},
		{"-90", "", false},
		{"NaN", "", false},
		{"Inf", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeDurationInput(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeDurationInput(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestIsNumberInput(t *testing.T) {
	for _, s := range []string{"4.58", "25,000", "-3", "∞", "Inf", ".5"} {
		if !IsNumberInput(s) {
			t.Errorf("%q should be a number", s)
		}
	}
	for _, s := range []string{"", "4.5.8", "five", "1 TiB", "1,2,3x"} {
		if IsNumberInput(s) {
			t.Errorf("%q should not be a number", s)
		}
	}
}
