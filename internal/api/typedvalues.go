package api

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Yata-Dash/Yata-Dash/internal/parse"
)

// Typed values — manual stats and targets — checked and put into canonical
// form before they are stored. The form does the same checks in front of
// the user (web/src/utils/units.ts); this is the guard behind it, so a
// hand-made request cannot store "This shouldn't be allowed" as a stat, and
// so a typed "200g" reads back as "200.00 GB" everywhere downstream.

// manualSizeFields, manualDurationFields and manualNumberFields are the
// canonical stats by shape. A field in none of them (a tracker-specific
// extra, "group") is passed through as typed: the canonical set grows, and
// refusing a value merely because it is unrecognised would be worse than
// storing it.
var manualSizeFields = map[string]bool{
	"uploaded": true, "downloaded": true, "buffer": true, "seed_size": true,
	"real_uploaded": true, "real_downloaded": true,
}

var manualDurationFields = map[string]bool{
	"avg_seed_time": true, "total_seedtime": true,
}

var manualNumberFields = map[string]bool{
	"ratio": true, "real_ratio": true, "required_ratio": true, "bonus_points": true,
	"seeding": true, "leeching": true, "hit_and_runs": true, "snatched": true,
	"grabbed": true, "upload_snatches": true, "fl_tokens": true, "invites": true,
	"warnings": true, "uploads_approved": true, "adoptions": true,
	"requests_filled": true, "forum_posts": true,
}

// Targets by shape. avg_seed and days are stored as plain seconds and days
// (what the form has always sent), so a duration or age typed straight into
// the API is converted rather than kept as text.
var targetSizeKeys = map[string]bool{
	"uploaded": true, "downloaded": true, "seed_size": true, "total_transfer": true,
	// Offered generically wherever the tracker reports them; compared as
	// sizes by the dashboard's generic target loop.
	"buffer": true, "real_uploaded": true, "real_downloaded": true,
}

var targetNumberKeys = map[string]bool{
	"ratio": true, "total_uploads": true, "adoptions": true, "bonus_points": true,
	"snatched": true, "monthly_uploads": true,
}

// shapeError says what was expected in the words the form uses, so the
// toast and the inline message read the same.
func shapeError(what, key, value, shape string) error {
	return fmt.Errorf("%s %q: %q is not %s", what, key, value, shape)
}

const (
	sizeShape     = "a size — enter one like 1.5 TiB or 200 GB"
	durationShape = "a duration — enter one like 3M 6D (Y M W D h m s)"
	numberShape   = "a number"
	ageShape      = "an account age — enter days, or one like 1Y 6M"
)

// manualStatsMaxFields caps how many values one tracker can carry. Well past
// the ~30 canonical stats the form offers, and low enough that a malformed or
// hostile POST can't turn the config file into a dumping ground.
const manualStatsMaxFields = 100

// sanitizeManualStats cleans user-typed stat values into the same shapes a
// fetch produces, so a typed number and a fetched one are indistinguishable to
// everything downstream — sorting, targets, charts, alert rules. Sizes become
// "%.2f UNIT" with the unit's case fixed, durations the canonical "1Y 2M 3D"
// form. A value that fails its shape is an error naming the field;
// validateTrackerPayload runs this before anything is applied.
//
// Empty values are dropped rather than stored, so clearing a row removes the
// stat instead of leaving an empty string that reads as a real answer.
func sanitizeManualStats(in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for field, raw := range in {
		if len(out) >= manualStatsMaxFields {
			break
		}
		field = strings.TrimSpace(field)
		v := strings.TrimSpace(raw)
		if field == "" || v == "" {
			continue
		}
		var ok bool
		switch {
		case manualSizeFields[field]:
			if v, ok = parse.NormalizeSizeInput(v); !ok {
				return nil, shapeError("manual stat", field, raw, sizeShape)
			}
		case manualDurationFields[field]:
			if v, ok = parse.NormalizeDurationInput(v); !ok {
				return nil, shapeError("manual stat", field, raw, durationShape)
			}
		case manualNumberFields[field]:
			if !parse.IsNumberInput(v) {
				return nil, shapeError("manual stat", field, raw, numberShape)
			}
		}
		out[field] = v
	}
	return out, nil
}

// sanitizeTargets checks and canonicalises a targets map the same way. Keys
// it does not know ("count:<group>" requirement counters, anything a newer
// form sends) pass through as typed.
func sanitizeTargets(in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for key, raw := range in {
		key = strings.TrimSpace(key)
		v := strings.TrimSpace(raw)
		if key == "" || v == "" {
			continue
		}
		var ok bool
		switch {
		case targetSizeKeys[key]:
			if v, ok = parse.NormalizeSizeInput(v); !ok {
				return nil, shapeError("target", key, raw, sizeShape)
			}
		case targetNumberKeys[key]:
			if !parse.IsNumberInput(v) {
				return nil, shapeError("target", key, raw, numberShape)
			}
		case key == "avg_seed":
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				norm, ok := parse.NormalizeDurationInput(v)
				if !ok {
					return nil, shapeError("target", key, raw, durationShape)
				}
				v = strconv.FormatInt(int64(*parse.SeedTimeToSeconds(norm)), 10)
			}
		case key == "days":
			if _, err := strconv.Atoi(v); err != nil {
				days, ok := ageDays(v)
				if !ok {
					return nil, shapeError("target", key, raw, ageShape)
				}
				v = strconv.Itoa(days)
			}
		}
		out[key] = v
	}
	return out, nil
}

// ageDays reads an account-age target in the "1Y 6M 2W 3D" form the form
// offers (utils/parse.ts parseAgeDays), as whole days. Upper-cased first:
// an age has no minutes, so "6m" can only mean months here.
func ageDays(s string) (int, bool) {
	norm, ok := parse.NormalizeDurationInput(strings.ToUpper(s))
	if !ok {
		return 0, false
	}
	secs := parse.SeedTimeToSeconds(norm)
	if secs == nil || *secs < 86400 {
		return 0, false
	}
	return int(*secs / 86400), true
}

// validateTrackerPayload is the shape check for everything typed. It runs
// before applyPayload so a bad value is refused as a 400 naming the field
// and nothing is written — a config half-updated by a rejected request would
// be worse than either outcome.
func validateTrackerPayload(p *trackerPayload) error {
	if p.ManualStats != nil {
		clean, err := sanitizeManualStats(*p.ManualStats)
		if err != nil {
			return err
		}
		*p.ManualStats = clean
	}
	if p.Targets != nil {
		clean, err := sanitizeTargets(*p.Targets)
		if err != nil {
			return err
		}
		*p.Targets = clean
	}
	return nil
}
