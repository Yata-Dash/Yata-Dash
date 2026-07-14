// summary.go — GET /api/summary: the read-only integration snapshot for
// homelab dashboards (Homepage, Homarr, scripts). Serves ONLY stored data —
// polling this endpoint never contacts a tracker; the background refresh loop
// keeps the numbers fresh. Mounted on the token-or-session group (server.go);
// full reference in docs/API.md.
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/stats"
	"github.com/Yata-Dash/Yata-Dash/internal/version"
)

// summaryTracker is one tracker's one-liner. Sizes come twice: the display
// string as the tracker reports it ("1.40 TiB") for direct templating, and a
// numeric GiB value for math/thresholds (same conversion as history charts).
type summaryTracker struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Abbr    string `json:"abbr,omitempty"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	// Status: ok | error | disabled | opted_out | unknown ("unknown" = not
	// refreshed since the server started; numbers below are still the stored
	// last-known values).
	Status    string `json:"status"`
	ErrorKind string `json:"error_kind,omitempty"`

	Username string `json:"username,omitempty"`
	Group    string `json:"group,omitempty"`

	Uploaded      string   `json:"uploaded,omitempty"`
	Downloaded    string   `json:"downloaded,omitempty"`
	Buffer        string   `json:"buffer,omitempty"`
	SeedSize      string   `json:"seed_size,omitempty"`
	UploadedGiB   *float64 `json:"uploaded_gib,omitempty"`
	DownloadedGiB *float64 `json:"downloaded_gib,omitempty"`
	BufferGiB     *float64 `json:"buffer_gib,omitempty"`
	SeedSizeGiB   *float64 `json:"seed_size_gib,omitempty"`
	Ratio         *float64 `json:"ratio,omitempty"`
	Seeding       *float64 `json:"seeding,omitempty"`
	Leeching      *float64 `json:"leeching,omitempty"`
	BonusPoints   *float64 `json:"bonus_points,omitempty"`
	HitAndRuns    *float64 `json:"hit_and_runs,omitempty"`
	Warnings      *float64 `json:"warnings,omitempty"`

	UnreadMail          bool `json:"unread_mail"`
	UnreadNotifications bool `json:"unread_notifications"`

	// UpdatedAt is the newest stored stat's timestamp (unix seconds; 0 = no
	// data yet).
	UpdatedAt int64 `json:"updated_at"`
}

type summaryTotals struct {
	Trackers int `json:"trackers"`
	Enabled  int `json:"enabled"`
	OK       int `json:"ok"`
	Issues   int `json:"issues"` // enabled trackers whose status is error

	UploadedGiB   float64  `json:"uploaded_gib"`
	DownloadedGiB float64  `json:"downloaded_gib"`
	BufferGiB     float64  `json:"buffer_gib"`
	Ratio         *float64 `json:"ratio,omitempty"` // nil when nothing downloaded
}

// GET /api/summary
func getSummary(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		trackers := d.Cfg.Trackers()
		totals := summaryTotals{Trackers: len(trackers)}
		out := make([]summaryTracker, 0, len(trackers))

		for _, t := range trackers {
			st := summaryTracker{
				ID: t.ID, Name: t.Name, URL: t.URL, Enabled: t.Enabled,
				Username: t.Username,
			}
			if td, ok := d.Reg.TrackerByURL(t.URL); ok {
				st.Abbr = td.Abbr
				if st.Name == "" {
					st.Name = td.Name
				}
			}

			merged, err := d.Stats.Merged(t.ID)
			if err == nil {
				nums := stats.NumericSnapshot(merged)
				str := func(field string) string {
					if f, ok := merged[field]; ok {
						if s, ok := f.Value.(string); ok {
							return s
						}
						return fmt.Sprintf("%v", f.Value)
					}
					return ""
				}
				num := func(field string) *float64 {
					if v, ok := nums[field]; ok {
						n := v
						return &n
					}
					return nil
				}
				st.Group = str("group")
				if st.Username == "" {
					st.Username = str("username")
				}
				st.Uploaded, st.UploadedGiB = str("uploaded"), num("uploaded")
				st.Downloaded, st.DownloadedGiB = str("downloaded"), num("downloaded")
				st.Buffer, st.BufferGiB = str("buffer"), num("buffer")
				st.SeedSize, st.SeedSizeGiB = str("seed_size"), num("seed_size")
				st.Ratio = num("ratio")
				st.Seeding = num("seeding")
				st.Leeching = num("leeching")
				st.BonusPoints = num("bonus_points")
				st.HitAndRuns = num("hit_and_runs")
				st.Warnings = num("warnings")
				st.UnreadMail = str("unread_mail") == "true"
				st.UnreadNotifications = str("unread_notifications") == "true"
				for _, f := range merged {
					if f.UpdatedAt > st.UpdatedAt {
						st.UpdatedAt = f.UpdatedAt
					}
				}
			}

			st.Status, st.ErrorKind = trackerStatus(d, t.ID, t.Enabled, t.URL)
			if t.Enabled {
				totals.Enabled++
				switch st.Status {
				case "ok":
					totals.OK++
				case "error":
					totals.Issues++
				}
			}
			if st.UploadedGiB != nil {
				totals.UploadedGiB += *st.UploadedGiB
			}
			if st.DownloadedGiB != nil {
				totals.DownloadedGiB += *st.DownloadedGiB
			}
			if st.BufferGiB != nil {
				totals.BufferGiB += *st.BufferGiB
			}
			out = append(out, st)
		}

		if totals.DownloadedGiB > 0 {
			r := totals.UploadedGiB / totals.DownloadedGiB
			totals.Ratio = &r
		}

		jsonOK(w, map[string]any{
			"version":      version.Version,
			"generated_at": time.Now().Unix(),
			"totals":       totals,
			"trackers":     out,
		})
	}
}

// trackerStatus derives the health one-liner from the refresh loop's last
// outcome (lastFetchState — in-memory, so a fresh boot reports "unknown"
// until the first cycle touches the tracker).
func trackerStatus(d *Deps, trackerID string, enabled bool, url string) (status, errKind string) {
	if !enabled {
		return "disabled", ""
	}
	if _, opted := d.Reg.OptOut(url); opted {
		return "opted_out", ""
	}
	v, seen := lastFetchState.Load(trackerID)
	if !seen {
		return "unknown", ""
	}
	if kind, _ := v.(string); kind != "" {
		return "error", kind
	}
	return "ok", ""
}
