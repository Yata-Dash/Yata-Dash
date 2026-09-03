package stats

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// Account-warning fields — the two deadlines a private-tracker account can
// quietly miss.
//
// Both are the same shape: a stored timestamp, a clock, and a number of days.
// Neither announces itself. An expired API key means stats stop arriving and
// the tracker just looks broken; a pruned account means everything the ladder
// was built toward is gone. Both are trivially avoidable before the date and
// irreversible after it.
//
// These are derived on every read rather than stored, because their inputs are
// a timestamp and "now" — storing them would mean a value that is wrong by one
// day for every day since it was written.
const (
	// FieldLastLogin is the "last login" timestamp. Source-agnostic by design:
	// whatever writes it — a tracker's API, or the user's own "I've logged in"
	// — drives everything below without further changes.
	//
	// It follows the ORDINARY merge priority, with no special case: the
	// tracker's API is the source of truth about the tracker's own account,
	// and the user's record only fills in where the API says nothing. That is
	// almost everywhere — of 22 trackers probed on 2026-09-02, one reported a
	// login time — so the manual value is the live one in practice while the
	// two trackers that do report defer to what the tracker itself says.
	FieldLastLogin = "last_login"
	// FieldDaysSinceLogin is how long ago that was, in whole days.
	FieldDaysSinceLogin = "days_since_login"
	// FieldLoginDaysRemaining is how long is LEFT before the tracker acts on
	// the account, from its declared max_login_gap_days. Negative = overdue.
	FieldLoginDaysRemaining = "login_days_remaining"
	// FieldAPIKeyExpiresAt / FieldAPIKeyExpiryDays are the same pair for a key
	// with an expiry date.
	FieldAPIKeyExpiresAt  = "api_key_expires_at"
	FieldAPIKeyExpiryDays = "api_key_expiry_days"
)

// AccountPolicy carries the def-declared account rules for one tracker — the
// part of this calculation the stats engine cannot know on its own, since a
// tracker's inactivity policy comes from its def rather than from its stats.
type AccountPolicy struct {
	// MaxLoginGapDays is the tracker's inactivity deadline; 0 = not known.
	MaxLoginGapDays int
}

// deriveAccountFields adds the days-remaining fields to a merged view.
//
// Every field here is OMITTED when its input is missing, never emitted as a
// helpful-looking zero. That distinction is the whole feature: alert
// conditions treat an absent field as "no answer" and never match it, so
// silence on the twenty trackers that report nothing is automatic — whereas a
// days_since_login of 0 would read as "logged in today" on all of them, which
// is both false and exactly backwards.
func (e *Engine) deriveAccountFields(trackerID string, out models.MergedStats, now time.Time) {
	if src, ok := out[FieldLastLogin]; ok {
		if t, ok := statTime(src.Value); ok {
			since := wholeDays(now.Sub(t))
			out[FieldDaysSinceLogin] = derivedFrom(src, since)
			// The deadline needs the tracker's policy, which only a def can
			// supply. Without one there is nothing to count down to — and
			// saying so by omission is right: Yata not knowing a policy is not
			// evidence that the tracker has none.
			gap := 0
			if e.AccountPolicy != nil {
				gap = e.AccountPolicy(trackerID).MaxLoginGapDays
			}
			if gap > 0 {
				deadline := t.AddDate(0, 0, gap)
				out[FieldLoginDaysRemaining] = derivedFrom(src, wholeDays(deadline.Sub(now)))
			}
		}
	}
	if src, ok := out[FieldAPIKeyExpiresAt]; ok {
		if t, ok := statTime(src.Value); ok {
			out[FieldAPIKeyExpiryDays] = derivedFrom(src, wholeDays(t.Sub(now)))
		}
	}
}

// derivedFrom builds a derived field carrying the provenance of the timestamp
// it was computed from, so the UI's source dot points at where the underlying
// date actually came from rather than inventing a source of its own.
func derivedFrom(src models.StatField, days int) models.StatField {
	return models.StatField{Value: days, Source: src.Source, UpdatedAt: src.UpdatedAt}
}

// wholeDays truncates a duration toward zero in days: 25 hours ago is "1 day",
// 3 hours ago is "0 days" (today), and a deadline 6 hours away is "0 days"
// rather than a reassuring "1".
func wholeDays(d time.Duration) int { return int(d.Hours() / 24) }

// statTimeLayouts are the timestamp formats these fields actually arrive in.
//
// The bare "2006-01-02 15:04:05" form (Anthelion's LastAccess) carries NO
// timezone and is read as UTC, which can be up to 14 hours out. That is
// acceptable here and would not be everywhere: these thresholds are measured
// in days to weeks, so half a day of slop cannot flip a warning that fires a
// week ahead of its deadline. Contrast the freeleech countdown, where a
// mis-resolved "EST" put the end time five hours out on a display counting
// hours and minutes, and the abbreviation had to be resolved properly (see
// namedZoneOffsets in internal/fetch). Same trap, different tolerance.
var statTimeLayouts = []string{
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// statTime reads a stored timestamp. Accepts the RFC3339 most APIs use, a
// space-separated variant, a bare date, and epoch seconds — a stat layer holds
// whatever JSON the tracker sent, so the number cases are not hypothetical.
func statTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case float64:
		return unixOrZero(int64(t))
	case int64:
		return unixOrZero(t)
	case int:
		return unixOrZero(int64(t))
	}
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range statTimeLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			return parsed, true
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return unixOrZero(n)
	}
	return time.Time{}, false
}

func unixOrZero(n int64) (time.Time, bool) {
	if n <= 0 {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}
