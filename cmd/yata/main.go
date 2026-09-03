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
//	--allowed-hosts / YATA_ALLOWED_HOSTS
//	                       extra hostnames this instance answers to, comma
//	                       separated (default none). Access by IP address or
//	                       via localhost always works; a reverse proxy's
//	                       domain has to be named. "*" disables the check, and
//	                       can only be set here. Adds to (does not replace)
//	                       the list in Settings → Network, which is editable
//	                       from the dashboard and applies without a restart.
//	                       See internal/api/hostguard.go.
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

// splitHosts parses the comma-separated --allowed-hosts value, dropping blanks
// so a trailing comma or a stray space cannot become an empty hostname that
// matches nothing but still looks configured.
func splitHosts(s string) []string {
	var out []string
	for _, h := range strings.Split(s, ",") {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}

func main() {
	var (
		host         = flag.String("host", envOr("YATA_HOST", ""), "listen address (overrides config)")
		port         = flag.Int("port", atoi(envOr("YATA_PORT", "0")), "listen port (overrides config)")
		configPath   = flag.String("config", envOr("YATA_CONFIG", "config.json"), "path to config.json")
		defsDir      = flag.String("defs", envOr("YATA_DEFS", "defs"), "tracker definitions directory")
		dataPath     = flag.String("data", envOr("YATA_DATA", "yata.db"), "SQLite database path")
		baseDir      = flag.String("base", envOr("YATA_BASE", "."), "directory containing static/ and templates/")
		logPath      = flag.String("log", envOr("YATA_LOG", ""), "log file path (default: yata.log next to the database)")
		allowedHosts = flag.String("allowed-hosts", envOr("YATA_ALLOWED_HOSTS", ""),
			"comma-separated hostnames this instance answers to, beyond IP addresses and localhost "+
				"(e.g. a reverse proxy's domain). \"*\" disables the check.")
		resetAuth = flag.Bool("reset-auth", false, "remove the login account and exit (trackers, stats and settings are kept)")
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
		// Warnings loaded but lost something (a misspelled key); errors were
		// skipped entirely. Saying "skipped" for both would send anyone
		// chasing a warning looking for a def that isn't actually missing.
		if issue.Warning {
			log.Printf("defs: %s: %s", issue.File, issue.Error)
		} else {
			log.Printf("defs: skipped %s: %s", issue.File, issue.Error)
		}
	}
	log.Printf("defs: loaded %d tracker defs, %d types", len(reg.Trackers()), len(reg.Types()))

	db, err := store.Open(*dataPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer db.Close()

	// Tighten state written by an earlier version. New files are created
	// private, but an existing install's database, WAL sidecars and config
	// backups were world-readable, and those are the ones with a history of
	// credentials in them. Warn rather than fail: a filesystem without Unix
	// modes is a bad reason to refuse to boot (see package fsperm).
	if err := store.Harden(*dataPath); err != nil {
		logger.Warnf("store: could not make the database private: %v", err)
	}
	if err := cfg.HardenBackups(); err != nil {
		logger.Warnf("config: could not make existing backups private: %v", err)
	}

	// Last-resort recovery for someone locked out with no second factor and no
	// recovery codes. Running it requires a shell on the machine (or `docker
	// exec`), which is the point: there is deliberately no way to reach this
	// over the network. Unlike the reset it replaces, data is left untouched —
	// only the account and its sessions go.
	if *resetAuth {
		if _, ok, err := db.GetUser(); err != nil {
			log.Fatalf("reset-auth: %v", err)
		} else if !ok {
			log.Printf("reset-auth: no account configured — nothing to do")
			return
		}
		if err := db.DeleteUser(); err != nil {
			log.Fatalf("reset-auth: %v", err)
		}
		log.Printf("reset-auth: account removed — login protection is now off. " +
			"Open the dashboard and set it up again from Settings → General → Account.")
		return
	}

	statsEngine := stats.New(db)
	// Read the qui seedsize mode live so a settings change re-slots the qui
	// layer in the merge without a restart.
	statsEngine.QUISeedMode = func() string { return cfg.Settings().QUISeedsizeMode }
	// Resolve each tracker's inactivity policy from its def at read time, so a
	// def edit followed by a reload takes effect without a restart — and so a
	// tracker with no def (or no declared policy) simply reports none.
	statsEngine.AccountPolicy = func(trackerID string) stats.AccountPolicy {
		t, ok := cfg.Tracker(trackerID)
		if !ok {
			return stats.AccountPolicy{}
		}
		return stats.AccountPolicy{MaxLoginGapDays: reg.MaxLoginGapDays(t.URL)}
	}
	deps := &api.Deps{
		Cfg:     cfg,
		DB:      db,
		Reg:     reg,
		Fetch:   fetch.NewClient(reg, "test_data.json"),
		Stats:   statsEngine,
		Log:     logger,
		Alerts:  notify.New(cfg, logger),
		BaseDir: *baseDir,
		// Deployment-level only. Names added in Settings → Network are read
		// live per request (see allowedHostsFor), so they need no restart.
		AllowedHosts: splitHosts(*allowedHosts),
	}

	// Seed the manual stats layer from config (typed-in stats and join dates)
	// so a manual-entry tracker has its numbers, and account-age works, on
	// first load — before any fetch, and without one for trackers that have no
	// API to fetch from.
	for _, t := range cfg.Trackers() {
		_ = statsEngine.SaveManual(t.ID, t.ManualLayer())
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
