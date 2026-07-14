// Package api wires the HTTP server: routes, static file serving, and the
// JSON handlers. One file per route group.
package api

import (
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

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

// Deps bundles everything the handlers need.
type Deps struct {
	Cfg     *config.Manager
	DB      *store.DB
	Reg     *defs.Registry
	Fetch   *fetch.Client
	Stats   *stats.Engine
	Log     *logging.Logger
	Alerts  *notify.Engine
	Paths   *pathways.Data // nil = pathways feature hidden
	BaseDir string         // directory containing static/ and templates/
	// ResetCode gates /api/auth/reset (the destructive recovery wipe). It is
	// generated fresh each start and printed to the console + log, so a reset
	// proves console/filesystem access. Empty disables reset entirely.
	ResetCode string
}

// NewRouter builds the full application router.
func NewRouter(d *Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
	// No CORS headers: the SPA is served from the same origin as the API, so
	// it never makes a cross-origin request. Emitting "Access-Control-Allow-
	// Origin: *" would let any website the user visits read an unauthenticated
	// instance's responses (e.g. /api/config/export — API keys + cookies) when
	// it's reachable from the browser (localhost/LAN). Cross-origin reads stay
	// blocked by the browser's same-origin policy.

	r.Route("/api", func(api chi.Router) {
		// Request/path tracing — emitted at Trace so Debug stays readable;
		// set the level to "trace" in the Logs tab to see every call.
		if d.Log != nil {
			api.Use(requestLogger(d.Log))
		}
		// Reject state-changing requests browsers mark as cross-site — simple
		// POSTs skip CORS preflight, so this is the only thing stopping a
		// malicious web page from blindly POSTing at a reachable instance.
		api.Use(blockCrossSite(d))
		// Public — reachable without a session.
		api.Get("/version", func(w http.ResponseWriter, _ *http.Request) {
			jsonOK(w, map[string]string{"version": version.Version})
		})
		registerAuth(api, d)

		// Read-only integration surface — a login session OR a Yata API token
		// (Settings → Integrations) works here. Kept to endpoints that expose
		// no credentials and trigger no tracker traffic; see docs/API.md.
		api.Group(func(ro chi.Router) {
			ro.Use(requireAuthOrToken(d))
			ro.Get("/summary", getSummary(d))
			ro.Get("/history/series", getHistorySeries(d))
		})

		// Everything else requires a valid session once an account is
		// configured (basic auth to protect open ports).
		api.Group(func(pr chi.Router) {
			pr.Use(requireAuth(d))
			registerTokens(pr, d)
			registerTrackers(pr, d)
			registerStats(pr, d)
			registerScrape(pr, d)
			registerSettings(pr, d)
			registerDefs(pr, d)
			registerThemes(pr, d)
			registerHistory(pr, d)
			registerQUI(pr, d)
			registerProwlarr(pr, d)
			registerJackett(pr, d)
			registerPathways(pr, d)
			registerMock(pr, d)
			registerLogs(pr, d)
			registerConfigIO(pr, d)
			registerNotifications(pr, d)
			registerUpdates(pr, d)
		})
	})

	// Static assets + SPA shell.
	staticDir := filepath.Join(d.BaseDir, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.ServeFile(w, req, filepath.Join(d.BaseDir, "templates", "index.html"))
	})
	return r
}
