package pathways

import (
	"regexp"
	"strconv"
	"strings"
)

// Req is one parsed requirement token from a route's free-text reqs.
type Req struct {
	// Kind: class | age | ratio | uploaded | seed_size | uploads | bonus |
	// seedtime | none | unknown
	Kind string `json:"kind"`
	// Classes holds the alternative class names for kind "class"
	// ("Leviathan or Ship" → ["Leviathan", "Ship"]). Trailing "+" stripped.
	Classes []string `json:"classes,omitempty"`
	// Plus is true when the class carried a "+" suffix ("Prometheus+"):
	// that class OR HIGHER — and official invites may demand extras on top.
	Plus bool `json:"plus,omitempty"`
	// Days for kind "age".
	Days int `json:"days,omitempty"`
	// Value: GiB for uploaded/seed_size, plain number for ratio/uploads/
	// bonus, seconds for seedtime.
	Value float64 `json:"value,omitempty"`
	// Raw is the original token text.
	Raw string `json:"raw"`
}

var (
	durRe   = regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*(years?|yrs?|y|months?|mos?|weeks?|wks?|w|days?|d)$`)
	ratioRe = regexp.MustCompile(`(?i)^ratio\s*>?=?\s*(\d+(?:\.\d+)?)$`)
	sizeRe  = regexp.MustCompile(`(?i)^(?:upload\s+)?(\d+(?:\.\d+)?)\s*(TiB|TB|GiB|GB|MiB|MB)\s*(upload(?:ed)?|seed\s*size|buffer)?$`)
	countRe = regexp.MustCompile(`(?i)^(\d+)\s*(uploads?|torrents?|adoptions?)$`)
	bpRe    = regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*([km])?\s*BP$`)
	stRe    = regexp.MustCompile(`(?i)^avg\.?\s*seed\s*time\s*>?=?\s*(.+)$`)
)

// unitDays maps duration units to days.
func unitDays(u string) float64 {
	switch strings.ToLower(string(u[0])) {
	case "y":
		return 365
	case "m":
		return 30.44
	case "w":
		return 7
	default:
		return 1
	}
}

// sizeGiB converts a number+unit to GiB (decimal units treated as binary —
// the community data mixes them; the difference is noise at this precision).
func sizeGiB(n float64, unit string) float64 {
	switch strings.ToLower(unit)[0:1] {
	case "t":
		return n * 1024
	case "g":
		return n
	default:
		return n / 1024
	}
}

// durWordRe matches word-form duration units (case-insensitive), used by
// the community trackerpathways data ("8 months", "1 year 2 weeks 1 day").
var durWordRe = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(years?|yrs?|months?|mos?|weeks?|wks?|days?)`)

// durLetterRe matches the single-letter Unit3D convention used by the tracker
// defs ("8M", "1M 2W 1D", "1Y 3M"). CRITICAL: uppercase "M" is months — the
// letter set is case-sensitive for M so a lowercase "m" (minutes) is never
// misread as a month. Y/W/D accept either case.
var durLetterRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*([YyMWwDd])`)

// ParseDurationDays parses durations in either the community word form
// ("1 month 2 weeks 1 day") or the Unit3D single-letter form ("1M 2W 1D",
// "8M", "1Y 3M"), or a plain number of days. Returns ok=false when nothing
// parses, so callers can skip a requirement rather than treating it as zero.
func ParseDurationDays(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v, v > 0 // a bare number is a day count
	}

	var total float64
	found := false
	// Word units first; remove each match so the single-letter pass can't
	// double-count the leading letter of a word (e.g. the "m" in "months").
	rest := s
	for _, m := range durWordRe.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.ParseFloat(m[1], 64)
		total += n * unitDays(m[2])
		found = true
		rest = strings.Replace(rest, m[0], " ", 1)
	}
	for _, m := range durLetterRe.FindAllStringSubmatch(rest, -1) {
		n, _ := strconv.ParseFloat(m[1], 64)
		total += n * unitDays(m[2]) // unitDays lowercases; "M" → month
		found = true
	}
	return total, found && total > 0
}

// ParseReqs splits a route's free-text requirement string into tokens.
// Unrecognised tokens come back as kind "unknown" with the raw text — the
// UI shows them verbatim so no community information is ever lost.
func ParseReqs(text string) []Req {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	if strings.EqualFold(t, "no requirement") || strings.EqualFold(t, "no requirements") || strings.EqualFold(t, "none") {
		return []Req{{Kind: "none", Raw: t}}
	}
	if strings.EqualFold(t, "unknown") {
		return []Req{{Kind: "unknown", Raw: t}}
	}

	var out []Req
	for _, tok := range strings.Split(t, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		out = append(out, parseToken(tok))
	}
	return out
}

func parseToken(tok string) Req {
	// Duration → account age.
	if m := durRe.FindStringSubmatch(tok); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		return Req{Kind: "age", Days: int(n*unitDays(m[2]) + 0.5), Raw: tok}
	}
	if ratioRe.MatchString(tok) {
		m := ratioRe.FindStringSubmatch(tok)
		v, _ := strconv.ParseFloat(m[1], 64)
		return Req{Kind: "ratio", Value: v, Raw: tok}
	}
	if m := sizeRe.FindStringSubmatch(tok); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		kind := "uploaded"
		if strings.Contains(strings.ToLower(m[3]), "seed") {
			kind = "seed_size"
		}
		return Req{Kind: kind, Value: sizeGiB(n, m[2]), Raw: tok}
	}
	if m := countRe.FindStringSubmatch(tok); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		return Req{Kind: "uploads", Value: n, Raw: tok}
	}
	if m := bpRe.FindStringSubmatch(tok); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		switch strings.ToLower(m[2]) {
		case "k":
			n *= 1_000
		case "m":
			n *= 1_000_000
		}
		return Req{Kind: "bonus", Value: n, Raw: tok}
	}
	if m := stRe.FindStringSubmatch(tok); m != nil {
		if days, ok := ParseDurationDays(m[1]); ok {
			return Req{Kind: "seedtime", Value: days * 86400, Raw: tok}
		}
	}
	// Class token: "Prometheus+", "Pro+", "Leviathan or Ship", "Superfan+".
	// Heuristic: starts with a letter and is short — treat as class name(s).
	if regexp.MustCompile(`^[A-Za-z]`).MatchString(tok) && len(tok) <= 60 {
		plus := false
		var classes []string
		for _, alt := range regexp.MustCompile(`(?i)\s+or\s+`).Split(tok, -1) {
			alt = strings.TrimSpace(alt)
			if strings.HasSuffix(alt, "+") {
				plus = true
				alt = strings.TrimSuffix(alt, "+")
			}
			if alt != "" {
				classes = append(classes, alt)
			}
		}
		if len(classes) > 0 {
			return Req{Kind: "class", Classes: classes, Plus: plus, Raw: tok}
		}
	}
	return Req{Kind: "unknown", Raw: tok}
}
