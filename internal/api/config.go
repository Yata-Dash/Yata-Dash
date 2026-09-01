package api

import (
	"encoding/csv"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

func registerConfigIO(r chi.Router, d *Deps) {
	// POST, not GET. As a GET this was reachable by cross-site navigation:
	// safe methods skip the cross-site check and a SameSite=Lax cookie still
	// travels on a top-level navigation, so any page the user visited could
	// make their config.json download itself. The response is opaque to the
	// attacker, but a file full of credentials landing in Downloads on cue is
	// a workable first step for "your backup is ready, send it over".
	r.Post("/config/export", exportConfig(d))
	r.Post("/config/import", importConfig(d))
	r.Get("/backups", listBackups(d))
	r.Post("/backups", createBackup(d))
	r.Get("/history/export.csv", exportHistoryCSV(d))
}

// exportConfig sends config.json as a download. It is a BACKUP, and the only
// artifact Yata produces that is never safe to share: every tracker API key
// and session cookie is in it verbatim.
//
// The password is re-checked even though the caller already holds a session,
// because the two protect against different things. A session is enough for
// someone at a borrowed or unlocked machine; the password is not. This is the
// same bar disabling 2FA and changing the password already meet.
//
// It does not, and cannot, protect a user who has been talked into exporting
// deliberately — that is what the warning on the button is for.
func exportConfig(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !reauthenticate(d, w, r, "config export") {
			return
		}
		// Read rather than ServeFile: this is a POST, and ServeFile's
		// conditional/range handling assumes a GET. The config is small and
		// saveLocked writes it by atomic rename, so a concurrent save yields
		// either the whole old file or the whole new one, never a torn read.
		data, err := os.ReadFile(d.Cfg.Path())
		if err != nil {
			jsonError(w, "read_error", http.StatusInternalServerError)
			return
		}
		d.logInfof("config: exported (%d bytes) — the file contains every stored credential", len(data))
		w.Header().Set("Content-Disposition", `attachment; filename="yata-config.json"`)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	}
}

// importConfig replaces the entire config with the uploaded JSON (the current
// config is backed up first by the manager). Server host/port changes take
// effect on the next restart.
func importConfig(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(io.LimitReader(r.Body, 8<<20)) // 8 MB cap
		if err != nil {
			jsonError(w, "read_error", http.StatusBadRequest)
			return
		}
		if err := d.Cfg.Import(data); err != nil {
			d.logWarnf("config: import rejected — %v", err)
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Re-sync derived state from the new config (the manual stats layer:
		// typed-in stats and join dates).
		for _, t := range d.Cfg.Trackers() {
			_ = d.Stats.SaveManual(t.ID, t.ManualLayer())
		}
		d.logInfof("config: imported (%d trackers) — previous config backed up", len(d.Cfg.Trackers()))
		jsonOK(w, map[string]any{"ok": true})
	}
}

func listBackups(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		list, err := d.Cfg.ListBackups()
		if err != nil {
			jsonError(w, "list_error", http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]any{"backups": list, "dir": d.Cfg.BackupDir()})
	}
}

// createBackup makes an on-demand backup and prunes to the configured limit.
func createBackup(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		path, err := d.Cfg.Backup()
		if err != nil {
			jsonError(w, "backup_error", http.StatusInternalServerError)
			return
		}
		_ = d.Cfg.PruneBackups(d.Cfg.Settings().BackupKeep)
		d.logInfof("config: manual backup created (%s)", path)
		jsonOK(w, map[string]any{"ok": true})
	}
}

// csvFieldOrder is the preferred column order for the history export; any extra
// fields are appended alphabetically.
var csvFieldOrder = []string{
	"uploaded", "downloaded", "buffer", "ratio", "seed_size",
	"seeding", "leeching", "bonus_points", "avg_seed_time", "hit_and_runs",
}

// exportHistoryCSV writes the daily-rollup history for all trackers as a wide
// CSV (one row per tracker per day, one column per stat) for personal analysis.
func exportHistoryCSV(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		points, err := d.DB.AllDailySince(time.Now().AddDate(0, 0, -400))
		if err != nil {
			jsonError(w, "history_error", http.StatusInternalServerError)
			return
		}

		// tracker id → display name
		names := map[string]string{}
		for _, t := range d.Cfg.Trackers() {
			names[t.ID] = t.Name
		}

		// (tracker, day) → field → value; collect the set of fields seen.
		type key struct {
			id  string
			day int64
		}
		rowMap := map[key]map[string]float64{}
		fieldsSeen := map[string]bool{}
		for _, p := range points {
			k := key{p.TrackerID, p.RecordedAt}
			if rowMap[k] == nil {
				rowMap[k] = map[string]float64{}
			}
			rowMap[k][p.Field] = p.Value
			fieldsSeen[p.Field] = true
		}

		// Ordered field columns: preferred order first, then any extras.
		fields := []string{}
		for _, f := range csvFieldOrder {
			if fieldsSeen[f] {
				fields = append(fields, f)
				delete(fieldsSeen, f)
			}
		}
		extra := []string{}
		for f := range fieldsSeen {
			extra = append(extra, f)
		}
		sort.Strings(extra)
		fields = append(fields, extra...)

		// Stable row order: by day, then tracker name.
		keys := make([]key, 0, len(rowMap))
		for k := range rowMap {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].day != keys[j].day {
				return keys[i].day < keys[j].day
			}
			return names[keys[i].id] < names[keys[j].id]
		})

		w.Header().Set("Content-Disposition", `attachment; filename="yata-history.csv"`)
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		cw := csv.NewWriter(w)
		header := append([]string{"date", "tracker"}, fields...)
		_ = cw.Write(header)
		for _, k := range keys {
			rec := make([]string, 0, len(fields)+2)
			rec = append(rec, time.Unix(k.day, 0).UTC().Format("2006-01-02"))
			name := names[k.id]
			if name == "" {
				name = k.id
			}
			rec = append(rec, name)
			vals := rowMap[k]
			for _, f := range fields {
				if v, ok := vals[f]; ok {
					rec = append(rec, strconv.FormatFloat(v, 'f', -1, 64))
				} else {
					rec = append(rec, "")
				}
			}
			_ = cw.Write(rec)
		}
		cw.Flush()
	}
}
