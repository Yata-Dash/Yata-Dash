package api

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// Type detection for trackers Yata has no definition for. The user's own API
// key is tried against each candidate type's endpoint until one answers with
// usable stats.
//
// This is deliberately manual — a button, never automatic. Detection means a
// handful of requests that are expected to fail, aimed at a tracker whose
// operator has not agreed to anything, and that is not something to do behind
// the user's back or on a schedule.

// detectCandidates are the types worth probing, most common first. "custom"
// is absent because a custom fetcher needs a path and field map that only a
// definition can supply — if a tracker needed custom handling it would have a
// definition already. Scrape-only types are absent because there is nothing
// to probe.
var detectCandidates = []string{"unit3d", "gazelle_json", "gazelle_games", gazelleANTNEBType}

// gazelleANTNEBType is the Anthelion/Nebulance family — one type key that
// several places key behaviour off (detection, the Gazelle profile layout),
// so it is named once rather than spelled out at each site.
const gazelleANTNEBType = "gazelle_antneb"

// detectHallmarks are canonical fields a genuine stats response is expected to
// carry. A match needs at least one of them, because "the request returned
// some JSON" is a much weaker signal than it looks: plenty of sites answer an
// unknown path with 200 and a one-key error body, which a field count alone
// reads as a successful match.
var detectHallmarks = []string{"username", "uploaded", "downloaded", "ratio", "seeding", "bonus_points"}

// needsUsername reports whether probing as this type requires a username —
// either because its custom API interpolates one into the path, or because the
// type declares it as a required config field. Derived rather than listed by
// type key, so a new family with a "{username}" endpoint is handled without
// anyone remembering to update this file.
func needsUsername(d *Deps, trackerURL, typeKey string) bool {
	if api := d.Reg.ResolveCustomAPI(trackerURL, typeKey); api != nil && strings.Contains(api.Path, "{username}") {
		return true
	}
	tt, ok := d.Reg.Type(typeKey)
	return ok && slices.Contains(tt.API.RequiredFields, "username")
}

// looksLikeStats reports whether a fetched field set is convincing enough to
// adopt the type that produced it.
func looksLikeStats(fields map[string]any) bool {
	for _, f := range detectHallmarks {
		if v, ok := fields[f]; ok && v != nil && v != "" {
			return true
		}
	}
	return false
}

// detectAttempt is one candidate's outcome, returned in full so the user can
// see what was tried rather than just "nothing worked".
type detectAttempt struct {
	Type   string `json:"type"`
	Label  string `json:"label"`
	Status string `json:"status"` // ok | fail | not_configured
	Detail string `json:"detail,omitempty"`
	Fields int    `json:"fields,omitempty"`
}

type detectResponse struct {
	Detected string          `json:"detected,omitempty"` // type key, "" = nothing matched
	Label    string          `json:"label,omitempty"`
	Applied  bool            `json:"applied"`
	Attempts []detectAttempt `json:"attempts"`
}

// POST /api/trackers/{id}/detect — probe candidate types and adopt the first
// that returns stats.
func detectTrackerType(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, ok := d.Cfg.Tracker(id)
		if !ok {
			jsonError(w, "not found", http.StatusNotFound)
			return
		}
		// A tracker with a definition already knows what it is; probing would
		// be pointless traffic.
		if td, defined := d.Reg.TrackerByURL(t.URL); defined && td.Type != "" {
			jsonStatus(w, http.StatusConflict, map[string]any{
				"error": "has_definition", "type": td.Type,
			})
			return
		}
		if _, opted := d.Reg.OptOut(t.URL); opted {
			jsonStatus(w, http.StatusForbidden, map[string]any{"error": "tracker_opted_out"})
			return
		}
		if strings.TrimSpace(t.APIKey) == "" {
			jsonStatus(w, http.StatusBadRequest, map[string]any{"error": "no_key"})
			return
		}

		resp := detectResponse{Attempts: make([]detectAttempt, 0, len(detectCandidates))}
		for _, typeKey := range detectCandidates {
			label := typeKey
			if tt, found := d.Reg.Type(typeKey); found {
				label = tt.Label
			}
			probe := t
			probe.Type = typeKey
			attempt := detectAttempt{Type: typeKey, Label: label}

			// Some types select the account by name as well as by key. Say so
			// rather than reporting the resulting failure as "this isn't that
			// kind of site" — the probe never had what it needed to succeed.
			if needsUsername(d, t.URL, typeKey) && strings.TrimSpace(t.Username) == "" {
				attempt.Status, attempt.Detail = "not_configured", "no_username"
				resp.Attempts = append(resp.Attempts, attempt)
				continue
			}
			fields, ferr := d.Fetch.Fetch(probe)
			switch {
			case ferr != nil:
				attempt.Status, attempt.Detail = "fail", ferr.Kind
				d.logWarnf("detect: %s as %s — %v", t.Name, typeKey, ferr)
			case !looksLikeStats(fields):
				// It answered, but with nothing that resembles account stats.
				attempt.Status, attempt.Detail = "ok", "no_stats"
				attempt.Fields = len(fields)
			default:
				attempt.Status, attempt.Fields = "ok", len(fields)
			}
			resp.Attempts = append(resp.Attempts, attempt)
			if attempt.Status == "ok" && attempt.Detail == "" {
				resp.Detected, resp.Label = typeKey, label
				break
			}
		}

		if resp.Detected != "" {
			if err := d.Cfg.UpdateTracker(id, func(tr *models.Tracker) {
				tr.Type = resp.Detected
				clampTrackerScrape(d, tr)
			}); err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			resp.Applied = true
			d.logInfof("tracker: detected %s as type %s", t.Name, resp.Detected)
		} else {
			d.logInfof("tracker: type detection found no match for %s", t.Name)
		}
		// Record the probe as this tracker's latest test result so the table
		// pill reflects what just happened rather than a stale success.
		if resp.Applied {
			updated, _ := d.Cfg.Tracker(id)
			hit := resp.Attempts[len(resp.Attempts)-1] // the loop breaks on the match
			testResults.Store(id, TrackerTestResult{
				API:      CheckResult{Status: "ok", Fields: hit.Fields},
				Scrape:   testScrape(d, updated, false),
				TestedAt: time.Now().Unix(),
			})
		}
		jsonOK(w, resp)
	}
}
