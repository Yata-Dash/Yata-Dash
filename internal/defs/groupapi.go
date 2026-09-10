package defs

import (
	"encoding/json"
	"fmt"
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

// apiGroup is one rank as the tracker reports it, and — because CanonicalLadder
// re-encodes through these structs — it is also the exact set of fields that
// ever reaches the store. Fields the platform does not serve yet (style, perks)
// are read when present and simply absent otherwise, so adding them upstream
// needs no change here and no def edit.
type apiGroup struct {
	ID          int         `json:"id,omitempty"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Color       string      `json:"color,omitempty"`
	Icon        string      `json:"icon,omitempty"`
	Perks       []apiPerk   `json:"perks,omitempty"`
	Reqs        []apiGroupR `json:"requirements,omitempty"`
}

// apiPerk tolerates both shapes a perk can arrive in: an object with an icon,
// and the bare string traxary's `premium` ladder uses today. It re-encodes as
// the object form — one shape in the store, and UnmarshalJSON reads it back.
type apiPerk struct {
	Icon  string `json:"icon,omitempty"`
	Label string `json:"label"`
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

// apiGroupR is one threshold. It has no field for the tracker's per-user
// verdict ("satisfied"), which is how that verdict is kept out of the store —
// see CanonicalLadder.
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
	raw, ok := decodeLadders(body)[spec.Ladder]
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

// CanonicalLadder projects a group-ladder response down to the ladder fields
// Yata models, and returns that as JSON. It is what gets hashed and stored.
//
// An allowlist by construction: the response is decoded into the structs above
// and re-encoded, so a field none of them declares cannot reach the store. That
// matters for two different reasons.
//
// The response comes from a USER endpoint, and the store is a record of the
// SITE's rules. traxary annotates each threshold with "satisfied" — its verdict
// on this account — and any future per-user field would arrive the same way.
// Hashing one would file a fresh "this tracker changed its requirements"
// revision every time the user crossed a threshold, recording their progress in
// the table meant to be about everything except that. A denylist would only
// have caught the field names we thought of.
//
// Re-encoding also settles key order and perk shape, so two identical ladders
// hash the same however the platform serialised them.
func CanonicalLadder(body []byte) ([]byte, error) {
	ladders := decodeLadders(body)
	if len(ladders) == 0 {
		return nil, fmt.Errorf("no group ladders in response")
	}
	return json.Marshal(ladders)
}

// decodeLadders reads the ladders out of a response, keyed as the tracker keys
// them, and ignores everything else at the top level.
//
// Per key rather than one decode of the whole object, because this is a USER
// endpoint: a scalar like "viewer_id": 4021 sitting beside the ladders is an
// entirely reasonable thing for it to grow, and decoding the object in one go
// would fail on it and take the whole feature down. Skipping what does not
// parse as a ladder drops such a field AND survives it.
func decodeLadders(body []byte) map[string][]apiGroup {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil
	}
	out := make(map[string][]apiGroup, len(top))
	for key, raw := range top {
		var groups []apiGroup
		if err := json.Unmarshal(raw, &groups); err != nil {
			continue
		}
		out[key] = groups
	}
	return out
}

// LadderHasGroup reports whether name is one of the ladder's ranks. Used to
// notice that the user holds a rank the cached ladder has never heard of,
// which is the strongest available signal that the tracker changed it.
func LadderHasGroup(groups []GroupDef, name string) bool {
	return LadderIndex(groups, name) >= 0
}
