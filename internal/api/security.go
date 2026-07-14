// security.go — defense-in-depth HTTP middleware: response headers and
// cross-site request blocking for the browser-facing API, plus the recovery
// reset code.
package api

import (
	"crypto/rand"
	"encoding/hex"
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
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
	case "cross-site":
		return true
	case "same-origin", "same-site", "none": // none = user-initiated (address bar)
		return false
	}
	// Fallback: browsers have sent Origin on cross-origin writes for over a
	// decade — compare its host against the request host. "null" (sandboxed
	// iframe / opaque redirect) never matches and is blocked. No Origin at
	// all = not a browser write — allow.
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return true
	}
	return !strings.EqualFold(u.Host, r.Host)
}

// NewResetCode generates the recovery code required by /api/auth/reset,
// formatted like "3F2A-9C41". Regenerated every start; printed to the
// console and log so a reset proves console/filesystem access, not just
// network reach.
func NewResetCode() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	s := strings.ToUpper(hex.EncodeToString(b))
	return s[:4] + "-" + s[4:]
}

// normalizeResetCode makes code comparison forgiving: case-insensitive,
// dashes and spaces ignored.
func normalizeResetCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	return strings.ReplaceAll(s, " ", "")
}
