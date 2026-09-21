package parse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Typed input — sizes, durations and plain numbers a user enters by hand
// (manual stats, targets, alert thresholds). Scraped values arrive in the
// shape a tracker chose and are kept that way; typed values have no source
// to be faithful to, so they are read leniently and stored in one canonical
// form, or refused with a message that shows the expected shape.

// sizeInputRe reads a typed size: an optional sign, a number (commas
// allowed), and a unit that may be as terse as a single letter — "200g",
// "1.5 tib", "3,000 GB". The unit is REQUIRED: a bare "200" is as likely to
// mean 200 GiB as 200 TiB, and guessing wrong is worse than asking.
var sizeInputRe = regexp.MustCompile(`^(-?)(\d[\d,]*\.?\d*|\.\d+)\s*(?:([KkMmGgTtPp])([Ii])?[Bb]?|([Bb]))$`)

// NormalizeSizeInput turns a typed size into the canonical "%.2f UNIT" form:
// "200g" → "200.00 GB", "1.5 tib" → "1.50 TiB". A bare letter keeps the
// decimal label the way the user typed it; "i" asks for the binary one. Yata
// reads both with the 1024 factor (see sizeFactors), so the label is a
// choice of spelling, not of magnitude. ok is false for anything that is not
// a size — a number with no unit, a word, an unsupported unit.
func NormalizeSizeInput(s string) (string, bool) {
	m := sizeInputRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", ""), 64)
	if err != nil {
		return "", false
	}
	unit := "B"
	if m[3] != "" {
		unit = strings.ToUpper(m[3]) + "B"
		if m[4] != "" {
			unit = strings.ToUpper(m[3]) + "iB"
		}
	}
	return fmt.Sprintf("%s%.2f %s", m[1], v, unit), true
}

// durationWords maps the long forms of the duration units to the single
// letters SeedTimeToSeconds reads. Case matters for the letters (M months,
// m minutes) but not for the words, which are unambiguous.
var durationWords = []struct {
	re     *regexp.Regexp
	letter string
}{
	{regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:years?|yrs?)\b`), "Y"},
	{regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:months?|mos?)\b`), "M"},
	{regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:weeks?|wks?)\b`), "W"},
	{regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:days?)\b`), "D"},
	{regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:hours?|hrs?)\b`), "h"},
	{regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:minutes?|mins?)\b`), "m"},
	{regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:seconds?|secs?)\b`), "s"},
}

// durationTokenRe is what a duration must reduce to once the words are
// letters: number-letter pairs and nothing else. SeedTimeToSeconds alone
// would happily read "3 monkeys" as 3 months.
var durationTokenRe = regexp.MustCompile(`^(?:\d+(?:\.\d+)?\s*[YyMWwDdhms]\s*)+$`)

// NormalizeDurationInput turns a typed duration into the canonical
// "1Y 2M 3D 4h" form. Accepts the letter form ("3M 6D"), the words ("3
// months 6 days") and a plain number of seconds, which is what the API
// layer already stores. ok is false for anything else.
func NormalizeDurationInput(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		if v <= 0 {
			return "", false
		}
		return FormatSeedTime(v), true
	}
	for _, w := range durationWords {
		s = w.re.ReplaceAllString(s, "${1}"+w.letter)
	}
	if !durationTokenRe.MatchString(s) {
		return "", false
	}
	secs := SeedTimeToSeconds(s)
	if secs == nil {
		return "", false
	}
	return FormatSeedTime(*secs), true
}

// numberInputRe is a plain count or ratio: digits, optional thousands
// commas, optional decimals, optional sign.
var numberInputRe = regexp.MustCompile(`^-?(\d[\d,]*\.?\d*|\.\d+)$`)

// IsNumberInput reports whether a typed value is a plain number. Ratios can
// also be infinite — a tracker shows "∞" or "Inf" for an account that has
// never downloaded — so those spellings pass as well.
func IsNumberInput(s string) bool {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "∞", "inf", "infinity":
		return true
	}
	return numberInputRe.MatchString(s)
}
