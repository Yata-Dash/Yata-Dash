package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
)

func registerNotifications(r chi.Router, d *Deps) {
	r.Get("/notifications", getNotifications(d))
	r.Put("/notifications", putNotifications(d))
	r.Post("/notifications/test", testNotification(d))
	r.Post("/notifications/dryrun", dryRunNotification(d))
	r.Get("/notifications/export", exportNotifications(d))
	r.Get("/notifications/digest/preview", previewDigest(d))
	r.Post("/notifications/digest/send", sendDigestNow(d))
}

// exportNotifications streams the alert config (destinations + rules) as a
// SHARE-SAFE download: destination secrets (webhook URL, token, chat id) are
// stripped so the file can be posted publicly. Names, types, and all rules
// survive. Re-importing into the SAME instance keeps working — putNotifications
// backfills secrets for matching destination IDs — and anyone else who imports
// it just re-enters their own webhooks. Full-fidelity backup = config export.
func exportNotifications(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		n := d.Cfg.Notifications()
		for i := range n.Destinations {
			n.Destinations[i].URL = ""
			n.Destinations[i].Token = ""
			n.Destinations[i].ChatID = ""
		}
		w.Header().Set("Content-Disposition", `attachment; filename="yata-alerts.json"`)
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(n)
	}
}

// getNotifications returns the configured destinations and rules. Secrets are
// returned as-is (this is behind auth and the editor needs them) — keep the
// instance protected if it's exposed.
func getNotifications(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, d.Cfg.Notifications())
	}
}

// putNotifications replaces the whole notification config (destinations + rules).
func putNotifications(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var n models.NotificationConfig
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		// The editor only ever round-trips destinations/rules — it doesn't know
		// about seeded_default_rules, so a decode always leaves it false. Carry
		// the existing value forward: otherwise a user who clears every rule and
		// destination (both empty, exactly the seeding trigger) would get the
		// starter rules silently reinjected on the next server restart.
		existing := d.Cfg.Notifications()
		n.SeededDefaultRules = existing.SeededDefaultRules
		// Digest LastSentAt/LastReadyTargets are server-maintained (updated only
		// by config.Manager.UpdateDigestState after a send) — the editor sends
		// the schedule fields (enabled/weekday/hour/destinations) but never
		// these, so preserve them exactly like SeededDefaultRules above.
		n.Digest.LastSentAt = existing.Digest.LastSentAt
		n.Digest.LastReadyTargets = existing.Digest.LastReadyTargets
		// Backfill secrets for known destinations arriving with none — this is
		// how re-importing a share-safe export (URL/token/chat id stripped) into
		// the same instance keeps its webhooks working.
		existingDests := map[string]models.NotifyDestination{}
		for _, dst := range existing.Destinations {
			existingDests[dst.ID] = dst
		}
		for i := range n.Destinations {
			dst := &n.Destinations[i]
			if old, ok := existingDests[dst.ID]; ok &&
				dst.URL == "" && dst.Token == "" && dst.ChatID == "" {
				dst.URL, dst.Token, dst.ChatID = old.URL, old.Token, old.ChatID
			}
		}
		// Assign IDs to any new destinations/rules.
		for i := range n.Destinations {
			if n.Destinations[i].ID == "" {
				n.Destinations[i].ID = newToken()[:16]
			}
		}
		for i := range n.Rules {
			if n.Rules[i].ID == "" {
				n.Rules[i].ID = newToken()[:16]
			}
		}
		// Identify newly-created rules (ids not present before this save) so we
		// can announce any that already meet their conditions.
		oldIDs := map[string]bool{}
		for _, r := range d.Cfg.Notifications().Rules {
			oldIDs[r.ID] = true
		}

		if err := d.Cfg.UpdateNotifications(n); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.logInfof("notifications: saved (%d destinations, %d rules)", len(n.Destinations), len(n.Rules))

		if d.Alerts != nil {
			var newRules []models.AlertRule
			for _, r := range d.Cfg.Notifications().Rules {
				if !oldIDs[r.ID] {
					newRules = append(newRules, r)
				}
			}
			if len(newRules) > 0 {
				d.Alerts.Announce(newRules, d.Cfg.Trackers(), func(id string) models.MergedStats {
					m, _ := d.Stats.Merged(id)
					return m
				}, trendFnFor(d))
			}
		}
		jsonOK(w, d.Cfg.Notifications())
	}
}

// dryRunNotification evaluates a rule supplied in the body (so unsaved edits
// can be tested) against every in-scope enabled tracker's current merged
// stats. Nothing is sent and engine edge-trigger state is untouched — the
// response just reports which trackers would fire right now.
func dryRunNotification(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var rule models.AlertRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if len(rule.Conditions) == 0 {
			jsonError(w, "rule has no conditions", http.StatusBadRequest)
			return
		}
		results := notify.DryRun(rule, d.Cfg.Trackers(), func(id string) models.MergedStats {
			m, _ := d.Stats.Merged(id)
			return m
		}, trendFnFor(d))
		jsonOK(w, map[string]any{"results": results})
	}
}

// testNotification sends a test message to a destination supplied in the body
// (so the user can test before saving).
func testNotification(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var dest models.NotifyDestination
		if err := json.NewDecoder(r.Body).Decode(&dest); err != nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if err := notify.Send(dest, "Yata test notification",
			"If you can read this, your destination is working. 🎉"); err != nil {
			jsonStatus(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		jsonOK(w, map[string]any{"ok": true})
	}
}
