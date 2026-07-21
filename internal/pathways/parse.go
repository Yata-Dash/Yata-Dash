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
	// AnyOf holds the alternative requirement SETS of kind "any_of", parsed
	// from "A or B and C": the token is satisfied when every atom of ANY ONE
	// set is met ("9 months or 6 months and 10+ uploads" → [[9mo], [6mo, 10
	// uploads]]).
	AnyOf [][]Req `json:"any_of,omitempty"`
	// Raw is the original token text.
	Raw string `json:"raw"`
}

var (
	durRe   = regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*(years?|yrs?|y|months?|mos?|weeks?|wks?|w|days?|d)$`)
	ratioRe = regexp.MustCompile(`(?i)^ratio\s*>?=?\s*(\d+(?:\.\d+)?)$`)
	sizeRe  = regexp.MustCompile(`(?i)^(?:upload\s+)?(\d+(?:\.\d+)?)\s*(TiB|TB|GiB|GB|MiB|MB)\s*(upload(?:ed)?|seed\s*size|buffer)?$`)
	// countRe: "200 uploads", "10+ uploads" (the "+" is part of the quantity —
	// "at least" — never a conjunction), "75 adopted torrents".
	countRe = regexp.MustCompile(`(?i)^(\d+)\+?\s*(adopted\s+torrents?|adoptions?|uploads?|torrents?)$`)
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
		q := parseToken(tok)
		// A token with no alternatives, only conjuncts ("Elite + 6 months"),
		// is just several requirements written without a comma — flatten it
		// so each gets its own row, exactly as a comma would have produced.
		if q.Kind == "any_of" && len(q.AnyOf) == 1 {
			out = append(out, q.AnyOf[0]...)
			continue
		}
		out = append(out, q)
	}
	return out
}

// Token grammar, one level above the atoms below:
//
//	token       := alternative ("or" alternative)*   — ANY ONE must be met
//	alternative := atom (("and" | " + ") atom)*      — ALL must be met
//	atom        := one of the regex forms in parseAtom
//
// It is deliberately all-or-nothing: if a single atom anywhere in the token
// fails to parse, the whole token falls back to parseAtom and (normally)
// stays "unknown", shown verbatim for the user to check by hand. The
// community data is mostly prose — "2 years with either 250 movie uploads or
// 30 completed subtitle pots", "…write 1 short paragraph…" — and half-reading
// a sentence would be worse than not reading it.
var (
	orRe = regexp.MustCompile(`(?i)\s+or\s+`)
	// The "+" conjunction must be SPACED: an attached one is part of the
	// quantity ("10+ uploads") or a class suffix ("Prometheus+").
	andRe = regexp.MustCompile(`(?i)\s+(?:and|\+)\s+`)
)

func parseToken(tok string) Req {
	// "X or higher" / "X or above" is the "+" modifier written out, NOT an
	// alternative — "higher" is not a class name.
	if m := regexp.MustCompile(`(?i)\s+or\s+(?:higher|above)\s*$`).FindString(tok); m != "" {
		q := parseAtom(strings.TrimSuffix(tok, m) + "+")
		q.Raw = tok
		return q
	}
	if sets, ok := parseAltSets(tok); ok {
		return Req{Kind: "any_of", AnyOf: sets, Raw: tok}
	}
	return parseAtom(tok)
}

// parseAltSets splits a token into its alternative × conjunct sets. ok is
// false — meaning the caller should fall back to the single-atom parse —
// when the token has no "or"/"and" structure, when any atom fails to parse,
// or when every alternative is a lone class name: "Whale or Sailboat" is
// already expressed as one class requirement with several Classes, and that
// shape carries the def-ladder dedupe (see classImplies).
func parseAltSets(tok string) ([][]Req, bool) {
	alts := orRe.Split(tok, -1)
	var sets [][]Req
	allLoneClass := true
	for _, alt := range alts {
		var set []Req
		for _, part := range andRe.Split(alt, -1) {
			part = strings.TrimSpace(part)
			if part == "" {
				return nil, false
			}
			q := parseAtom(part)
			if q.Kind == "unknown" || q.Kind == "none" {
				return nil, false
			}
			// parseAtom's class branch is a catch-all: ANY letter-leading
			// text becomes a "class". That is fine for a whole token shown
			// verbatim, but inside this grammar it would let a clause of
			// prose ("Profile link", "seedsize > 2TB") pass as a real
			// requirement and split a sentence into nonsense rows.
			if q.Kind == "class" && !looksLikeClassName(part) {
				return nil, false
			}
			set = append(set, q)
		}
		if len(set) != 1 || set[0].Kind != "class" {
			allLoneClass = false
		}
		sets = append(sets, set)
	}
	if allLoneClass {
		return nil, false
	}
	// A single alternative means the token was a pure conjunction ("Elite +
	// 6 months"); one atom means no structure at all. Both are handled by
	// ParseReqs / parseAtom rather than as an "any_of".
	if len(sets) == 0 || (len(sets) == 1 && len(sets[0]) < 2) {
		return nil, false
	}
	return sets, true
}

// classNameRe: letters/digits and the punctuation real rank names use, with
// the optional "+" suffix. No comparison operators, brackets or quotes —
// those mark prose or an unparsed threshold, never a class.
var classNameRe = regexp.MustCompile(`^[A-Z][A-Za-z0-9.\-' ]*\+?$`)

// looksLikeClassName gates what the token grammar will accept as a user
// class. Real ranks are short, capitalised and at most a few words
// ("Elite", "Power User", "BluArchivist"); the community data's prose
// clauses ("screenshots", "seedsize > 2TB") are not.
func looksLikeClassName(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 40 || !classNameRe.MatchString(s) {
		return false
	}
	return len(strings.Fields(s)) <= 3
}

func parseAtom(tok string) Req {
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
		kind := "uploads"
		if strings.Contains(strings.ToLower(m[2]), "adopt") {
			kind = "adoptions"
		}
		return Req{Kind: kind, Value: n, Raw: tok}
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
