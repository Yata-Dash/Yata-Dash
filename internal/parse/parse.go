// Package parse provides shared parsing utilities for sizes, seed times, and numbers.
// These are used by the stats fetching layer and the history recorder.
package parse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var sizeRe = regexp.MustCompile(`(?i)([\d.]+)\s*(B|[KMGTP]i?B)`)

var sizeFactors = map[string]float64{
	"b":   1.0 / (1024 * 1024 * 1024),
	"kib": 1.0 / (1024 * 1024),
	"mib": 1.0 / 1024,
	"gib": 1.0,
	"tib": 1024.0,
	"pib": 1024.0 * 1024.0,
	// Decimal-labelled units (TBDev-family sites render "1.005 TB"): tracker
	// software near-universally computes 1024-based sizes but mislabels them,
	// so these map to the same factors as their -iB counterparts.
	"kb": 1.0 / (1024 * 1024),
	"mb": 1.0 / 1024,
	"gb": 1.0,
	"tb": 1024.0,
	"pb": 1024.0 * 1024.0,
}

// SizeToGiB parses a human-readable size string (e.g. "3.14 TiB") into GiB.
// Returns nil when the input is empty or unparseable.
func SizeToGiB(s string) *float64 {
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return nil
	}
	f, ok := sizeFactors[strings.ToLower(m[2])]
	if !ok {
		return nil
	}
	result := v * f
	return &result
}

// SeedTimeToSeconds parses a seed time string (e.g. "3M 6D 22h 23m 13s") to seconds.
// Returns nil for empty/invalid input.
// Also accepts a plain integer/float as raw seconds.
func SeedTimeToSeconds(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Plain numeric — treat as seconds directly
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		if v > 0 {
			return &v
		}
		return nil
	}
	grp := func(pattern string, flags ...string) float64 {
		p := pattern
		if len(flags) > 0 && flags[0] == "i" {
			p = "(?i)" + p
		}
		re := regexp.MustCompile(p)
		m := re.FindStringSubmatch(s)
		if m == nil {
			return 0
		}
		v, _ := strconv.ParseFloat(m[1], 64)
		return v
	}
	total := grp(`([\d.]+)\s*Y`, "i")*365.25*86400 +
		grp(`([\d.]+)\s*M`)*30.44*86400 + // uppercase M only
		grp(`([\d.]+)\s*W`, "i")*7*86400 +
		grp(`([\d.]+)\s*D`, "i")*86400 +
		grp(`([\d.]+)\s*h`)*3600 +
		grp(`([\d.]+)\s*m`)*60 + // lowercase m only
		grp(`([\d.]+)\s*s`)

	if total <= 0 {
		return nil
	}
	return &total
}

// AnyFloat converts any numeric JSON value to float64.
func AnyFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(strings.ReplaceAll(n, ",", ""), 64)
		return f
	}
	return 0
}

// BytesToSize converts a raw byte count (as returned by the Gazelle API) to a
// human-readable size string with exactly 2 decimal places, e.g. "25.52 GiB".
// Torrent-tracker stat values are always sensibly read at GiB scale or above
// (down to "0.00 GiB" for near-zero) — B/KiB/MiB tiers are never used, so
// every tracker's stats display consistently regardless of how small a
// fresh/near-zero value is.
func BytesToSize(b int64) string {
	if b <= 0 {
		return "0.00 GiB"
	}
	const (
		gib = int64(1024) * 1024 * 1024
		tib = gib * 1024
		pib = tib * 1024
	)
	switch {
	case b >= pib:
		return fmt.Sprintf("%.2f PiB", float64(b)/float64(pib))
	case b >= tib:
		return fmt.Sprintf("%.2f TiB", float64(b)/float64(tib))
	default:
		return fmt.Sprintf("%.2f GiB", float64(b)/float64(gib))
	}
}

// NormalizeSeedSize re-formats a size string to exactly 2 decimal places.
// Handles scraped values like "1.239 TiB" → "1.24 TiB".
// Leaves strings that don't match a known unit pattern unchanged.
func NormalizeSeedSize(s string) string {
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return s
	}
	return fmt.Sprintf("%.2f %s", v, m[2])
}

// FormatSeedTime formats seconds into a human string like "1Y 2M 3D 4h 5m 6s".
func FormatSeedTime(totalSec float64) string {
	t := int64(totalSec)
	if t <= 0 {
		return "0s"
	}
	steps := []struct {
		sec int64
		u   string
	}{
		{31536000, "Y"}, {2592000, "M"}, {604800, "W"},
		{86400, "D"}, {3600, "h"}, {60, "m"}, {1, "s"},
	}
	var parts []string
	for _, s := range steps {
		v := t / s.sec
		t -= v * s.sec
		if v > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", v, s.u))
		}
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, " ")
}
