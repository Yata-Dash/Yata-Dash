package api

import (
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Two-factor enrolment. The flow is deliberately two-stage — start, then
// enable — because a secret that has never round-tripped through the user's
// authenticator is a lockout waiting to happen: a mistyped manual entry, a
// phone whose clock is wrong, an app that failed to save. Nothing is switched
// on until a code generated from the secret has been checked.

// totpIssuer labels the entry in the authenticator app's list.
const totpIssuer = "Yata"

// requireSession is the shared guard for the 2FA handlers, which validate the
// session themselves rather than sitting behind the router middleware.
func requireSession(d *Deps, w http.ResponseWriter, r *http.Request) bool {
	if !isAuthenticated(d, r) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// confirmPassword re-checks the account password. Enabling, disabling or
// re-issuing recovery codes are all account-takeover-grade actions, so a
// borrowed session alone must not be enough to perform them.
func confirmPassword(d *Deps, w http.ResponseWriter, password string) (bool, bool) {
	u, ok, err := d.DB.GetUser()
	if err != nil || !ok {
		jsonError(w, "store_error", http.StatusInternalServerError)
		return false, false
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		jsonError(w, "invalid_credentials", http.StatusUnauthorized)
		return false, false
	}
	return true, u.TOTPEnabled
}

// totpStart generates a candidate secret and returns everything needed to
// enrol: the QR image, the otpauth URI behind it, and the grouped secret for
// anyone typing it in by hand. The secret is stored but stays inert until
// totpEnable succeeds.
func totpStart(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireSession(d, w, r) {
			return
		}
		c := decodeCreds(r)
		okPw, alreadyOn := confirmPassword(d, w, c.Password)
		if !okPw {
			return
		}
		if alreadyOn {
			jsonError(w, "totp_already_enabled", http.StatusConflict)
			return
		}
		u, _, err := d.DB.GetUser()
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		secret, err := newTOTPSecret()
		if err != nil {
			jsonError(w, "secret_error", http.StatusInternalServerError)
			return
		}
		if err := d.DB.SetTOTPSecret(secret, false); err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		uri := totpURI(totpIssuer, u.Username, secret)
		// A QR failure is not fatal — manual entry still works, so the panel
		// degrades to the typed secret rather than blocking enrolment.
		svg, err := qrSVG(uri)
		if err != nil {
			d.logWarnf("auth: could not render 2FA QR code: %v", err)
			svg = ""
		}
		jsonOK(w, map[string]any{
			"secret":         groupSecret(secret),
			"secret_compact": secret,
			"uri":            uri,
			"qr_svg":         svg,
		})
	}
}

// totpEnable switches 2FA on once the user proves the secret works, and issues
// the recovery codes. The codes are returned exactly once — only their hashes
// are kept — so the response is the user's single chance to save them.
func totpEnable(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireSession(d, w, r) {
			return
		}
		c := decodeCreds(r)
		u, ok, err := d.DB.GetUser()
		if err != nil || !ok {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		if u.TOTPEnabled {
			jsonError(w, "totp_already_enabled", http.StatusConflict)
			return
		}
		if u.TOTPSecret == "" {
			jsonError(w, "no_pending_secret", http.StatusBadRequest)
			return
		}
		now := time.Now()
		step, valid := verifyTOTP(u.TOTPSecret, strings.TrimSpace(c.Code), now, u.TOTPLastStep)
		if !valid {
			jsonStatus(w, http.StatusBadRequest, map[string]any{"error": "totp_invalid"})
			return
		}
		codes, hashes, err := newRecoveryCodes()
		if err != nil {
			jsonError(w, "secret_error", http.StatusInternalServerError)
			return
		}
		if err := d.DB.ReplaceRecoveryCodes(hashes, now); err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		if err := d.DB.SetTOTPSecret(u.TOTPSecret, true); err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		// The enrolment code counts as spent, so it can't immediately be
		// replayed against the login form.
		_ = d.DB.SetTOTPLastStep(step)
		d.logInfof("auth: two-factor authentication enabled for %q", u.Username)
		jsonOK(w, map[string]any{"ok": true, "recovery_codes": codes})
	}
}

// totpDisable turns 2FA off. It needs the password AND a current second
// factor: if a session alone could disable it, an attacker who got past the
// first factor would simply remove the second.
func totpDisable(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireSession(d, w, r) {
			return
		}
		c := decodeCreds(r)
		okPw, enabled := confirmPassword(d, w, c.Password)
		if !okPw {
			return
		}
		if !enabled {
			jsonError(w, "totp_not_enabled", http.StatusBadRequest)
			return
		}
		u, _, err := d.DB.GetUser()
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		valid, _, err := consumeSecondFactor(d, u, strings.TrimSpace(c.Code), time.Now())
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		if !valid {
			jsonStatus(w, http.StatusUnauthorized, map[string]any{"error": "totp_invalid"})
			return
		}
		if err := d.DB.DisableTOTP(); err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		d.logWarnf("auth: two-factor authentication disabled for %q", u.Username)
		jsonOK(w, map[string]any{"ok": true})
	}
}

// totpRegenerateRecovery issues a fresh set of codes, invalidating the old
// ones. For when the saved list is lost, or most of it has been spent.
func totpRegenerateRecovery(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireSession(d, w, r) {
			return
		}
		c := decodeCreds(r)
		okPw, enabled := confirmPassword(d, w, c.Password)
		if !okPw {
			return
		}
		if !enabled {
			jsonError(w, "totp_not_enabled", http.StatusBadRequest)
			return
		}
		codes, hashes, err := newRecoveryCodes()
		if err != nil {
			jsonError(w, "secret_error", http.StatusInternalServerError)
			return
		}
		if err := d.DB.ReplaceRecoveryCodes(hashes, time.Now()); err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		d.logInfof("auth: two-factor recovery codes regenerated")
		jsonOK(w, map[string]any{"ok": true, "recovery_codes": codes})
	}
}
