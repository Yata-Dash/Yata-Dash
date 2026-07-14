package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// TestLoginUsernameCaseInsensitive: the username matches regardless of case (and
// surrounding whitespace), while the password stays exact.
func TestLoginUsernameCaseInsensitive(t *testing.T) {
	d := testDeps(t)
	resetLimiter := func() {
		loginLimiter.mu.Lock()
		loginLimiter.byIP = map[string]*attemptState{}
		loginLimiter.mu.Unlock()
	}

	const pw = "correct horse battery"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err := d.DB.SetUser("MixedCaseUser", string(hash)); err != nil {
		t.Fatal(err)
	}

	login := func(user, pass string) int {
		resetLimiter() // isolate each attempt from the shared brute-force limiter
		body, _ := json.Marshal(authCreds{Username: user, Password: pass})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.9:5555"
		rec := httptest.NewRecorder()
		authLogin(d)(rec, req)
		return rec.Code
	}

	// Correct password with the username in any case (and with stray whitespace)
	// must succeed.
	for _, u := range []string{"MixedCaseUser", "mixedcaseuser", "MIXEDCASEUSER", "  MixedCaseUser  "} {
		if code := login(u, pw); code != http.StatusOK {
			t.Errorf("login with username %q should succeed, got %d", u, code)
		}
	}
	// The password remains case/character exact.
	if code := login("mixedcaseuser", "Correct Horse Battery"); code != http.StatusUnauthorized {
		t.Errorf("wrong-case password must fail, got %d", code)
	}
	// A genuinely different username still fails.
	if code := login("someoneelse", pw); code != http.StatusUnauthorized {
		t.Errorf("different username must fail, got %d", code)
	}
}

func TestLoginLockout(t *testing.T) {
	// Isolate the shared limiter for this test.
	loginLimiter.mu.Lock()
	loginLimiter.byIP = map[string]*attemptState{}
	loginLimiter.mu.Unlock()

	ip := "10.0.0.5"
	now := time.Now()

	// First maxLoginFailures-1 failures must NOT lock.
	for i := 0; i < maxLoginFailures-1; i++ {
		if d := recordLoginFailure(ip, now); d != 0 {
			t.Fatalf("failure %d should not lock yet", i+1)
		}
		if locked, _ := loginLocked(ip, now); locked {
			t.Fatalf("should not be locked after %d failures", i+1)
		}
	}
	// The threshold failure locks out.
	if d := recordLoginFailure(ip, now); d != lockoutDuration {
		t.Fatalf("expected lockout %s, got %s", lockoutDuration, d)
	}
	if locked, remain := loginLocked(ip, now); !locked || remain <= 0 {
		t.Fatalf("expected to be locked with remaining time, got locked=%v remain=%s", locked, remain)
	}
	// A different IP is unaffected.
	if locked, _ := loginLocked("10.0.0.6", now); locked {
		t.Fatal("a different IP must not be locked")
	}
	// Lock expires after the duration.
	if locked, _ := loginLocked(ip, now.Add(lockoutDuration+time.Second)); locked {
		t.Fatal("lock should expire")
	}
	// A successful login clears the IP's state.
	recordLoginFailure(ip, now)
	clearLoginFailures(ip)
	if locked, _ := loginLocked(ip, now); locked {
		t.Fatal("clearLoginFailures should reset state")
	}
}
