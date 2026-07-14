// demoseed builds a complete DEMO instance for screenshots/docs: real tracker
// defs, a dummy user, and varied synthetic stats. It REPLACES the trackers,
// notifications, and relevant settings of the target config and RECREATES the
// database. Point it at a scratch config/db — never at your real instance.
//
//	go run ./tools/demoseed -config path\to\demo.json -db path\to\demo.db
//
// No credentials are written, so the app never contacts the real trackers
// (API fetches fail pre-flight with no_key; scrapes are blocked by no_cookie).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/stats"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

const user = "DemoUser"

type demo struct {
	t        models.Tracker
	api      map[string]any // API stat layer
	scr      map[string]any // scrape stat layer (shows provenance dots)
	upGiB    float64        // current uploaded, GiB (history baseline)
	upRate   float64        // GiB/day growth
	downGiB  float64        // current downloaded, GiB (buffer/ratio derive from up−down)
	downRate float64
	ssGiB    float64 // current seed size, GiB
	ssRate   float64
	bonus    float64
	bonRate  float64
	seeds    float64 // current seeding count
	seedRate float64 // seeds/day growth
}

func main() {
	cfgPath := flag.String("config", "", "config.json to REPLACE demo sections in")
	dbPath := flag.String("db", "", "SQLite database path (recreated)")
	bulk := flag.Int("bulk", 0, "pad to this many trackers with synthetic ones (overflow testing)")
	flag.Parse()
	if *cfgPath == "" || *dbPath == "" {
		log.Fatal("-config and -db are required")
	}

	now := time.Now()
	flEnds := now.Add(50 * time.Hour).Unix()

	demos := []demo{
		{
			t: models.Tracker{
				ID: "demoseedpool0001", Name: "seedpool", URL: "https://seedpool.org", Type: "unit3d",
				Enabled: true, Username: user, JoinDate: "2024-06-15", TargetGroup: "SuperPool",
				Targets: map[string]string{"ratio": "1", "days": "183", "seed_size": "1 TiB"},
			},
			api: map[string]any{
				"username": user, "group": "PowerPool", "uploaded": "8.60 TiB", "downloaded": "2.10 TiB",
				"buffer": "6.50 TiB", "ratio": 4.10, "seeding": 640, "leeching": 1, "hit_and_runs": 0,
				"bonus_points": "126400", "join_date": "2024-06-15",
			},
			scr: map[string]any{"seed_size": "870.00 GiB", "avg_seed_time": "3M 2W", "fl_tokens": "6", "warnings": "0",
				"unread_mail": "true", "unread_notifications": "false"},
			upGiB: 8806, upRate: 35, downGiB: 2150, downRate: 7, ssGiB: 870, ssRate: 11,
			bonus: 126400, bonRate: 3400, seeds: 640, seedRate: 1.4,
		},
		{
			t: models.Tracker{
				ID: "demoaither000001", Name: "Aither", URL: "https://aither.cc", Type: "unit3d",
				Enabled: true, Username: user, JoinDate: "2024-11-08", TargetGroup: "Helios",
				Targets: map[string]string{"ratio": "0.8", "days": "183", "avg_seed": "1728000", "total_uploads": "1"},
			},
			api: map[string]any{
				"username": user, "group": "Zeus", "uploaded": "12.40 TiB", "downloaded": "3.20 TiB",
				"buffer": "9.20 TiB", "ratio": 3.88, "seeding": 412, "leeching": 2, "hit_and_runs": 0,
				"bonus_points": "84250", "uploads_approved": "6", "join_date": "2024-11-08",
				"active_event": "Global Freeleech", "active_event_ends_at": flEnds,
			},
			scr: map[string]any{"seed_size": "6.90 TiB", "avg_seed_time": "2M 1W", "fl_tokens": "8", "warnings": "0",
				"unread_mail": "true", "unread_notifications": "true"},
			upGiB: 12697, upRate: 42, downGiB: 3277, downRate: 11, ssGiB: 7066, ssRate: 15,
			bonus: 84250, bonRate: 2900, seeds: 412, seedRate: 1.1,
		},
		{
			t: models.Tracker{
				ID: "demolst000000001", Name: "LST", URL: "https://lst.gg", Type: "unit3d",
				Enabled: true, Username: user, JoinDate: "2025-02-14", TargetGroup: "Dolphin",
				Targets: map[string]string{"total_uploads": "5", "ratio": "1", "avg_seed": "1728000", "days": "91"},
			},
			api: map[string]any{
				"username": user, "group": "Goldfish", "uploaded": "3.60 TiB", "downloaded": "1.10 TiB",
				"buffer": "2.50 TiB", "ratio": 3.27, "seeding": 188, "leeching": 1, "hit_and_runs": 0,
				"bonus_points": "15400", "join_date": "2025-02-14", "uploads_approved": "3",
			},
			scr:   map[string]any{"seed_size": "1.35 TiB", "avg_seed_time": "3W 2D", "fl_tokens": "3"},
			upGiB: 3686, upRate: 18, downGiB: 1126, downRate: 6, ssGiB: 1382, ssRate: 9,
			bonus: 15400, bonRate: 950, seeds: 188, seedRate: 0.6,
		},
		{
			t: models.Tracker{
				// Deliberately young account (~2 months) — exercises the History
				// view's short-data cases (range clamping, sparse charts).
				ID: "demoant000000001", Name: "Anthelion", URL: "https://anthelion.me", Type: "gazelle",
				Enabled: true, Username: user, JoinDate: "2026-05-11", TargetGroup: "Power User",
				Targets: map[string]string{"uploaded": "1 TiB", "ratio": "1", "bonus_points": "25000", "days": "30"},
			},
			api: map[string]any{
				"username": user, "group": "Member", "uploaded": "1.40 TiB", "downloaded": "900.00 GiB",
				"buffer": "533.60 GiB", "ratio": 1.59, "seeding": 96, "leeching": 0,
				"invites": "2", "snatched": "156", "join_date": "2026-05-11",
			},
			scr: map[string]any{
				"bonus_points": "31200", "uploads_approved": "2", "adoptions": "4",
				"fl_tokens": "12", "hit_and_runs": "0",
			},
			upGiB: 1433, upRate: 11, downGiB: 900, downRate: 5, ssGiB: 0, ssRate: 0,
			bonus: 31200, bonRate: 620, seeds: 96, seedRate: 0.4,
		},
		{
			t: models.Tracker{
				ID: "demozenith000001", Name: "Zenith", URL: "https://znth.cx", Type: "unit3d",
				Enabled: true, Username: user, JoinDate: "2025-05-02", TargetGroup: "Bulker",
				Targets: map[string]string{"ratio": "0.69", "days": "21", "avg_seed": "604800"},
			},
			api: map[string]any{
				"username": user, "group": "Seeker", "uploaded": "2.20 TiB", "downloaded": "800.00 GiB",
				"buffer": "1.42 TiB", "ratio": 2.82, "seeding": 143, "leeching": 3, "hit_and_runs": 0,
				"bonus_points": "9800", "join_date": "2025-05-02",
			},
			scr:   map[string]any{"seed_size": "3.10 TiB", "avg_seed_time": "1M 1W"},
			upGiB: 2252, upRate: 22, downGiB: 819, downRate: 8, ssGiB: 3174, ssRate: 20,
			bonus: 9800, bonRate: 410, seeds: 143, seedRate: 0.5,
		},
		{
			t: models.Tracker{
				ID: "demomam000000001", Name: "MyAnonamouse", URL: "https://www.myanonamouse.net", Type: "custom",
				Enabled: true, Username: user, JoinDate: "2023-09-01",
				Targets: map[string]string{"uploaded": "10 TiB", "ratio": "2"},
			},
			api: map[string]any{
				"username": user, "group": "Power User", "uploaded": "5.10 TiB", "downloaded": "1.20 TiB",
				"buffer": "3.90 TiB", "ratio": 4.25, "seeding": 350, "leeching": 0,
				"bonus_points": "152430", "join_date": "2023-09-01",
				"seed_size": "4.20 TiB",
			},
			upGiB: 5222, upRate: 25, downGiB: 1229, downRate: 4, ssGiB: 4300, ssRate: 18,
			bonus: 152430, bonRate: 4100, seeds: 350, seedRate: 0.8,
		},
	}

	// Overflow padding: clone a simple unit3d tracker N times with varied
	// numbers so every view can be stress-tested at scale.
	for i := len(demos); i < *bulk; i++ {
		n := i + 1
		up := 300 + float64((i*137)%1400) // 0.3–1.7 TiB, varied
		down := up / (2.5 + float64(i%4))
		demos = append(demos, demo{
			t: models.Tracker{
				ID: fmt.Sprintf("demobulk%08d", n), Name: fmt.Sprintf("Overflow %02d", n),
				URL: fmt.Sprintf("https://of%02d.example", n), Type: "unit3d",
				Enabled: true, Username: user, JoinDate: "2025-01-01",
			},
			api: map[string]any{
				"username": user, "group": "Member",
				"uploaded": sizeStr(up), "downloaded": sizeStr(down), "buffer": sizeStr(up - down),
				"ratio": up / down, "seeding": 40 + i%300, "leeching": i % 4, "hit_and_runs": 0,
				"bonus_points": fmt.Sprintf("%d", 2000+i*130), "join_date": "2025-01-01",
			},
			upGiB: up, upRate: 4 + float64(i%12), downGiB: down, downRate: 1 + float64(i%3),
			bonus: float64(2000 + i*130), bonRate: 60 + float64(i%40),
			seeds: float64(40 + i%300), seedRate: 0.2,
		})
	}

	// ── Config: keep server section, replace everything demo-relevant ──────
	cfg := models.Config{Server: models.ServerConfig{Host: "127.0.0.1", Port: 8420}}
	if raw, err := os.ReadFile(*cfgPath); err == nil {
		var existing models.Config
		if json.Unmarshal(raw, &existing) == nil && existing.Server.Port != 0 {
			cfg.Server = existing.Server
		}
	}
	cfg.Settings = models.DefaultSettings()
	cfg.Settings.ShowStatSources = true
	cfg.Settings.ProfileAutoSync = false
	// Enable a qui instance so the qBittorrent stat bar renders in the UI. The
	// demo has no reachable qui, so the bar shows its "not reachable" state at
	// runtime; the screenshot harness injects synthetic healthy values for the
	// capture (like the rest of the credential-less demo). Harmless otherwise.
	cfg.Settings.QUIEnabledInstances = []int{1}
	for _, dm := range demos {
		cfg.Trackers = append(cfg.Trackers, dm.t)
	}
	cfg.Notifications = models.NotificationConfig{
		Destinations: []models.NotifyDestination{
			{ID: "demodest00000001", Name: "Team Discord", Type: "discord",
				URL: "https://discord.com/api/webhooks/000000/DEMO", Enabled: false},
			{ID: "demodest00000002", Name: "Gotify", Type: "gotify",
				URL: "https://gotify.example", Token: "DEMO-TOKEN", Enabled: false},
		},
		Rules: []models.AlertRule{
			{ID: "demorule00000001", Name: "Low ratio guard", Enabled: true,
				TrackerMode: "include", Match: "all", CooldownMins: 360,
				Conditions:   []models.Condition{{Field: "ratio", Op: "lt", Value: "1.0"}},
				Destinations: []string{"demodest00000001"}},
			{ID: "demorule00000002", Name: "Freeleech spotter", Enabled: true,
				TrackerMode: "include", Match: "any", CooldownMins: 720,
				TrackerIDs:  []string{"demoaither000001", "demolst000000001"},
				Conditions:  []models.Condition{{Field: "freeleech_active", Op: "is_true"}}},
		},
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*cfgPath, out, 0o644); err != nil {
		log.Fatal(err)
	}

	// ── Database: recreate from scratch ─────────────────────────────────────
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(*dbPath + suffix)
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	eng := stats.New(db)

	for _, dm := range demos {
		if err := eng.SaveAPI(dm.t.ID, dm.api); err != nil {
			log.Fatal(err)
		}
		if len(dm.scr) > 0 {
			if err := eng.SaveScrape(dm.t.ID, dm.scr); err != nil {
				log.Fatal(err)
			}
		}
		// History: ~13 months of daily rollups (bounded by join date) + 48h of
		// fine points — long, organic curves for trend rates, target ETAs, and
		// the History view.
		seedHistory(db, dm, now)
	}
	seedEvents(db, now)
	fmt.Printf("demo instance ready: %d trackers → %s / %s\n", len(demos), *cfgPath, *dbPath)
}

// sizeStr formats a GiB value as a human size string for a stat layer.
func sizeStr(gib float64) string {
	if gib >= 1024 {
		return fmt.Sprintf("%.2f TiB", gib/1024)
	}
	return fmt.Sprintf("%.2f GiB", gib)
}

// seedEvents writes a few group-change events so the History timeline overlay
// has something to show (the credential-less demo can't generate them live).
func seedEvents(db *store.DB, now time.Time) {
	day := func(n int) time.Time { return now.Add(-time.Duration(n) * 24 * time.Hour) }
	add := func(id string, n int, detail string) {
		if err := db.AddEvent(id, day(n), "group_change", detail); err != nil {
			log.Fatal(err)
		}
	}
	// seedpool: a climb over the year (real seedpool group names → promotions).
	add("demoseedpool0001", 300, "User→Pool")
	add("demoseedpool0001", 150, "Pool→PowerPool")
	// Zenith: a demotion then recovery (real zenith group names).
	add("demozenith000001", 120, "Seeker→User")
	add("demozenith000001", 40, "User→Seeker")
}

// histDays caps how far back daily history goes (~13 months — enough to
// exercise the History view's 1y range); a tracker joined more recently
// starts at its join date.
const histDays = 400

// seedHistory writes the tracker's synthetic history: one daily rollup per
// day plus a fine point every 3 hours for the last 48h. Values interpolate
// from a floored start to the current stat with a light wobble, so charts
// look lived-in rather than ruler-drawn.
func seedHistory(db *store.DB, dm demo, now time.Time) {
	days := histDays
	if jd, err := time.Parse("2006-01-02", dm.t.JoinDate); err == nil {
		if d := int(now.Sub(jd).Hours() / 24); d < days {
			days = d
		}
	}
	if days < 10 {
		days = 10
	}
	span := float64(days)
	// Value ageDays ago: linear from start (rate-projected, floored at 3% of
	// current so long spans don't go negative) to the current value.
	back := func(cur, rate, ageDays float64) float64 {
		start := cur - span*rate
		if start < cur*0.03 {
			start = cur * 0.03
		}
		v := cur - (cur - start) * ageDays / span
		return v * (1 + 0.008*math.Sin(ageDays/4.5))
	}
	fields := func(ageDays float64) map[string]float64 {
		up := back(dm.upGiB, dm.upRate, ageDays)
		down := back(dm.downGiB, dm.downRate, ageDays)
		f := map[string]float64{
			"uploaded":     up,
			"downloaded":   down,
			"buffer":       up - down,
			"bonus_points": math.Round(back(dm.bonus, dm.bonRate, ageDays)),
			"seeding":      math.Round(back(dm.seeds, dm.seedRate, ageDays) * (1 + 0.04*math.Sin(ageDays/3))),
			"leeching":     math.Round(1.5 + 1.5*math.Sin(ageDays/7)),
			"hit_and_runs": 0,
		}
		if down > 0 {
			f["ratio"] = math.Round(up/down*100) / 100
		}
		if dm.ssGiB > 0 {
			f["seed_size"] = back(dm.ssGiB, dm.ssRate, ageDays)
		}
		return f
	}
	for i := days; i >= 0; i-- {
		at := now.Add(-time.Duration(i) * 24 * time.Hour)
		if err := db.RecordDaily(dm.t.ID, at, fields(float64(i))); err != nil {
			log.Fatal(err)
		}
	}
	// Fine history (48h sparklines + short History ranges): every 3 hours.
	for i := 16; i >= 0; i-- {
		at := now.Add(-time.Duration(i) * 3 * time.Hour)
		if err := db.AddHistory(dm.t.ID, at, fields(float64(i)/8)); err != nil {
			log.Fatal(err)
		}
	}
}
