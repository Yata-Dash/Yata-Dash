package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/scrape"
)

const maskedKey = "••••••••"

func registerTrackers(r chi.Router, d *Deps) {
	r.Get("/trackers", listTrackers(d))
	r.Post("/trackers", createTracker(d))
	r.Put("/trackers/{id}", updateTracker(d))
	r.Delete("/trackers/{id}", deleteTracker(d))
	r.Post("/trackers/{id}/test", testTracker(d))
	r.Get("/trackers/test-status", testStatusAll(d))
	r.Post("/trackers/test-adhoc", testAdhocTracker(d))
}

// toView converts a Tracker into its safe public representation, enriched
// with def-derived metadata.
func toView(d *Deps, t models.Tracker) models.TrackerView {
	v := models.TrackerView{
		ID:                       t.ID,
		Name:                     t.Name,
		URL:                      t.URL,
		Type:                     t.Type,
		Enabled:                  t.Enabled,
		HasKey:                   strings.TrimSpace(t.APIKey) != "",
		HasSession:               strings.TrimSpace(t.SessionCookie) != "",
		Username:                 t.Username,
		Targets:                  t.Targets,
		TargetGroup:              t.TargetGroup,
		TargetDeadlines:          t.TargetDeadlines,
		JoinDate:                 t.JoinDate,
		MinScrapeIntervalMinutes: t.MinScrapeIntervalMinutes,
		MaxScrapesPerDay:         t.MaxScrapesPerDay,
		AutoInterval:             t.AutoInterval,
		APIOnly:                  t.APIOnly,
		MockScenario:             t.MockScenario,
	}
	if v.Targets == nil {
		v.Targets = map[string]string{}
	}
	if v.TargetDeadlines == nil {
		v.TargetDeadlines = map[string]string{}
	}
	if v.HasKey {
		v.APIKeyMasked = maskedKey
	}
	typeKey := t.Type
	v.DefApproval = defs.ApprovalUnknown // manual trackers: nobody signed off
	var customAPI *defs.CustomAPI        // def's custom API, for requiredFieldsFor
	if td, ok := d.Reg.TrackerByURL(t.URL); ok {
		v.DefKey = td.Key
		v.Abbr = td.Abbr
		v.DefApproval = td.ApprovalStatus()
		v.DefApprovalNote = td.ApprovalNote()
		typeKey = td.Type
		if v.Name == "" {
			v.Name = td.Name
		}
		if td.API != nil && td.API.APIKeyHint != "" {
			v.APIKeyHint = td.API.APIKeyHint
		}
		if td.Rules != nil {
			v.MinRatio = td.Rules.MinRatio
			v.MinSeedDays = td.Rules.MinSeedDays
			v.MinSeedHours = td.Rules.MinSeedHours
			v.MinSeedDaysEpisode = td.Rules.MinSeedDaysEpisode
			v.MinSeedDaysSeason = td.Rules.MinSeedDaysSeason
		}
		customAPI = td.API
	}
	if tt, ok := d.Reg.Type(typeKey); ok {
		// Fields the def's API already provides aren't required from the user
		// (e.g. HUNO's member_since → join_date).
		v.RequiredFields = requiredFieldsFor(tt.API.RequiredFields, customAPI)
	}
	rs := d.Reg.ResolveScrape(t.URL, t.Type)
	v.SupportsHTMLScrape = !rs.SkipHTMLScrape && !rs.DisableScraping
	v.ScrapeDisabledByTracker = rs.DisableScraping
	if rs.OptedOut {
		v.OptedOut = true
		v.OptOutNote = rs.OptOut.Note
	}
	v.TrackerMinInterval = rs.MinIntervalMinutes
	v.TrackerMaxPerDay = rs.MaxScrapesPerDay
	v.ProfileURL = profileURL(d, t, rs)
	return v
}

// profileURL builds the user's profile page link. Path-based types (Unit3D)
// substitute the username; ID-based types (gazelle's /user.php?id=N) substitute
// the user_id captured from the API into the merged stats. When a required
// substitution value is unavailable, fall back to the tracker base URL.
func profileURL(d *Deps, t models.Tracker, rs defs.ResolvedScrape) string {
	if strings.TrimSpace(t.URL) == "" {
		return ""
	}
	base := strings.TrimRight(t.URL, "/")
	path := rs.ProfilePath
	if path == "" {
		return base
	}
	// ID-based profile URLs need the user_id stat (fetched from the API).
	if strings.Contains(path, "{id}") {
		uid := mergedString(d, t.ID, "user_id")
		if uid == "" {
			return base
		}
		path = strings.ReplaceAll(path, "{id}", uid)
	}
	if strings.Contains(path, "{username}") {
		if strings.TrimSpace(t.Username) == "" {
			return base
		}
		path = strings.ReplaceAll(path, "{username}", t.Username)
	}
	return base + path
}

// mergedString reads one merged stat field as a trimmed string ("" if absent).
func mergedString(d *Deps, trackerID, field string) string {
	if d == nil || d.Stats == nil {
		return ""
	}
	merged, err := d.Stats.Merged(trackerID)
	if err != nil {
		return ""
	}
	if f, ok := merged[field]; ok {
		if s, ok := f.Value.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func listTrackers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		trackers := d.Cfg.Trackers()
		out := make([]models.TrackerView, 0, len(trackers))
		for _, t := range trackers {
			out = append(out, toView(d, t))
		}
		jsonOK(w, out)
	}
}

// trackerPayload is the create/update request body.
type trackerPayload struct {
	Name                     *string            `json:"name"`
	URL                      *string            `json:"url"`
	Type                     *string            `json:"type"`
	APIKey                   *string            `json:"api_key"`
	SessionCookie            *string            `json:"session_cookie"`
	Username                 *string            `json:"username"`
	Enabled                  *bool              `json:"enabled"`
	MinScrapeIntervalMinutes *int               `json:"min_scrape_interval_minutes"`
	MaxScrapesPerDay         *int               `json:"max_scrapes_per_day"`
	AutoInterval             *bool              `json:"auto_interval"`
	APIOnly                  *bool              `json:"api_only"`
	Targets                  *map[string]string `json:"targets"`
	TargetGroup              *string            `json:"target_group"`
	TargetDeadlines          *map[string]string `json:"target_deadlines"`
	MockScenario             *string            `json:"mock_scenario"`
	JoinDate                 *string            `json:"join_date"`
}

func createTracker(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p trackerPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if p.URL == nil || strings.TrimSpace(*p.URL) == "" {
			jsonError(w, "url is required", http.StatusBadRequest)
			return
		}
		// Respect tracker opt-outs: sites on the opt-out list have asked not
		// to be supported by this app and cannot be added.
		if entry, opted := d.Reg.OptOut(*p.URL); opted {
			jsonStatus(w, http.StatusForbidden, map[string]any{
				"error":   "tracker_opted_out",
				"opt_out": entry,
			})
			return
		}
		t := models.Tracker{
			ID:      newID(),
			URL:     strings.TrimRight(strings.TrimSpace(*p.URL), "/"),
			Enabled: true,
			Targets: map[string]string{},
		}
		// Default name/type from the def registry when the URL matches.
		if td, ok := d.Reg.TrackerByURL(t.URL); ok {
			t.Name = td.Name
			t.Type = td.Type
		}
		applyPayload(&t, p)
		if t.Type == "" {
			t.Type = "unit3d"
		}
		if t.Name == "" {
			t.Name = t.URL
		}
		clampTrackerScrape(d, &t)
		if err := d.Cfg.AddTracker(t); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		syncManualLayer(d, t)
		d.logInfof("tracker: added %s (%s, type %s)", t.Name, t.ID, t.Type)
		jsonStatus(w, http.StatusCreated, toView(d, t))
	}
}

func updateTracker(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var p trackerPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		err := d.Cfg.UpdateTracker(id, func(t *models.Tracker) {
			applyPayload(t, p)
			clampTrackerScrape(d, t)
		})
		if err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}
		t, _ := d.Cfg.Tracker(id)
		syncManualLayer(d, t)
		// A pending test result (tested unsaved credentials, see testTracker)
		// graduates to the official cached result only if what was just saved
		// matches what was tested — otherwise it's discarded as stale.
		promoteOrDiscardPendingTest(t)
		d.logInfof("tracker: updated %s (%s)", t.Name, t.ID)
		jsonOK(w, toView(d, t))
	}
}

// applyPayload merges a payload into a tracker. The masked API key sentinel
// means "unchanged"; an empty string means "clear it".
func applyPayload(t *models.Tracker, p trackerPayload) {
	if p.Name != nil {
		t.Name = strings.TrimSpace(*p.Name)
	}
	if p.URL != nil && strings.TrimSpace(*p.URL) != "" {
		t.URL = strings.TrimRight(strings.TrimSpace(*p.URL), "/")
	}
	if p.Type != nil && *p.Type != "" {
		t.Type = *p.Type
	}
	if p.APIKey != nil && *p.APIKey != maskedKey {
		t.APIKey = strings.TrimSpace(*p.APIKey)
	}
	if p.SessionCookie != nil && *p.SessionCookie != maskedKey {
		t.SessionCookie = strings.TrimSpace(*p.SessionCookie)
	}
	if p.Username != nil {
		t.Username = strings.TrimSpace(*p.Username)
	}
	if p.Enabled != nil {
		t.Enabled = *p.Enabled
	}
	if p.MinScrapeIntervalMinutes != nil {
		v := *p.MinScrapeIntervalMinutes
		if v < 0 {
			v = 0
		}
		t.MinScrapeIntervalMinutes = v
	}
	if p.MaxScrapesPerDay != nil {
		v := *p.MaxScrapesPerDay
		if v < 0 {
			v = 0
		}
		t.MaxScrapesPerDay = v
	}
	if p.AutoInterval != nil {
		t.AutoInterval = *p.AutoInterval
	}
	if p.APIOnly != nil {
		t.APIOnly = *p.APIOnly
	}
	if p.Targets != nil {
		t.Targets = *p.Targets
	}
	if p.TargetGroup != nil {
		t.TargetGroup = *p.TargetGroup
	}
	if p.TargetDeadlines != nil {
		t.TargetDeadlines = *p.TargetDeadlines
	}
	if p.MockScenario != nil {
		t.MockScenario = *p.MockScenario
	}
	if p.JoinDate != nil {
		t.JoinDate = strings.TrimSpace(*p.JoinDate)
	}
	sanitizeTargetDeadlines(t)
}

// sanitizeTargetDeadlines drops a deadline whose target field has no value
// (removed target, stale entry from an earlier edit) and any "days" (account
// age) entry that arrives despite the editors never offering one — reaching
// an age by a date isn't something the user controls. Runs after every
// payload apply so a direct API call can't smuggle either past the UI.
func sanitizeTargetDeadlines(t *models.Tracker) {
	for key := range t.TargetDeadlines {
		if key == "days" || strings.TrimSpace(t.Targets[key]) == "" {
			delete(t.TargetDeadlines, key)
		}
	}
}

// clampTrackerScrape is a backstop for the per-tracker min interval: a non-zero
// user value can never be stored below the effective floor (max of the 60-min
// hard floor and the def operator's requested minimum). The frontend also
// blocks this, but a direct API call must not be able to undercut it.
func clampTrackerScrape(d *Deps, t *models.Tracker) {
	if t.MinScrapeIntervalMinutes <= 0 {
		return
	}
	floor := scrape.HardFloorMinutes
	if rs := d.Reg.ResolveScrape(t.URL, t.Type); rs.MinIntervalMinutes > floor {
		floor = rs.MinIntervalMinutes
	}
	if t.MinScrapeIntervalMinutes < floor {
		t.MinScrapeIntervalMinutes = floor
	}
}

// syncManualLayer mirrors the tracker's user-entered values (currently just
// the join date) into the lowest-priority "manual" stats layer, so they fill
// gaps the API and scrape leave empty. Called after every create/update.
func syncManualLayer(d *Deps, t models.Tracker) {
	fields := map[string]any{}
	if jd := strings.TrimSpace(t.JoinDate); jd != "" {
		fields["join_date"] = jd
	}
	_ = d.Stats.SaveManual(t.ID, fields) // empty map clears the layer
}

func deleteTracker(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		name := ""
		if t, ok := d.Cfg.Tracker(id); ok {
			name = t.Name
		}
		if err := d.Cfg.DeleteTracker(id); err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}
		_ = d.DB.DeleteTracker(id)
		d.logInfof("tracker: removed %s (%s) — history and scrape log deleted", name, id)
		jsonOK(w, map[string]bool{"ok": true})
	}
}
