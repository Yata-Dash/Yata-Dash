// Yata — self-hosted private tracker stats dashboard.
//
// Configuration precedence: flags > environment variables > config.json.
//
//	--host / YATA_HOST     listen address          (default 0.0.0.0)
//	--port / YATA_PORT     listen port             (default 8420)
//	--config / YATA_CONFIG path to config.json     (default ./config.json)
//	--defs / YATA_DEFS     defs directory          (default ./defs)
//	--data / YATA_DATA     SQLite database path    (default ./yata.db)
//	--base / YATA_BASE     static/templates dir    (default .)
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/api"
	"github.com/Yata-Dash/Yata-Dash/internal/config"
	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/fetch"
	"github.com/Yata-Dash/Yata-Dash/internal/logging"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
	"github.com/Yata-Dash/Yata-Dash/internal/pathways"
	"github.com/Yata-Dash/Yata-Dash/internal/stats"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
	"github.com/Yata-Dash/Yata-Dash/internal/version"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	var (
		host       = flag.String("host", envOr("YATA_HOST", ""), "listen address (overrides config)")
		port       = flag.Int("port", atoi(envOr("YATA_PORT", "0")), "listen port (overrides config)")
		configPath = flag.String("config", envOr("YATA_CONFIG", "config.json"), "path to config.json")
		defsDir    = flag.String("defs", envOr("YATA_DEFS", "defs"), "tracker definitions directory")
		dataPath   = flag.String("data", envOr("YATA_DATA", "yata.db"), "SQLite database path")
		baseDir    = flag.String("base", envOr("YATA_BASE", "."), "directory containing static/ and templates/")
		logPath    = flag.String("log", envOr("YATA_LOG", ""), "log file path (default: yata.log next to the database)")
	)
	flag.Parse()

	cfg, err := config.Open(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Rolling logger: tees to stdout, a rotated file, and an in-memory buffer
	// (served by the Logs settings tab). Redirect the standard log package
	// through it so existing diagnostics are captured too.
	if *logPath == "" {
		*logPath = filepath.Join(filepath.Dir(*dataPath), "yata.log")
	}
	// Capture EVERYTHING (trace) always — the Logs tab filters what's shown,
	// but the file + buffer never drop entries. 4000-line in-memory buffer.
	logger, err := logging.New(*logPath, logging.Trace, 4000, os.Stdout, 5*1024*1024, 3)
	if err != nil {
		log.Fatalf("logging: %v", err)
	}
	defer logger.Close()
	log.SetFlags(0) // the logger adds its own timestamps
	log.SetOutput(logger)
	logger.Infof("Yata %s starting (log: %s)", version.Version, *logPath)

	reg, err := defs.Load(*defsDir)
	if err != nil {
		log.Fatalf("defs: %v", err)
	}
	for _, issue := range reg.Issues() {
		log.Printf("defs: skipped %s: %s", issue.File, issue.Error)
	}
	log.Printf("defs: loaded %d tracker defs, %d types", len(reg.Trackers()), len(reg.Types()))

	db, err := store.Open(*dataPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer db.Close()

	statsEngine := stats.New(db)
	// Read the qui seedsize mode live so a settings change re-slots the qui
	// layer in the merge without a restart.
	statsEngine.QUISeedMode = func() string { return cfg.Settings().QUISeedsizeMode }
	deps := &api.Deps{
		Cfg:       cfg,
		DB:        db,
		Reg:       reg,
		Fetch:     fetch.NewClient(reg, "test_data.json"),
		Stats:     statsEngine,
		Log:       logger,
		Alerts:    notify.New(cfg, logger),
		BaseDir:   *baseDir,
		ResetCode: api.NewResetCode(),
	}
	// The recovery code gates the login screen's destructive "reset login +
	// wipe data" — printing it here (console + log file) means a reset proves
	// access to the machine, not just to the port. New code every start.
	log.Printf("auth: recovery code %s — needed for the login screen's reset (wipes all data)", deps.ResetCode)
	logger.Infof("auth: recovery code %s — needed for the login screen's reset (wipes all data)", deps.ResetCode)

	// Seed the manual stats layer from config (user-entered join dates) so
	// account-age works on first load, even before any fetch.
	for _, t := range cfg.Trackers() {
		if jd := strings.TrimSpace(t.JoinDate); jd != "" {
			_ = statsEngine.SaveManual(t.ID, map[string]any{"join_date": jd})
		}
	}

	// Pathways data is optional — the feature hides itself when absent.
	if pd, err := pathways.Load(filepath.Join(*defsDir, "pathways", "routes.json")); err == nil {
		deps.Paths = pd
		log.Printf("pathways: %d routes loaded (source: %s, fetched %s)",
			len(pd.Routes), pd.Source.Name, pd.Source.Fetched)
	} else {
		log.Printf("pathways: no route data (%v) — view disabled", err)
	}

	// Housekeeping: fine-grained history (sparklines) kept 14 days; daily
	// rollups (long-range growth charts + trend rates) kept per the
	// history_daily_retention_days setting (default ~2 years, re-read each
	// pass so a settings change applies without a restart); scrape log 30 days.
	go func() {
		for {
			dailyDays := cfg.Settings().HistoryDailyRetentionDays
			if dailyDays <= 0 {
				dailyDays = 730
			}
			_ = db.PruneHistory(time.Now().UTC().Add(-14 * 24 * time.Hour))
			_ = db.PruneDaily(time.Now().UTC().Add(-time.Duration(dailyDays) * 24 * time.Hour))
			// Events (group-change + connection timeline) and the connection
			// rollups kept in step with the daily window, so the timeline and
			// uptime strip cover the same span the charts can show.
			_ = db.PruneEvents(time.Now().UTC().Add(-time.Duration(dailyDays) * 24 * time.Hour))
			_ = db.PruneConnectionDaily(time.Now().UTC().Add(-time.Duration(dailyDays) * 24 * time.Hour))
			_ = db.PruneScrapeLog(time.Now().UTC().Add(-30 * 24 * time.Hour))
			_ = db.PruneSessions(time.Now())
			time.Sleep(6 * time.Hour)
		}
	}()

	// Automatic config backups (opt-in). Checks hourly whether a backup is due
	// for the configured frequency, then prunes to the keep-limit.
	go func() {
		for {
			runScheduledBackup(cfg, logger)
			time.Sleep(time.Hour)
		}
	}()

	// Server-side refresh + alert evaluation loop. Keeps stats fresh and fires
	// alert webhooks even when no browser/homelab client is polling. The first
	// pass primes alert state silently (no notifications for already-true rules).
	go func() {
		time.Sleep(20 * time.Second) // let startup settle before the first fetch
		for {
			api.RunRefreshCycle(deps)
			// Cadence follows the user's setting (default 30 min, floor 15) and
			// is re-read each cycle so changes apply without a restart.
			iv := cfg.Settings().RefreshIntervalMinutes
			if iv <= 0 {
				iv = 30
			}
			if iv < api.RefreshFloorMinutes {
				iv = api.RefreshFloorMinutes
			}
			time.Sleep(time.Duration(iv) * time.Minute)
		}
	}()

	// Weekly digest scheduler — its own 5-minute tick, deliberately separate
	// from the refresh loop above: that cadence is user-variable, and tying
	// the digest to it would either delay delivery by up to that interval or
	// require reasoning about drift. RunDigestIfDue is a cheap no-op check
	// (digestDueAt) when the digest is disabled or not yet due.
	go func() {
		time.Sleep(20 * time.Second) // let startup settle before the first check
		for {
			api.RunDigestIfDue(deps)
			time.Sleep(5 * time.Minute)
		}
	}()

	// Opt-in daily update check (versions.json on the repo); off by default.
	api.StartUpdateChecker(deps)

	server := cfg.Server()
	if *host != "" {
		server.Host = *host
	}
	if *port != 0 {
		server.Port = *port
	}
	addr := fmt.Sprintf("%s:%d", server.Host, server.Port)
	log.Printf("Yata listening on http://%s", addr)

	// Security nudge: if there's no login configured AND we're listening on a
	// non-loopback address, anyone on the network can reach Yata with full
	// access. The UI shows a matching banner; this warns headless operators.
	if _, hasUser, _ := db.GetUser(); !hasUser && !isLoopbackHost(server.Host) {
		logger.Warnf("SECURITY: no login is configured and Yata is listening on %s — "+
			"anyone who can reach this address has full access. Set up a username/password "+
			"in Settings → General → Account, or bind to 127.0.0.1.", server.Host)
	}

	if err := http.ListenAndServe(addr, api.NewRouter(deps)); err != nil {
		log.Fatal(err)
	}
}

// isLoopbackHost reports whether the listen host is localhost-only.
func isLoopbackHost(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func atoi(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

// runScheduledBackup creates a config backup when one is due for the configured
// frequency (no-op when backups are disabled or the last one is recent enough).
func runScheduledBackup(cfg *config.Manager, logger *logging.Logger) {
	s := cfg.Settings()
	if !s.BackupEnabled {
		return
	}
	var interval time.Duration
	switch s.BackupFrequency {
	case "daily":
		interval = 24 * time.Hour
	case "monthly":
		interval = 30 * 24 * time.Hour
	default: // weekly
		interval = 7 * 24 * time.Hour
	}
	if last, ok := cfg.LastBackupTime(); ok && time.Since(last) < interval {
		return
	}
	path, err := cfg.Backup()
	if err != nil {
		logger.Errorf("backup: scheduled backup failed — %v", err)
		return
	}
	_ = cfg.PruneBackups(s.BackupKeep)
	logger.Infof("backup: scheduled %s backup created (%s)", s.BackupFrequency, path)
}
