package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/logging"
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

// recordLoginFailure admits one attempt and fails it; returns the lockout
// duration if it locked the IP (else 0).
func recordLoginFailure(t *testing.T, ip string, now time.Time) time.Duration {
	t.Helper()
	a, wait := reserveAttempt(ip, now)
	if a == nil {
		t.Fatalf("attempt from %s refused (wait %s) when one was expected to be admitted", ip, wait)
	}
	return a.fail()
}

// loginLocked reports whether the IP is currently locked out, and for how long.
func loginLocked(ip string, now time.Time) (bool, time.Duration) {
	loginLimiter.mu.Lock()
	defer loginLimiter.mu.Unlock()
	s := loginLimiter.byIP[ip]
	if s != nil && now.Before(s.lockedUntil) {
		return true, s.lockedUntil.Sub(now)
	}
	return false, 0
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
		if d := recordLoginFailure(t, ip, now); d != 0 {
			t.Fatalf("failure %d should not lock yet", i+1)
		}
		if locked, _ := loginLocked(ip, now); locked {
			t.Fatalf("should not be locked after %d failures", i+1)
		}
	}
	// The threshold failure locks out.
	if d := recordLoginFailure(t, ip, now); d != lockoutDuration {
		t.Fatalf("expected lockout %s, got %s", lockoutDuration, d)
	}
	if locked, remain := loginLocked(ip, now); !locked || remain <= 0 {
		t.Fatalf("expected to be locked with remaining time, got locked=%v remain=%s", locked, remain)
	}
	// ...and a locked IP is refused before anything is evaluated.
	if a, wait := reserveAttempt(ip, now); a != nil || wait <= 0 {
		t.Fatalf("a locked IP must be refused with a wait, got admitted=%v wait=%s", a != nil, wait)
	}
	// A different IP is unaffected.
	if locked, _ := loginLocked("10.0.0.6", now); locked {
		t.Fatal("a different IP must not be locked")
	}
	// Lock expires after the duration.
	later := now.Add(lockoutDuration + time.Second)
	if locked, _ := loginLocked(ip, later); locked {
		t.Fatal("lock should expire")
	}
	// A successful login clears the IP's failure count.
	for i := 0; i < maxLoginFailures-1; i++ {
		recordLoginFailure(t, ip, later)
	}
	a, _ := reserveAttempt(ip, later)
	if a == nil {
		t.Fatal("the last attempt before the limit must be admitted")
	}
	a.succeed()
	a.release() // settling twice must be harmless
	if d := recordLoginFailure(t, ip, later); d != 0 {
		t.Fatal("a success should reset the failure count")
	}
}

// In-flight attempts count against the limit; release hands the slot back.
func TestLoginAttemptReservation(t *testing.T) {
	resetLoginLimiter()
	ip := "10.0.0.7"
	now := time.Now()

	held := make([]*loginAttempt, 0, maxLoginFailures)
	for i := 0; i < maxLoginFailures; i++ {
		a, _ := reserveAttempt(ip, now)
		if a == nil {
			t.Fatalf("attempt %d refused while under the limit", i+1)
		}
		held = append(held, a)
	}
	if a, wait := reserveAttempt(ip, now); a != nil || wait != attemptBusyRetry {
		t.Fatalf("with %d attempts in flight the next must wait %s, got admitted=%v wait=%s",
			maxLoginFailures, attemptBusyRetry, a != nil, wait)
	}
	held[0].release()
	if a, _ := reserveAttempt(ip, now); a == nil {
		t.Fatal("a released slot must be available again")
	} else {
		held[0] = a
	}
	for _, a := range held {
		a.fail()
	}
	if locked, _ := loginLocked(ip, now); !locked {
		t.Fatal("five failures settled from in-flight attempts must lock the IP")
	}
}

// Parallel wrong guesses: at most maxLoginFailures reach bcrypt.
func TestConcurrentWrongPasswordsAreCapped(t *testing.T) {
	d := testDeps(t)
	var out bytes.Buffer
	lg, err := logging.New("", logging.Trace, 1000, &out, 1<<20, 1)
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	d.Log = lg
	const pw = "correct horse battery"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	if err := d.DB.SetUser("someone", string(hash)); err != nil {
		t.Fatal(err)
	}

	const guesses = 60
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make(chan int, guesses)
	for i := 0; i < guesses; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, _ := json.Marshal(authCreds{Username: "someone", Password: fmt.Sprintf("wrong guess %d", i)})
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
			req.RemoteAddr = "203.0.113.50:5555"
			rec := httptest.NewRecorder()
			<-start
			authLogin(d)(rec, req)
			codes <- rec.Code
		}(i)
	}
	close(start)
	wg.Wait()
	close(codes)

	for c := range codes {
		if c == http.StatusOK {
			t.Fatal("a wrong password signed in")
		}
	}
	// Only guesses that reached bcrypt log a failed attempt.
	if evaluated := strings.Count(out.String(), "failed login attempt"); evaluated > maxLoginFailures {
		t.Fatalf("%d of %d parallel guesses were evaluated, limit is %d", evaluated, guesses, maxLoginFailures)
	}
	if locked, _ := loginLocked("203.0.113.50", time.Now()); !locked {
		t.Fatal("the IP should be locked out after the burst")
	}
}

// The unknown-username hash must cost the same, or timing reveals the username.
func TestDummyHashMatchesRealCost(t *testing.T) {
	cost, err := bcrypt.Cost(dummyHash())
	if err != nil {
		t.Fatal(err)
	}
	if cost != bcryptCost {
		t.Fatalf("dummy hash cost = %d, want bcryptCost (%d)", cost, bcryptCost)
	}
}
