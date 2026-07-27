package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// authPost drives one auth endpoint through the router, carrying the session
// cookie from a previous response when there is one.
func authPost(t *testing.T, router http.Handler, path string, body authCreds, cookie *http.Cookie) (int, map[string]any, *http.Cookie) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.RemoteAddr = "203.0.113.20:5555"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	var got *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			got = c
		}
	}
	if got == nil {
		got = cookie
	}
	return rec.Code, out, got
}

// TestPasswordPolicy: the floor is twelve characters, and anything past
// bcrypt's 72-byte cutoff is refused rather than silently truncated.
func TestPasswordPolicy(t *testing.T) {
	resetTestLimiter()
	d := testDeps(t)
	router := NewRouter(d)

	short := strings.Repeat("a", minPasswordLen-1)
	if code, body, _ := authPost(t, router, "/api/auth/setup", authCreds{Username: "mystery", Password: short}, nil); code != http.StatusBadRequest || body["error"] != "password_too_short" {
		t.Fatalf("an %d-character password should be refused, got %d %v", len(short), code, body["error"])
	}
	long := strings.Repeat("a", maxPasswordLen+1)
	if code, body, _ := authPost(t, router, "/api/auth/setup", authCreds{Username: "mystery", Password: long}, nil); code != http.StatusBadRequest || body["error"] != "password_too_long" {
		t.Fatalf("a %d-character password should be refused (bcrypt truncates at %d), got %d %v",
			len(long), maxPasswordLen, code, body["error"])
	}
	ok := strings.Repeat("a", minPasswordLen)
	code, _, cookie := authPost(t, router, "/api/auth/setup", authCreds{Username: "mystery", Password: ok}, nil)
	if code != http.StatusOK {
		t.Fatalf("a %d-character password should be accepted, got %d", minPasswordLen, code)
	}
	if cookie == nil {
		t.Fatal("setup should issue a session")
	}
	// New hashes use the current cost.
	u, _, _ := d.DB.GetUser()
	if cost, err := bcrypt.Cost([]byte(u.PasswordHash)); err != nil || cost != bcryptCost {
		t.Fatalf("new hash cost = %d (err %v), want %d", cost, err, bcryptCost)
	}
	// The floor applies to changes too.
	if code, body, _ := authPost(t, router, "/api/auth/password",
		authCreds{Password: ok, NewPassword: short}, cookie); code != http.StatusBadRequest || body["error"] != "password_too_short" {
		t.Fatalf("change-password must enforce the floor, got %d %v", code, body["error"])
	}
}

// TestGrandfatheredPasswordIsFlagged: an account created before the floor rose
// keeps working, but is marked so the UI can nudge. The flag can only be set
// at login, because a bcrypt hash doesn't reveal the length behind it.
func TestGrandfatheredPasswordIsFlagged(t *testing.T) {
	resetTestLimiter()
	d := testDeps(t)
	router := NewRouter(d)

	// Simulate an old account: an eight-character password at the old cost.
	old := "hunter22"
	hash, _ := bcrypt.GenerateFromPassword([]byte(old), bcrypt.MinCost)
	if err := d.DB.SetUser("mystery", string(hash)); err != nil {
		t.Fatal(err)
	}
	if u, _, _ := d.DB.GetUser(); u.WeakPassword {
		t.Fatal("the flag should start clear — nothing is known until a login")
	}

	code, _, cookie := authPost(t, router, "/api/auth/login", authCreds{Username: "mystery", Password: old}, nil)
	if code != http.StatusOK {
		t.Fatalf("a grandfathered password must still sign in, got %d", code)
	}
	u, _, _ := d.DB.GetUser()
	if !u.WeakPassword {
		t.Fatal("a sub-floor password should be flagged after login")
	}
	// The stored hash is also brought up to the current cost while we hold
	// the plaintext.
	if cost, err := bcrypt.Cost([]byte(u.PasswordHash)); err != nil || cost != bcryptCost {
		t.Fatalf("hash cost after login = %d (err %v), want %d", cost, err, bcryptCost)
	}
	// ...and the upgraded hash still verifies the same password.
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(old)) != nil {
		t.Fatal("the upgraded hash must still accept the original password")
	}

	// Setting a compliant password clears the flag.
	strong := strings.Repeat("z", minPasswordLen)
	if code, _, _ := authPost(t, router, "/api/auth/password",
		authCreds{Password: old, NewPassword: strong}, cookie); code != http.StatusOK {
		t.Fatalf("change-password failed: %d", code)
	}
	if u, _, _ := d.DB.GetUser(); u.WeakPassword {
		t.Fatal("the flag must clear once the password meets the floor")
	}
}

// enrol2FA takes an account from "no 2FA" to "enabled", returning the secret
// and the issued recovery codes.
func enrol2FA(t *testing.T, d *Deps, router http.Handler, password string, cookie *http.Cookie) (string, []string, *http.Cookie) {
	t.Helper()
	code, body, cookie := authPost(t, router, "/api/auth/totp/start", authCreds{Password: password}, cookie)
	if code != http.StatusOK {
		t.Fatalf("totp/start: %d %v", code, body)
	}
	secret, _ := body["secret_compact"].(string)
	if secret == "" {
		t.Fatal("totp/start returned no secret")
	}
	if svg, _ := body["qr_svg"].(string); !strings.HasPrefix(svg, "<svg") {
		t.Fatal("totp/start should return a QR image")
	}
	// Nothing is on yet — the secret is inert until a code proves it works.
	if u, _, _ := d.DB.GetUser(); u.TOTPEnabled {
		t.Fatal("2FA must not be enabled before a code is verified")
	}

	good, err := totpCode(secret, totpStep(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	code, body, cookie = authPost(t, router, "/api/auth/totp/enable", authCreds{Code: good}, cookie)
	if code != http.StatusOK {
		t.Fatalf("totp/enable: %d %v", code, body)
	}
	raw, _ := body["recovery_codes"].([]any)
	codes := make([]string, 0, len(raw))
	for _, c := range raw {
		codes = append(codes, c.(string))
	}
	if len(codes) != recoveryCodeCount {
		t.Fatalf("got %d recovery codes, want %d", len(codes), recoveryCodeCount)
	}
	return secret, codes, cookie
}

// nextCode returns a code for the step after the current one. Enrolment (and
// every subsequent use) burns the step it consumed, so a test that reuses the
// same window is correctly rejected as a replay. The step ahead is inside the
// accepted skew, and is always greater than anything already spent.
func nextCode(t *testing.T, secret string) string {
	t.Helper()
	c, err := totpCode(secret, totpStep(time.Now())+1)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestTOTPEnrolmentAndLogin walks the whole flow: enrol, then sign in with a
// code, then with a recovery code, and confirm each guard along the way.
func TestTOTPEnrolmentAndLogin(t *testing.T) {
	resetTestLimiter()
	d := testDeps(t)
	router := NewRouter(d)
	pw := strings.Repeat("a", minPasswordLen)

	_, _, cookie := authPost(t, router, "/api/auth/setup", authCreds{Username: "mystery", Password: pw}, nil)

	// A wrong code must not enable 2FA.
	if _, _, c := authPost(t, router, "/api/auth/totp/start", authCreds{Password: pw}, cookie); c != nil {
		cookie = c
	}
	if code, body, _ := authPost(t, router, "/api/auth/totp/enable", authCreds{Code: "000000"}, cookie); code != http.StatusBadRequest || body["error"] != "totp_invalid" {
		t.Fatalf("enable with a wrong code: want 400 totp_invalid, got %d %v", code, body["error"])
	}
	if u, _, _ := d.DB.GetUser(); u.TOTPEnabled {
		t.Fatal("a failed verification must leave 2FA off")
	}

	secret, recovery, cookie := enrol2FA(t, d, router, pw, cookie)
	if u, _, _ := d.DB.GetUser(); !u.TOTPEnabled {
		t.Fatal("2FA should be on after a verified code")
	}

	// The password alone is no longer enough, and no session is issued.
	resetTestLimiter()
	code, body, noSession := authPost(t, router, "/api/auth/login", authCreds{Username: "mystery", Password: pw}, nil)
	if code != http.StatusUnauthorized || body["error"] != "totp_required" {
		t.Fatalf("password-only login: want 401 totp_required, got %d %v", code, body["error"])
	}
	if noSession != nil {
		t.Fatal("no session may be issued before the second factor")
	}
	// A wrong second factor fails.
	if code, body, _ := authPost(t, router, "/api/auth/login",
		authCreds{Username: "mystery", Password: pw, Code: "000000"}, nil); code != http.StatusUnauthorized || body["error"] != "totp_invalid" {
		t.Fatalf("wrong code: want 401 totp_invalid, got %d %v", code, body["error"])
	}

	// The right code signs in. Note it has to be the NEXT step: enabling 2FA
	// consumed the code that proved the secret worked.
	resetTestLimiter()
	good := nextCode(t, secret)
	code, _, session := authPost(t, router, "/api/auth/login",
		authCreds{Username: "mystery", Password: pw, Code: good}, nil)
	if code != http.StatusOK || session == nil {
		t.Fatalf("login with a valid code: got %d (session %v)", code, session != nil)
	}

	// The same code cannot be replayed, even though its window is still open.
	resetTestLimiter()
	if code, body, _ := authPost(t, router, "/api/auth/login",
		authCreds{Username: "mystery", Password: pw, Code: good}, nil); code != http.StatusUnauthorized {
		t.Fatalf("replayed code: want 401, got %d %v", code, body["error"])
	}

	// A recovery code works, exactly once.
	resetTestLimiter()
	code, _, session = authPost(t, router, "/api/auth/login",
		authCreds{Username: "mystery", Password: pw, Code: recovery[0]}, nil)
	if code != http.StatusOK || session == nil {
		t.Fatalf("recovery-code login: got %d", code)
	}
	if left, _ := d.DB.RecoveryCodesLeft(); left != recoveryCodeCount-1 {
		t.Fatalf("%d recovery codes left, want %d", left, recoveryCodeCount-1)
	}
	resetTestLimiter()
	if code, _, _ := authPost(t, router, "/api/auth/login",
		authCreds{Username: "mystery", Password: pw, Code: recovery[0]}, nil); code != http.StatusUnauthorized {
		t.Fatalf("a spent recovery code must not work twice, got %d", code)
	}
}

// TestTOTPDisableNeedsBothFactors: a stolen session must not be enough to
// strip the second factor off the account.
func TestTOTPDisableNeedsBothFactors(t *testing.T) {
	resetTestLimiter()
	d := testDeps(t)
	router := NewRouter(d)
	pw := strings.Repeat("a", minPasswordLen)
	_, _, cookie := authPost(t, router, "/api/auth/setup", authCreds{Username: "mystery", Password: pw}, nil)
	secret, _, cookie := enrol2FA(t, d, router, pw, cookie)

	// Session + password but no code.
	if code, _, _ := authPost(t, router, "/api/auth/totp/disable", authCreds{Password: pw}, cookie); code != http.StatusUnauthorized {
		t.Fatalf("disable without a code: want 401, got %d", code)
	}
	// Session + code but the wrong password.
	good := nextCode(t, secret)
	if code, _, _ := authPost(t, router, "/api/auth/totp/disable",
		authCreds{Password: "wrong-password", Code: good}, cookie); code != http.StatusUnauthorized {
		t.Fatalf("disable with a wrong password: want 401, got %d", code)
	}
	if u, _, _ := d.DB.GetUser(); !u.TOTPEnabled {
		t.Fatal("failed attempts must leave 2FA on")
	}
	// Both, correctly.
	if code, body, _ := authPost(t, router, "/api/auth/totp/disable",
		authCreds{Password: pw, Code: good}, cookie); code != http.StatusOK {
		t.Fatalf("disable with both factors: want 200, got %d %v", code, body)
	}
	u, _, _ := d.DB.GetUser()
	if u.TOTPEnabled || u.TOTPSecret != "" {
		t.Fatal("disabling must clear the secret, not just the flag")
	}
	if left, _ := d.DB.RecoveryCodesLeft(); left != 0 {
		t.Fatalf("disabling must discard recovery codes, %d left", left)
	}
}

// TestAuthStatusHidesAccountDetail: whether an instance has 2FA on is not
// something an unauthenticated caller should be able to read.
func TestAuthStatusHidesAccountDetail(t *testing.T) {
	resetTestLimiter()
	d := testDeps(t)
	router := NewRouter(d)
	pw := strings.Repeat("a", minPasswordLen)
	_, _, cookie := authPost(t, router, "/api/auth/setup", authCreds{Username: "mystery", Password: pw}, nil)
	enrol2FA(t, d, router, pw, cookie)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["configured"] != true {
		t.Fatal("configured must be visible — the login screen needs it")
	}
	for _, leak := range []string{"username", "totp_enabled", "password_weak", "recovery_codes_left"} {
		if _, present := body[leak]; present {
			t.Errorf("%q must not be exposed without a session", leak)
		}
	}
}
