// security.go — defense-in-depth HTTP middleware: response headers and
// cross-site request blocking for the browser-facing API.
package api

import (
	"net/http"
	"net/url"
	"strings"
)

// securityHeaders sets defensive headers on every response. X-Frame-Options
// is SAMEORIGIN (not DENY): framing a logged-in Yata from another origin
// enables clickjacking, but same-origin embeds keep working.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// blockCrossSite rejects state-changing API requests that a browser marks as
// coming from another site. Simple cross-site POSTs skip CORS preflight, and
// SameSite=Lax drops the session cookie on them — which is exactly the
// "not logged in" state the recovery reset requires — so without this guard
// any web page the user visits could blindly POST to a reachable instance
// (worst case: /api/auth/reset wiping all data; on an unconfigured open
// instance, any settings/tracker mutation). Non-browser clients (curl,
// scripts, integrations) send neither Sec-Fetch-Site nor Origin and pass.
func blockCrossSite(d *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if crossSite(r) {
				d.logWarnf("security: blocked cross-site %s %s from %s (origin %q)",
					r.Method, r.URL.Path, clientIP(d, r), r.Header.Get("Origin"))
				jsonError(w, "cross_site_request_blocked", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// crossSite reports whether the request originated from another site.
func crossSite(r *http.Request) bool {
	// Modern browsers label every request; trust that first.
	sameSite := false
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
	case "cross-site":
		return true
	case "same-origin", "none": // none = user-initiated (address bar)
		return false
	case "same-site":
		// "Site" is the registrable domain and ignores the port, so another
		// app on the same host (yata.lan:8420 vs yata.lan:8080) or a sibling
		// subdomain is "same-site" — and the SameSite=Lax session cookie
		// travels with its POSTs. The SPA itself is always same-origin, so
		// same-site proves nothing; it gets the strict Origin check below.
		sameSite = true
	}
	// Fallback: browsers have sent Origin on cross-origin writes for over a
	// decade — compare its host against the request host. "null" (sandboxed
	// iframe / opaque redirect) never matches and is blocked. No Origin at
	// all = not a browser write — allow, unless the browser has already said
	// it came from another origin.
	origin := r.Header.Get("Origin")
	if origin == "" {
		return sameSite
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return true
	}
	return !strings.EqualFold(u.Host, r.Host)
}
