package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

// sessionCookie is the httpOnly cookie holding the opaque session token.
const sessionCookie = "yata_session"

// sessionTTL is how long a login stays valid.
const sessionTTL = 30 * 24 * time.Hour

// minPasswordLen is the minimum accepted password length.
const minPasswordLen = 8

// Login brute-force protection: after maxLoginFailures consecutive failures
// from one client IP, that IP is locked out for lockoutDuration.
const maxLoginFailures = 5
const lockoutDuration = 15 * time.Minute

// dummyHash is compared against when the submitted username doesn't match the
// account, keeping login response timing uniform (no username enumeration).
// The password below is never accepted — a mismatched username always fails.
var dummyHash = func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("yata-timing-equalizer"), bcrypt.DefaultCost)
	return h
}()

func registerAuth(r chi.Router, d *Deps) {
	// Public — needed before/without a session.
	r.Get("/auth/status", authStatus(d))
	r.Post("/auth/login", authLogin(d))
	r.Post("/auth/setup", authSetup(d))
	r.Post("/auth/reset", authReset(d)) // recovery: wipe data + account (not-logged-in only)
	// Self-guarded — these validate the session inside the handler.
	r.Post("/auth/logout", authLogout(d))
	r.Post("/auth/password", authChangePassword(d))
	r.Post("/auth/disable", authDisable(d))
}

// ── Login rate limiting (per client IP, in-memory) ───────────────────────────

type attemptState struct {
	failures    int
	lockedUntil time.Time
	lastSeen    time.Time
}

var loginLimiter = struct {
	mu   sync.Mutex
	byIP map[string]*attemptState
}{byIP: map[string]*attemptState{}}

// clientIP resolves the client address for rate limiting. When the
// trust_proxy_headers setting is on (reverse-proxy deployments), the first
// X-Forwarded-For hop is used — otherwise every proxied client would share
// the proxy's address and one attacker could lock everyone out. Off by
// default: the header is trivially spoofable when Yata is directly exposed.
func clientIP(d *Deps, r *http.Request) string {
	if d != nil && d.Cfg != nil && d.Cfg.Settings().TrustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
				return first
			}
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// requestIsHTTPS reports whether the client connection is HTTPS — directly,
// or via a trusted reverse proxy's X-Forwarded-Proto. Gates the session
// cookie's Secure flag.
func requestIsHTTPS(d *Deps, r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return d != nil && d.Cfg != nil && d.Cfg.Settings().TrustProxyHeaders &&
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// loginLocked reports whether the IP is currently locked out, and for how long.
func loginLocked(ip string, now time.Time) (bool, time.Duration) {
	loginLimiter.mu.Lock()
	defer loginLimiter.mu.Unlock()
	s := loginLimiter.byIP[ip]
	if s != nil && now.Before(s.lockedUntil) {
		return true, time.Until(s.lockedUntil)
	}
	return false, 0
}

// recordLoginFailure increments the IP's failure count and locks it out once the
// threshold is reached. Returns the lockout duration when it just locked (else 0).
func recordLoginFailure(ip string, now time.Time) time.Duration {
	loginLimiter.mu.Lock()
	defer loginLimiter.mu.Unlock()
	// Opportunistic cleanup: drop entries that aren't locked out and haven't
	// failed in over an hour (their failure count is stale anyway).
	for k, v := range loginLimiter.byIP {
		if now.After(v.lockedUntil) && now.Sub(v.lastSeen) > time.Hour {
			delete(loginLimiter.byIP, k)
		}
	}
	s := loginLimiter.byIP[ip]
	if s == nil {
		s = &attemptState{}
		loginLimiter.byIP[ip] = s
	}
	s.failures++
	s.lastSeen = now
	if s.failures >= maxLoginFailures {
		s.failures = 0
		s.lockedUntil = now.Add(lockoutDuration)
		return lockoutDuration
	}
	return 0
}

func clearLoginFailures(ip string) {
	loginLimiter.mu.Lock()
	delete(loginLimiter.byIP, ip)
	loginLimiter.mu.Unlock()
}

// requireAuth gates protected routes. When no account is configured the app is
// open (first-run / opt-in); once configured, a valid session cookie is required.
func requireAuth(d *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isAuthenticated(d, r) && authConfigured(d) {
				jsonError(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func authConfigured(d *Deps) bool {
	_, ok, err := d.DB.GetUser()
	return err == nil && ok
}

// isAuthenticated returns true when the request carries a valid session cookie.
// When no account is configured it is vacuously true (nothing to protect).
func isAuthenticated(d *Deps, r *http.Request) bool {
	if !authConfigured(d) {
		return true
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	ok, err := d.DB.SessionValid(c.Value, time.Now())
	return err == nil && ok
}

func authStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, configured, _ := d.DB.GetUser()
		resp := map[string]any{
			"configured":    configured,
			"authenticated": isAuthenticated(d, r),
		}
		if configured && isAuthenticated(d, r) {
			resp["username"] = u.Username
		}
		jsonOK(w, resp)
	}
}

type authCreds struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	NewPassword string `json:"new_password"`
	ResetCode   string `json:"reset_code"`
}

func decodeCreds(r *http.Request) authCreds {
	var c authCreds
	_ = json.NewDecoder(r.Body).Decode(&c)
	c.Username = strings.TrimSpace(c.Username)
	return c
}

// authSetup creates the first (and only) account. Allowed only when none exists.
func authSetup(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if authConfigured(d) {
			jsonError(w, "already_configured", http.StatusConflict)
			return
		}
		c := decodeCreds(r)
		if c.Username == "" {
			jsonError(w, "username_required", http.StatusBadRequest)
			return
		}
		if len(c.Password) < minPasswordLen {
			jsonError(w, "password_too_short", http.StatusBadRequest)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
		if err != nil {
			jsonError(w, "hash_error", http.StatusInternalServerError)
			return
		}
		if err := d.DB.SetUser(c.Username, string(hash)); err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		issueSession(d, w, r)
		d.logInfof("auth: account %q created — login protection enabled", c.Username)
		jsonOK(w, map[string]any{"ok": true, "username": c.Username})
	}
}

func authLogin(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(d, r)
		now := time.Now()
		if locked, remain := loginLocked(ip, now); locked {
			jsonStatus(w, http.StatusTooManyRequests, map[string]any{
				"error":       "locked",
				"retry_after": int(remain.Seconds()) + 1,
				"can_reset":   true,
			})
			return
		}
		c := decodeCreds(r)
		u, ok, err := d.DB.GetUser()
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		// Always run exactly one bcrypt compare — against a dummy hash when the
		// username doesn't match — so response timing can't confirm the username.
		// Usernames are matched case-insensitively (the password stays exact);
		// the stored username keeps its original case for display.
		hash := dummyHash
		userMatch := ok && strings.EqualFold(u.Username, c.Username)
		if userMatch {
			hash = []byte(u.PasswordHash)
		}
		if bcrypt.CompareHashAndPassword(hash, []byte(c.Password)) != nil || !userMatch {
			locked := recordLoginFailure(ip, now)
			d.logWarnf("auth: failed login attempt for %q from %s", c.Username, ip)
			if locked > 0 {
				d.logWarnf("auth: %s locked out for %s after %d failures", ip, locked, maxLoginFailures)
				jsonStatus(w, http.StatusTooManyRequests, map[string]any{
					"error":       "locked",
					"retry_after": int(locked.Seconds()),
					"can_reset":   true,
				})
				return
			}
			jsonError(w, "invalid_credentials", http.StatusUnauthorized)
			return
		}
		clearLoginFailures(ip)
		issueSession(d, w, r)
		d.logInfof("auth: %q logged in", u.Username)
		jsonOK(w, map[string]any{"ok": true, "username": u.Username})
	}
}

// authReset is the locked-out / forgotten-password recovery path. It wipes the
// account AND all config + stored data (a clean slate with no private info),
// and is allowed ONLY when the caller is NOT logged in — a logged-in user must
// use change-password (which keeps data). This is destructive by design.
func authReset(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authConfigured(d) {
			jsonError(w, "no_account", http.StatusBadRequest)
			return
		}
		// When configured, isAuthenticated == "has a valid session".
		if isAuthenticated(d, r) {
			jsonError(w, "logged_in", http.StatusForbidden) // use change-password instead
			return
		}
		// The wipe requires the recovery code printed to the server console/
		// log at startup — network reach alone must never be enough to erase
		// everything. Wrong codes count toward the login lockout so the code
		// can't be brute-forced.
		ip := clientIP(d, r)
		now := time.Now()
		if locked, remain := loginLocked(ip, now); locked {
			jsonStatus(w, http.StatusTooManyRequests, map[string]any{
				"error": "locked", "retry_after": int(remain.Seconds()) + 1,
			})
			return
		}
		c := decodeCreds(r)
		if d.ResetCode == "" || subtle.ConstantTimeCompare(
			[]byte(normalizeResetCode(c.ResetCode)), []byte(normalizeResetCode(d.ResetCode))) != 1 {
			lockedFor := recordLoginFailure(ip, now)
			d.logWarnf("auth: reset attempt with wrong recovery code from %s", ip)
			if lockedFor > 0 {
				jsonStatus(w, http.StatusTooManyRequests, map[string]any{
					"error": "locked", "retry_after": int(lockedFor.Seconds()),
				})
				return
			}
			jsonError(w, "bad_reset_code", http.StatusForbidden)
			return
		}
		_ = d.DB.DeleteUser() // account + all sessions
		_ = d.DB.WipeData()   // stats / history / scrape log
		_ = d.Cfg.Reset()     // trackers / settings / notifications → defaults (server kept)
		clearSessionCookie(d, w, r)
		loginLimiter.mu.Lock()
		loginLimiter.byIP = map[string]*attemptState{}
		loginLimiter.mu.Unlock()
		d.logWarnf("auth: login + all data reset via recovery from %s", ip)
		jsonOK(w, map[string]any{"ok": true})
	}
}

func authLogout(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil {
			_ = d.DB.DeleteSession(c.Value)
		}
		clearSessionCookie(d, w, r)
		jsonOK(w, map[string]any{"ok": true})
	}
}

func authChangePassword(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuthenticated(d, r) {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		c := decodeCreds(r)
		u, ok, err := d.DB.GetUser()
		if err != nil || !ok {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(c.Password)) != nil {
			jsonError(w, "invalid_credentials", http.StatusUnauthorized)
			return
		}
		if len(c.NewPassword) < minPasswordLen {
			jsonError(w, "password_too_short", http.StatusBadRequest)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(c.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			jsonError(w, "hash_error", http.StatusInternalServerError)
			return
		}
		if err := d.DB.SetUser(u.Username, string(hash)); err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		// Invalidate other sessions, then re-issue one for the caller.
		_ = d.DB.ClearSessions()
		issueSession(d, w, r)
		jsonOK(w, map[string]any{"ok": true})
	}
}

// authDisable removes the account entirely (turns protection off). Requires the
// current password as confirmation.
func authDisable(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuthenticated(d, r) {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		c := decodeCreds(r)
		u, ok, err := d.DB.GetUser()
		if err != nil || !ok {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(c.Password)) != nil {
			jsonError(w, "invalid_credentials", http.StatusUnauthorized)
			return
		}
		if err := d.DB.DeleteUser(); err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		clearSessionCookie(d, w, r)
		d.logInfof("auth: login protection disabled (account removed)")
		jsonOK(w, map[string]any{"ok": true})
	}
}

// issueSession generates a token, stores it, and sets the session cookie.
func issueSession(d *Deps, w http.ResponseWriter, r *http.Request) {
	token := newToken()
	if err := d.DB.CreateSession(token, time.Now().Add(sessionTTL)); err != nil {
		jsonError(w, "store_error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(d, r),
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func clearSessionCookie(d *Deps, w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(d, r),
		MaxAge:   -1,
	})
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
