package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The config export is the one artifact Yata produces that is never safe to
// share: every tracker API key and session cookie, verbatim. Passwords below
// are invented.

const exportPassword = "correct horse battery staple"

// resetLoginLimiter clears the shared per-IP lockout between tests. Called by
// testDeps; see the note there.
func resetLoginLimiter() {
	loginLimiter.mu.Lock()
	loginLimiter.byIP = map[string]*attemptState{}
	loginLimiter.mu.Unlock()
}

func configureAccount(t *testing.T, router http.Handler) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(authCreds{Username: "someone", Password: exportPassword})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c
		}
	}
	t.Fatal("setup issued no session cookie")
	return nil
}

func postExport(t *testing.T, router http.Handler, cookie *http.Cookie, creds authCreds) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(creds)
	req := httptest.NewRequest(http.MethodPost, "/api/config/export", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// The original shape: a GET meant a top-level cross-site navigation could make
// the file download itself, because safe methods skip the cross-site check and
// a SameSite=Lax cookie still travels on a navigation.
func TestConfigExportRejectsGET(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	req := httptest.NewRequest(http.MethodGet, "/api/config/export", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("the config export is still reachable by GET")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestConfigExportNeedsThePasswordEvenWithASession(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)
	cookie := configureAccount(t, router)

	rec := postExport(t, router, cookie, authCreds{Password: "not the password"})

	if rec.Code == http.StatusOK {
		t.Fatal("a live session alone was enough to export every credential")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "api_key") {
		t.Error("the refusal response leaked config content")
	}
}

func TestConfigExportSucceedsWithThePassword(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)
	cookie := configureAccount(t, router)

	rec := postExport(t, router, cookie, authCreds{Password: exportPassword})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "yata-config.json") {
		t.Errorf("Content-Disposition = %q, want an attachment filename", cd)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var probe map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &probe); err != nil {
		t.Fatalf("body is not the config JSON: %v", err)
	}
	if _, ok := probe["settings"]; !ok {
		t.Error("exported file has no settings block — it is not a usable backup")
	}
}

func TestConfigExportRefusesWithoutASession(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)
	configureAccount(t, router)

	rec := postExport(t, router, nil, authCreds{Password: exportPassword})

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// Without this, the export is a password oracle with no rate limit at all,
// while /api/auth/login stops after five attempts.
func TestFailedExportAttemptsFeedTheLoginLockout(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)
	cookie := configureAccount(t, router)

	var last *httptest.ResponseRecorder
	for range maxLoginFailures {
		last = postExport(t, router, cookie, authCreds{Password: "wrong"})
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status after %d failures = %d, want 429", maxLoginFailures, last.Code)
	}

	// The lockout is shared, so login is now refused too — guessing against
	// the export must not be a way around the login limiter.
	body, _ := json.Marshal(authCreds{Username: "someone", Password: exportPassword})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("login status = %d, want 429 — the lockout is not shared", rec.Code)
	}
}

// With 2FA on, the password alone must not be enough — otherwise the account's
// strongest protection is absent from the action that hands over every
// credential it guards.
func TestConfigExportRequiresTheSecondFactorWhen2FAIsOn(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)
	cookie := configureAccount(t, router)
	secret, recoveryCodes, cookie := enrol2FA(t, d, router, exportPassword, cookie)

	// Password only.
	rec := postExport(t, router, cookie, authCreds{Password: exportPassword})
	if rec.Code == http.StatusOK {
		t.Fatal("exported with the password alone while 2FA was enabled")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}

	// Password plus a live code. Deliberately the NEXT step: enrolment just
	// spent the current one, and the replay guard refuses to accept a step
	// twice. The ±1 skew window makes the next step valid now.
	code, err := totpCode(secret, totpStep(time.Now())+1)
	if err != nil {
		t.Fatalf("totpCode: %v", err)
	}
	rec = postExport(t, router, cookie, authCreds{Password: exportPassword, Code: code})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a valid code (%s)", rec.Code, rec.Body.String())
	}

	// A recovery code is the documented way in when the authenticator is gone,
	// so it has to work here too.
	rec = postExport(t, router, cookie, authCreds{Password: exportPassword, Code: recoveryCodes[0]})
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 with a recovery code (%s)", rec.Code, rec.Body.String())
	}
}

// An instance with no account is open by design; there is no password to
// demand, and asking for one would block the legitimate user entirely.
func TestConfigExportWorksOnAnUnconfiguredInstance(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	rec := postExport(t, router, nil, authCreds{})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 on an unconfigured instance (%s)", rec.Code, rec.Body.String())
	}
}
