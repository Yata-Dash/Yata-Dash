package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/store"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

// sessionCookie is the httpOnly cookie holding the opaque session token.
const sessionCookie = "yata_session"

// sessionTTL is how long a login stays valid.
const sessionTTL = 30 * 24 * time.Hour

// minPasswordLen is the minimum accepted password length. Length is the only
// rule: composition requirements (a digit, a symbol, a capital) reliably push
// people towards short predictable passwords that satisfy every class and
// resist nothing, so they are deliberately absent.
const minPasswordLen = 12

// maxPasswordLen exists because bcrypt silently ignores everything past 72
// bytes. Accepting a longer passphrase would mean quietly checking only its
// first 72 bytes while the user believes the rest counts.
const maxPasswordLen = 72

// bcryptCost is the work factor for new hashes. Hashes made at a lower cost
// are transparently upgraded on the next successful login, where the plaintext
// is briefly available.
const bcryptCost = 12

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
	// Self-guarded — these validate the session inside the handler.
	r.Post("/auth/logout", authLogout(d))
	r.Post("/auth/password", authChangePassword(d))
	r.Post("/auth/disable", authDisable(d))
	// Two-factor enrolment and management (all require a live session).
	r.Post("/auth/totp/start", totpStart(d))
	r.Post("/auth/totp/enable", totpEnable(d))
	r.Post("/auth/totp/disable", totpDisable(d))
	r.Post("/auth/totp/recovery", totpRegenerateRecovery(d))
}

// validatePassword applies the length policy, returning an error code for the
// response body or "" when acceptable.
func validatePassword(pw string) string {
	switch {
	case len(pw) < minPasswordLen:
		return "password_too_short"
	case len(pw) > maxPasswordLen:
		return "password_too_long"
	}
	return ""
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

// authState is what the account table says — including the case where it could
// not be read at all.
//
// Three states rather than a bool because "no account exists" and "the
// question could not be answered" demand opposite responses, and collapsing
// them picks the dangerous one. The open first-run instance is a deliberate
// feature, so a bool has to default to "open", and then any database error —
// a lock timeout under load is enough, given the single writer connection —
// silently unlocks every protected route.
type authState int

const (
	authUnconfigured authState = iota // no account: first run, nothing to protect
	authEnabled                       // an account exists: a session is required
	authUnknown                       // the account could not be read: assume protected
)

func authStateOf(d *Deps) authState {
	_, ok, err := d.DB.GetUser()
	switch {
	case err != nil:
		return authUnknown
	case ok:
		return authEnabled
	default:
		return authUnconfigured
	}
}

// requireAuth gates protected routes. When no account is configured the app is
// open (first-run / opt-in); once configured, a valid session cookie is
// required; and when the account can't be read at all, the route is closed.
func requireAuth(d *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch authStateOf(d) {
			case authUnconfigured:
				next.ServeHTTP(w, r) // nothing to protect yet
				return
			case authUnknown:
				// 503, not 401: the caller's credentials are not the problem,
				// and answering "unauthorized" sends someone to re-enter a
				// password that cannot be checked either. Login is broken too
				// in this state, so say so.
				d.logErrorf("auth: account lookup failed — refusing %s %s until the database is readable",
					r.Method, r.URL.Path)
				jsonError(w, "auth_unavailable", http.StatusServiceUnavailable)
				return
			}
			if !hasValidSession(d, r) {
				jsonError(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// hasValidSession reports whether the request carries a live session cookie.
// It asks nothing about whether an account exists.
func hasValidSession(d *Deps, r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	ok, err := d.DB.SessionValid(c.Value, time.Now())
	return err == nil && ok
}

// isAuthenticated reports whether the caller may act. When no account is
// configured it is vacuously true (nothing to protect); when the account
// cannot be read it is false, so handlers that guard themselves with it fail
// closed like the middleware does.
func isAuthenticated(d *Deps, r *http.Request) bool {
	switch authStateOf(d) {
	case authUnconfigured:
		return true
	case authUnknown:
		return false
	}
	return hasValidSession(d, r)
}

func authStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, configured, err := d.DB.GetUser()
		if err != nil {
			// The SPA boots off this endpoint. Reporting "not configured" here
			// would tell it the instance is open and send an unauthenticated
			// visitor to the setup screen.
			d.logErrorf("auth: status lookup failed — %v", err)
			jsonError(w, "auth_unavailable", http.StatusServiceUnavailable)
			return
		}
		authed := isAuthenticated(d, r)
		resp := map[string]any{
			"configured":    configured,
			"authenticated": authed,
		}
		// Everything below describes the account itself, so it is only exposed
		// to a caller already holding a session — whether an instance has 2FA
		// on is not something an anonymous visitor needs to learn.
		if configured && authed {
			resp["username"] = u.Username
			resp["totp_enabled"] = u.TOTPEnabled
			resp["password_weak"] = u.WeakPassword
			resp["min_password_len"] = minPasswordLen
			if u.TOTPEnabled {
				left, _ := d.DB.RecoveryCodesLeft()
				resp["recovery_codes_left"] = left
			}
		}
		jsonOK(w, resp)
	}
}

type authCreds struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	NewPassword string `json:"new_password"`
	// Code is the second factor: either a six-digit authenticator code or one
	// of the single-use recovery codes.
	Code string `json:"code"`
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
		// Explicitly "no account exists", never merely "not known to exist":
		// this is an unauthenticated endpoint that writes the account row, so
		// treating a failed lookup as "unconfigured" would let a database
		// error hand the account to whoever asks first.
		switch authStateOf(d) {
		case authEnabled:
			jsonError(w, "already_configured", http.StatusConflict)
			return
		case authUnknown:
			d.logErrorf("auth: account lookup failed — refusing setup until the database is readable")
			jsonError(w, "auth_unavailable", http.StatusServiceUnavailable)
			return
		}
		c := decodeCreds(r)
		if c.Username == "" {
			jsonError(w, "username_required", http.StatusBadRequest)
			return
		}
		if code := validatePassword(c.Password); code != "" {
			jsonError(w, code, http.StatusBadRequest)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcryptCost)
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
			// The submitted username is echoed only when it matches the
			// account. An unmatched value is whatever was typed into the
			// field, and the commonest way to get one is a password entered a
			// box too high — logging it verbatim would write the password to
			// a file meant for sharing. "unrecognised" loses nothing: there is
			// exactly one account, so which name was tried says little.
			who := "an unrecognised username"
			if userMatch {
				who = fmt.Sprintf("%q", u.Username)
			}
			d.logWarnf("auth: failed login attempt for %s from %s", who, ip)
			if locked > 0 {
				d.logWarnf("auth: %s locked out for %s after %d failures", ip, locked, maxLoginFailures)
				jsonStatus(w, http.StatusTooManyRequests, map[string]any{
					"error":       "locked",
					"retry_after": int(locked.Seconds()),
				})
				return
			}
			jsonError(w, "invalid_credentials", http.StatusUnauthorized)
			return
		}

		// Second factor. The password was right, so an absent code is the
		// normal first leg of the flow rather than a failure — only a WRONG
		// code counts towards the lockout.
		if u.TOTPEnabled {
			if strings.TrimSpace(c.Code) == "" {
				jsonStatus(w, http.StatusUnauthorized, map[string]any{"error": "totp_required"})
				return
			}
			okCode, usedRecovery, err := consumeSecondFactor(d, u, c.Code, now)
			if err != nil {
				jsonError(w, "store_error", http.StatusInternalServerError)
				return
			}
			if !okCode {
				locked := recordLoginFailure(ip, now)
				d.logWarnf("auth: failed two-factor attempt for %q from %s", u.Username, ip)
				if locked > 0 {
					jsonStatus(w, http.StatusTooManyRequests, map[string]any{
						"error": "locked", "retry_after": int(locked.Seconds()),
					})
					return
				}
				jsonStatus(w, http.StatusUnauthorized, map[string]any{"error": "totp_invalid"})
				return
			}
			if usedRecovery {
				left, _ := d.DB.RecoveryCodesLeft()
				d.logWarnf("auth: %q signed in with a recovery code (%d left)", u.Username, left)
			}
		}

		clearLoginFailures(ip)
		// The plaintext is in hand only here, so this is the one place the
		// stored hash can be brought up to the current cost and the account's
		// password measured against the current length floor.
		upgradeStoredHash(d, u, c.Password)
		_ = d.DB.SetWeakPassword(len(c.Password) < minPasswordLen)
		issueSession(d, w, r)
		d.logInfof("auth: %q logged in", u.Username)
		jsonOK(w, map[string]any{"ok": true, "username": u.Username})
	}
}

// consumeSecondFactor validates either an authenticator code or a single-use
// recovery code, spending it in the process. It reports which kind was used so
// the caller can warn about a dwindling recovery set.
func consumeSecondFactor(d *Deps, u store.User, code string, now time.Time) (ok, usedRecovery bool, err error) {
	if looksLikeRecoveryCode(code) {
		used, err := d.DB.UseRecoveryCode(hashRecoveryCode(code), now)
		return used, used, err
	}
	step, valid := verifyTOTP(u.TOTPSecret, code, now, u.TOTPLastStep)
	if !valid {
		return false, false, nil
	}
	// Burn the step so the same code can't be replayed within its window.
	return true, false, d.DB.SetTOTPLastStep(step)
}

// upgradeStoredHash re-hashes the password when the stored hash predates the
// current cost. Best-effort: a failure here leaves the working older hash in
// place, so the user is never locked out by a housekeeping step.
func upgradeStoredHash(d *Deps, u store.User, plaintext string) {
	cost, err := bcrypt.Cost([]byte(u.PasswordHash))
	if err != nil || cost >= bcryptCost {
		return
	}
	nh, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcryptCost)
	if err != nil {
		return
	}
	if err := d.DB.SetUser(u.Username, string(nh)); err != nil {
		d.logWarnf("auth: could not upgrade password hash cost: %v", err)
		return
	}
	d.logInfof("auth: password hash cost upgraded %d → %d", cost, bcryptCost)
}

// There is deliberately no network-reachable account reset. The previous one
// was gated on a code printed to the console AND the log file, which meant a
// log shared for debugging carried the ability to erase the instance. Recovery
// now runs through the 2FA recovery codes, or — for someone with no second
// factor at all — the `-reset-auth` flag on the binary, which requires access
// to the machine by construction and preserves all data.

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
		if code := validatePassword(c.NewPassword); code != "" {
			jsonError(w, code, http.StatusBadRequest)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(c.NewPassword), bcryptCost)
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

// reauthenticate re-proves the account holder is present, for an action a
// live session alone should not be enough to perform. Returns false having
// already written the response.
//
// Unlike confirmPassword (which guards account settings), this feeds the login
// lockout. Without that, an endpoint taking a password becomes an oracle with
// no rate limit at all while /api/auth/login stops after five tries — an
// attacker who has a session but not the password would simply guess here
// instead.
//
// On an instance with no account configured there is nothing to prove, and the
// caller is let through: the whole app is open in that state, so demanding a
// password that does not exist would only block the legitimate user.
func reauthenticate(d *Deps, w http.ResponseWriter, r *http.Request, what string) bool {
	if authStateOf(d) == authUnconfigured {
		return true
	}
	if !isAuthenticated(d, r) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	ip := clientIP(d, r)
	now := time.Now()
	if locked, remain := loginLocked(ip, now); locked {
		jsonStatus(w, http.StatusTooManyRequests, map[string]any{
			"error": "locked", "retry_after": int(remain.Seconds()) + 1,
		})
		return false
	}
	c := decodeCreds(r)
	u, ok, err := d.DB.GetUser()
	if err != nil || !ok {
		jsonError(w, "store_error", http.StatusInternalServerError)
		return false
	}
	// A failure here reports the lockout on the attempt that triggers it, the
	// same way login does — otherwise the two paths disagree about when the
	// limit bites and the UI has to explain both.
	fail := func(errCode string) bool {
		if locked := recordLoginFailure(ip, now); locked > 0 {
			d.logWarnf("auth: %s locked out %s for %s", what, ip, locked)
			jsonStatus(w, http.StatusTooManyRequests, map[string]any{
				"error": "locked", "retry_after": int(locked.Seconds()) + 1,
			})
			return false
		}
		jsonStatus(w, http.StatusUnauthorized, map[string]any{"error": errCode})
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(c.Password)) != nil {
		d.logWarnf("auth: %s refused — wrong password from %s", what, ip)
		return fail("invalid_credentials")
	}
	if u.TOTPEnabled {
		valid, _, err := consumeSecondFactor(d, u, strings.TrimSpace(c.Code), now)
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return false
		}
		if !valid {
			d.logWarnf("auth: %s refused — wrong second factor from %s", what, ip)
			return fail("totp_invalid")
		}
	}
	clearLoginFailures(ip)
	return true
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
