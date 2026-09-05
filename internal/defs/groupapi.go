package defs

import (
	"encoding/json"
	"strings"

	"github.com/Yata-Dash/Yata-Dash/internal/parse"
)

// This file turns a tracker's own group-ladder response into the []GroupDef
// the rest of Yata already knows how to render, so a ladder fetched at runtime
// is indistinguishable downstream from one written into a def by hand.
//
// See GROUP_API_PLAN.md. The mapping is here rather than in internal/fetch
// because it is a def-shape transformation with no HTTP in it — which is also
// what makes it testable against a saved payload.

// apiGroup is one rank as the tracker reports it. Fields the platform does not
// serve yet (style, perks) are read when present and simply absent otherwise,
// so adding them upstream needs no change here and no def edit.
type apiGroup struct {
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Color       string      `json:"color"`
	Icon        string      `json:"icon"`
	Perks       []apiPerk   `json:"perks"`
	Reqs        []apiGroupR `json:"requirements"`
}

// apiPerk tolerates both shapes a perk can arrive in: an object with an icon,
// and the bare string traxary's `premium` ladder uses today.
type apiPerk struct {
	Icon  string
	Label string
}

func (p *apiPerk) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		p.Label = s
		return nil
	}
	var obj struct {
		Icon  string `json:"icon"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	p.Icon, p.Label = obj.Icon, obj.Label
	return nil
}

// apiGroupR is one threshold. Satisfied is read but never stored: it is the
// tracker's verdict on THIS user, so it belongs to the account, not to the
// ladder — see StripGroupProgress.
type apiGroupR struct {
	Type  string  `json:"type"`
	Value float64 `json:"value"`
}

// byteFields, durationFields and countFields say how a raw API number is
// written into each GroupRequirements field. The unit is a property of the
// destination field, not of the tracker, so a def declares only WHICH field a
// requirement maps to and never restates that min_uploaded holds a size string.
var (
	byteFields = map[string]func(*GroupRequirements, string){
		"min_uploaded":       func(r *GroupRequirements, v string) { r.MinUploaded = v },
		"min_downloaded":     func(r *GroupRequirements, v string) { r.MinDownloaded = v },
		"min_total_transfer": func(r *GroupRequirements, v string) { r.MinTotalTransfer = v },
		"min_seed_size":      func(r *GroupRequirements, v string) { r.MinSeedSize = v },
	}
	durationFields = map[string]func(*GroupRequirements, string){
		"min_age":      func(r *GroupRequirements, v string) { r.MinAge = v },
		"min_seedtime": func(r *GroupRequirements, v string) { r.MinSeedtime = v },
	}
	countFields = map[string]func(*GroupRequirements, float64){
		"min_uploads":         func(r *GroupRequirements, v float64) { r.MinUploads = int(v) },
		"min_adoptions":       func(r *GroupRequirements, v float64) { r.MinAdoptions = int(v) },
		"min_bonus_points":    func(r *GroupRequirements, v float64) { r.MinBonusPoints = int(v) },
		"min_monthly_uploads": func(r *GroupRequirements, v float64) { r.MinMonthlyUploads = int(v) },
		"min_ratio":           func(r *GroupRequirements, v float64) { r.MinRatio = v },
	}
)

// LadderFromAPI maps a group-ladder response onto the ladder Yata ranks the
// user against, lowest rung first. Returns nil when the response carries no
// usable ladder, which callers must treat as "unknown" and never as "empty":
// an empty ladder would read as a tracker with no ranks at all.
func LadderFromAPI(body []byte, spec GroupAPISpec) []GroupDef {
	if spec.Ladder == "" {
		return nil
	}
	var payload map[string][]apiGroup
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	raw, ok := payload[spec.Ladder]
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]GroupDef, 0, len(raw))
	for _, g := range raw {
		name := strings.TrimSpace(g.Title)
		if name == "" {
			continue // a rank with no name cannot be matched or shown
		}
		gd := GroupDef{
			Name:         name,
			Style:        GroupStyle{Color: strings.TrimSpace(g.Color), Icon: strings.TrimSpace(g.Icon)},
			Requirements: requirementsFromAPI(g.Reqs, spec.Requirements),
		}
		gd.Requirements.Description = strings.TrimSpace(g.Description)
		for _, p := range g.Perks {
			if label := strings.TrimSpace(p.Label); label != "" {
				gd.Perks = append(gd.Perks, GroupPerk{Icon: strings.TrimSpace(p.Icon), Label: label})
			}
		}
		out = append(out, gd)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// requirementsFromAPI converts one rank's thresholds. Order within min_counts
// follows the API's own order, which is the order the tracker's own page lists
// them in.
func requirementsFromAPI(reqs []apiGroupR, mapping map[string]string) GroupRequirements {
	var out GroupRequirements
	for _, r := range reqs {
		field := mapping[strings.TrimSpace(r.Type)]
		if field == "" {
			continue // unmeasurable — see GroupAPISpec.Requirements
		}
		switch {
		case byteFields[field] != nil:
			byteFields[field](&out, parse.BytesToSize(int64(r.Value)))
		case durationFields[field] != nil:
			durationFields[field](&out, parse.FormatSeedTime(r.Value))
		case countFields[field] != nil:
			countFields[field](&out, r.Value)
		default:
			// Not one of Yata's own fields, so it is a count of a canonical
			// stat this tracker happens to report (comments, forum posts).
			// No Label: the UI falls back to the field's own label, which is
			// the same wording every other view already uses for it.
			out.MinCounts = append(out.MinCounts, MinCountReq{Field: field, Count: int(r.Value)})
		}
	}
	return out
}

// StripGroupProgress removes the per-user verdict the tracker attaches to each
// threshold ("satisfied"), returning canonical JSON of what is left.
//
// This is what makes a stored ladder a record of the SITE's rules. The flag
// flips as the user climbs, so hashing the response as it arrived would file a
// fresh "this tracker changed its requirements" revision every time they
// crossed a threshold — recording the user's progress in the one table that is
// supposed to be about everything except that.
//
// Re-marshalling also settles key order, so two identical ladders hash the
// same however the platform happened to serialise them.
func StripGroupProgress(body []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	return json.Marshal(stripProgress(v))
}

// progressKeys are per-user annotations on a requirement, never site facts.
var progressKeys = map[string]bool{"satisfied": true, "met": true, "current": true, "progress": true}

func stripProgress(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if progressKeys[strings.ToLower(k)] {
				continue
			}
			out[k] = stripProgress(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = stripProgress(val)
		}
		return out
	default:
		return v
	}
}

// LadderHasGroup reports whether name is one of the ladder's ranks. Used to
// notice that the user holds a rank the cached ladder has never heard of,
// which is the strongest available signal that the tracker changed it.
func LadderHasGroup(groups []GroupDef, name string) bool {
	return LadderIndex(groups, name) >= 0
}
