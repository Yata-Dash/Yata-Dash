// targeteval.go computes target-row met/unmet state for the alert engine's
// target_met event tracking. Mirrors web/src/views/grid.ts's targetRowsFor +
// the any_of/min_counts rendering in buildTargets, and web/src/utils/group.ts's
// groupRequirementsToTargets — parity of MEANING (what counts as "met"), not
// pixels. Kept separate from grid.ts's actual progress-bar math (percentages,
// ETAs) since events only need a boolean per row.
package api

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
	"github.com/Yata-Dash/Yata-Dash/internal/pathways"
)

// knownTargetLabels gives the display label for each core target key —
// mirrors CORE_TARGET_SPECS / targetRowsFor's row labels in the frontend.
var knownTargetLabels = map[string]string{
	"uploaded":      "Uploaded",
	"downloaded":    "Downloaded",
	"ratio":         "Ratio",
	"seed_size":     "Seed Size",
	"total_uploads": "Total Uploads",
	"days":          "Account Age",
	"avg_seed":      "Avg Seed Time",
	"bonus_points":  "Bonus Points",
	"adoptions":     "Adoptions",
	"snatched":      "Snatched",
}

// targetKeyOrder is the fixed evaluation/label order for known target keys —
// mirrors the sequence of if-blocks in grid.ts's targetRowsFor — so any_of
// alternative labels and row iteration are deterministic instead of riding on
// Go's randomised map order.
var targetKeyOrder = []string{
	"uploaded", "downloaded", "ratio", "seed_size", "total_uploads",
	"days", "avg_seed", "bonus_points", "adoptions", "snatched",
}

// evaluateTargetRows computes every EDGE-tracked target row for a tracker —
// base targets (t.Targets) plus its target group's min_counts and any_of
// alternatives — plus the logical met/total counts used in the target_met
// message's m/T. any_of alternatives collapse to ONE logical row for m/T
// (matching how buildTargets renders "One of" as a single requirement) but
// stay separate notify.TargetRow entries so each alternative can fire its
// own event when IT crosses (user-approved: two alternatives met at
// different times each fire, sharing the same m/T).
func evaluateTargetRows(t models.Tracker, merged models.MergedStats, groups []defs.GroupDef) (rows []notify.TargetRow, logicalMet, logicalTotal int) {
	for _, key := range sortedTargetKeys(t.Targets) {
		tgt := t.Targets[key]
		if tgt == "" {
			continue
		}
		met := baseTargetMet(key, tgt, merged)
		rows = append(rows, notify.TargetRow{Key: key, Label: targetLabel(key), Met: met})
		logicalTotal++
		if met {
			logicalMet++
		}
	}

	targetGroup := findGroupByName(groups, t.TargetGroup)
	if targetGroup == nil {
		return rows, logicalMet, logicalTotal
	}

	for _, mc := range targetGroup.Requirements.MinCounts {
		if mc.Field == "" || mc.Count <= 0 {
			continue
		}
		label := mc.Label
		if label == "" {
			label = titleCaseKey(mc.Field)
		}
		met := currentNumeric(merged, mc.Field) >= float64(mc.Count)
		rows = append(rows, notify.TargetRow{Key: "min_counts." + mc.Field, Label: label, Met: met})
		logicalTotal++
		if met {
			logicalMet++
		}
	}

	if anyOf := targetGroup.Requirements.AnyOf; len(anyOf) > 0 {
		logicalTotal++
		anyMet := false
		for i, alt := range anyOf {
			altTargets := groupRequirementsToTargets(alt)
			altKeys := sortedTargetKeys(altTargets)
			altMet := len(altKeys) > 0
			labels := make([]string, 0, len(altKeys))
			for _, k := range altKeys {
				if !baseTargetMet(k, altTargets[k], merged) {
					altMet = false
				}
				labels = append(labels, targetLabel(k))
			}
			rows = append(rows, notify.TargetRow{
				Key:   fmt.Sprintf("any_of.%d", i),
				Label: "One of: " + strings.Join(labels, " + "),
				Met:   altMet,
			})
			if altMet {
				anyMet = true
			}
		}
		if anyMet {
			logicalMet++
		}
	}
	return rows, logicalMet, logicalTotal
}

// findGroupByName looks up a group def by name (case-insensitive), or nil.
func findGroupByName(groups []defs.GroupDef, name string) *defs.GroupDef {
	if name == "" {
		return nil
	}
	for i := range groups {
		if strings.EqualFold(groups[i].Name, name) {
			return &groups[i]
		}
	}
	return nil
}

// sortedTargetKeys returns a target map's keys in canonical order (known
// keys first, in targetKeyOrder, then any custom keys alphabetically) so
// evaluation and any_of labels are deterministic.
func sortedTargetKeys(targets map[string]string) []string {
	seen := make(map[string]bool, len(targets))
	out := make([]string, 0, len(targets))
	for _, k := range targetKeyOrder {
		if _, ok := targets[k]; ok {
			out = append(out, k)
			seen[k] = true
		}
	}
	var extra []string
	for k := range targets {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// targetLabel is a known key's display label, or the title-cased key itself.
func targetLabel(key string) string {
	if l, ok := knownTargetLabels[key]; ok {
		return l
	}
	return titleCaseKey(key)
}

// titleCaseKey mirrors web/src/utils/format.ts's fieldLabel: "fl_tokens" →
// "Fl Tokens".
func titleCaseKey(key string) string {
	parts := strings.Split(key, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// baseTargetMet reports whether one base/alt target key is currently met.
// Missing or unparseable current data is UNMET, never skipped — an omitted
// stat may be an omitted zero (mirrors grid.ts's "miss" fallback rows).
func baseTargetMet(key, tgt string, merged models.MergedStats) bool {
	switch key {
	case "uploaded", "downloaded", "seed_size":
		return sizeMet(mergedFieldString(merged, key), tgt)
	case "ratio":
		return ratioMet(mergedFieldString(merged, "ratio"), tgt)
	case "total_uploads":
		return numMet(mergedFieldString(merged, "uploads_approved"), tgt)
	case "days":
		return ageDaysMet(mergedFieldString(merged, "join_date"), tgt)
	case "avg_seed":
		return seedTimeMet(mergedFieldString(merged, "avg_seed_time"), tgt)
	case "bonus_points", "adoptions", "snatched":
		return numMet(mergedFieldString(merged, key), tgt)
	default:
		// Unknown/custom key — targets a merged stat field of the same name.
		return customMet(mergedFieldString(merged, key), tgt)
	}
}

// sizeMet compares two size strings in GiB (>=).
func sizeMet(curRaw, tgtRaw string) bool {
	tgtV := parse.SizeToGiB(tgtRaw)
	if tgtV == nil || *tgtV <= 0 {
		return false
	}
	curV := parse.SizeToGiB(curRaw)
	if curV == nil {
		return false
	}
	return *curV >= *tgtV
}

// parseRatioValue parses a ratio field that may be numeric or an "infinite"
// sentinel (some trackers/normalizeCustomString report "Infinity"/"∞"/"Inf"
// when downloaded is 0) — mirrors web/src/utils/format.ts's parseRatio.
func parseRatioValue(raw string) (float64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	if strings.EqualFold(s, "infinity") || strings.EqualFold(s, "inf") || s == "∞" {
		return math.Inf(1), true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func ratioMet(curRaw, tgtRaw string) bool {
	tgtV, ok := parseRatioValue(tgtRaw)
	if !ok || tgtV <= 0 {
		return false
	}
	curV, ok := parseRatioValue(curRaw)
	if !ok {
		return false
	}
	return curV >= tgtV
}

// ageDaysMet compares a join_date (YYYY-MM-DD) against a target expressed as
// plain days or a pathways-style duration ("1Y 6M", "6 months", …).
func ageDaysMet(joinDate, tgtRaw string) bool {
	tgtDays, ok := pathways.ParseDurationDays(tgtRaw)
	if !ok || tgtDays <= 0 {
		return false
	}
	jd := strings.TrimSpace(joinDate)
	if jd == "" {
		return false
	}
	if len(jd) > 10 {
		jd = jd[:10] // tolerate a full timestamp, same as the rest of the app
	}
	parsed, err := time.Parse("2006-01-02", jd)
	if err != nil {
		return false
	}
	curDays := math.Floor(time.Since(parsed).Hours() / 24)
	return curDays >= tgtDays
}

// seedTimeMet compares two seed-time values (duration string or plain
// seconds) in seconds.
func seedTimeMet(curRaw, tgtRaw string) bool {
	tgtSec := parse.SeedTimeToSeconds(tgtRaw)
	if tgtSec == nil || *tgtSec <= 0 {
		return false
	}
	curSec := parse.SeedTimeToSeconds(curRaw)
	if curSec == nil {
		return false
	}
	return *curSec >= *tgtSec
}

// numMet compares two plain (possibly comma-grouped) numbers.
func numMet(curRaw, tgtRaw string) bool {
	tgtV, ok := parseCleanFloat(tgtRaw)
	if !ok || tgtV <= 0 {
		return false
	}
	curV, ok := parseCleanFloat(curRaw)
	if !ok {
		return false
	}
	return curV >= tgtV
}

// customMet handles a tracker-specific target key: compare as a size when
// BOTH sides parse as one, otherwise fall back to plain numeric comparison
// for both — mirrors grid.ts's generic fallback loop exactly (it doesn't mix
// units when only one side looks like a size).
func customMet(curRaw, tgtRaw string) bool {
	curV, tgtV := parse.SizeToGiB(curRaw), parse.SizeToGiB(tgtRaw)
	if curV == nil || tgtV == nil {
		c, cok := parseCleanFloat(curRaw)
		g, gok := parseCleanFloat(tgtRaw)
		curV, tgtV = nil, nil
		if cok {
			curV = &c
		}
		if gok {
			tgtV = &g
		}
	}
	if tgtV == nil || *tgtV <= 0 {
		return false
	}
	if curV == nil {
		return false
	}
	return *curV >= *tgtV
}

func parseCleanFloat(raw string) (float64, bool) {
	s := strings.ReplaceAll(strings.TrimSpace(raw), ",", "")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// currentNumeric reads a merged stat field as a plain number, treating
// anything missing/unparseable as 0 — an omitted count is an omitted zero,
// never a skip (same rule as baseTargetMet).
func currentNumeric(merged models.MergedStats, field string) float64 {
	v, _ := parseCleanFloat(mergedFieldString(merged, field))
	return v
}

// groupRequirementsToTargets ports web/src/utils/group.ts's function of the
// same name: maps a group requirement set's BASE fields (never any_of —
// callers handle alternatives separately) to the same target-map keys the
// settings form uses. min_age/min_seedtime normalise to plain days/seconds
// like the frontend's "Load from Group" does. min_monthly_uploads and
// Description are never evaluated (no live stat backs them / non-stat-based
// group) — same as the frontend's pathway evaluation.
func groupRequirementsToTargets(req defs.GroupRequirements) map[string]string {
	out := map[string]string{}
	if req.MinUploaded != "" {
		out["uploaded"] = req.MinUploaded
	}
	if req.MinDownloaded != "" {
		out["downloaded"] = req.MinDownloaded
	}
	if req.MinRatio != 0 {
		out["ratio"] = strconv.FormatFloat(req.MinRatio, 'f', -1, 64)
	}
	if req.MinSeedSize != "" {
		out["seed_size"] = req.MinSeedSize
	}
	if req.MinUploads != 0 {
		out["total_uploads"] = strconv.Itoa(req.MinUploads)
	}
	if req.MinAdoptions != 0 {
		out["adoptions"] = strconv.Itoa(req.MinAdoptions)
	}
	if days, ok := pathways.ParseDurationDays(req.MinAge); ok {
		out["days"] = strconv.FormatFloat(days, 'f', -1, 64)
	}
	if sec := parse.SeedTimeToSeconds(req.MinSeedtime); sec != nil {
		out["avg_seed"] = strconv.FormatFloat(*sec, 'f', -1, 64)
	}
	if req.MinBonusPoints != 0 {
		out["bonus_points"] = strconv.Itoa(req.MinBonusPoints)
	}
	// No live stat backs monthly_uploads yet, so the row always evaluates
	// unmet (customMet on a missing field) — which keeps an any_of alternative
	// containing it from counting as met here while the dashboard's
	// eligibility math (unavailable = assumed zero = unmet) says otherwise.
	if req.MinMonthlyUploads != 0 {
		out["monthly_uploads"] = strconv.Itoa(req.MinMonthlyUploads)
	}
	return out
}
