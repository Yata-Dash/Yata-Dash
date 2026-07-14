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

func resetTestLimiter() {
	loginLimiter.mu.Lock()
	loginLimiter.byIP = map[string]*attemptState{}
	loginLimiter.mu.Unlock()
}

func TestCrossSiteDetection(t *testing.T) {
	mk := func(hdr map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://yata.local:8420/api/settings", nil)
		r.Host = "yata.local:8420"
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		name string
		hdr  map[string]string
		want bool
	}{
		{"sec-fetch cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, true},
		{"sec-fetch same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, false},
		{"sec-fetch same-site", map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{"sec-fetch none (address bar)", map[string]string{"Sec-Fetch-Site": "none"}, false},
		// Sec-Fetch-Site wins even when Origin looks fine (browser knows best).
		{"sec-fetch cross-site with matching origin", map[string]string{
			"Sec-Fetch-Site": "cross-site", "Origin": "http://yata.local:8420"}, true},
		{"origin matches host", map[string]string{"Origin": "http://yata.local:8420"}, false},
		{"origin other site", map[string]string{"Origin": "https://evil.example"}, true},
		{"origin null (sandboxed iframe)", map[string]string{"Origin": "null"}, true},
		{"no headers (curl/scripts)", nil, false},
	}
	for _, c := range cases {
		if got := crossSite(mk(c.hdr)); got != c.want {
			t.Errorf("%s: crossSite = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestBlockCrossSiteMiddleware: cross-site writes are rejected before the
// handler runs; cross-site reads and same-origin writes pass through.
func TestBlockCrossSiteMiddleware(t *testing.T) {
	d := testDeps(t)
	called := false
	h := blockCrossSite(d)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	post := httptest.NewRequest(http.MethodPost, "/api/auth/reset", nil)
	post.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post)
	if rec.Code != http.StatusForbidden || called {
		t.Fatalf("cross-site POST: want 403 and handler not called, got %d called=%v", rec.Code, called)
	}

	get := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	get.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK {
		t.Fatalf("cross-site GET must pass (reads are same-origin-policy protected), got %d", rec.Code)
	}

	same := httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	same.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, same)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin POST must pass, got %d", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for hdr, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rec.Header().Get(hdr); got != want {
			t.Errorf("%s = %q, want %q", hdr, got, want)
		}
	}
}

// TestAuthResetRequiresCode: the recovery wipe only proceeds with the code
// printed to the server console — network reach alone must never be enough.
func TestAuthResetRequiresCode(t *testing.T) {
	d := testDeps(t)
	d.ResetCode = NewResetCode()

	setupUser := func() {
		hash, _ := bcrypt.GenerateFromPassword([]byte("hunter2hunter2"), bcrypt.DefaultCost)
		if err := d.DB.SetUser("admin", string(hash)); err != nil {
			t.Fatal(err)
		}
	}
	reset := func(code string) int {
		resetTestLimiter()
		body, _ := json.Marshal(authCreds{ResetCode: code})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/reset", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.7:4444"
		rec := httptest.NewRecorder()
		authReset(d)(rec, req)
		return rec.Code
	}

	setupUser()
	if code := reset(""); code != http.StatusForbidden {
		t.Fatalf("reset without code: want 403, got %d", code)
	}
	if code := reset("WRONG-CODE"); code != http.StatusForbidden {
		t.Fatalf("reset with wrong code: want 403, got %d", code)
	}
	if _, ok, _ := d.DB.GetUser(); !ok {
		t.Fatal("failed resets must not delete the account")
	}
	// Correct code works, forgiving about case/dashes/spaces.
	if code := reset(" " + normalizeResetCode(d.ResetCode) + " "); code != http.StatusOK {
		t.Fatalf("reset with correct (normalized) code: want 200, got %d", code)
	}
	if _, ok, _ := d.DB.GetUser(); ok {
		t.Fatal("successful reset must delete the account")
	}

	// An empty server-side code disables reset outright, even with an empty match.
	setupUser()
	d.ResetCode = ""
	if code := reset(""); code != http.StatusForbidden {
		t.Fatalf("reset with empty server code must be disabled, got %d", code)
	}

	// Wrong codes count toward the shared lockout (no brute-forcing the code).
	d.ResetCode = NewResetCode()
	resetTestLimiter()
	ip, now := "203.0.113.8", time.Now()
	for i := 0; i < maxLoginFailures; i++ {
		recordLoginFailure(ip, now)
	}
	body, _ := json.Marshal(authCreds{ResetCode: d.ResetCode})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reset", bytes.NewReader(body))
	req.RemoteAddr = ip + ":4444"
	rec := httptest.NewRecorder()
	authReset(d)(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked-out IP must get 429 even with the right code, got %d", rec.Code)
	}
	resetTestLimiter()
}

// TestClientIPProxyHeaders: X-Forwarded-For is honored only when the
// trust_proxy_headers setting is on.
func TestClientIPProxyHeaders(t *testing.T) {
	d := testDeps(t)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = "172.16.0.2:9999" // the proxy
	r.Header.Set("X-Forwarded-For", "198.51.100.4, 172.16.0.2")

	if ip := clientIP(d, r); ip != "172.16.0.2" {
		t.Fatalf("trust off: want proxy address, got %q", ip)
	}
	s := d.Cfg.Settings()
	s.TrustProxyHeaders = true
	if err := d.Cfg.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	if ip := clientIP(d, r); ip != "198.51.100.4" {
		t.Fatalf("trust on: want first X-Forwarded-For hop, got %q", ip)
	}

	// X-Forwarded-Proto gates the Secure cookie flag the same way.
	r.Header.Set("X-Forwarded-Proto", "https")
	if !requestIsHTTPS(d, r) {
		t.Fatal("trust on + X-Forwarded-Proto https: cookie should be Secure")
	}
	s.TrustProxyHeaders = false
	_ = d.Cfg.UpdateSettings(s)
	if requestIsHTTPS(d, r) {
		t.Fatal("trust off: X-Forwarded-Proto must be ignored")
	}
}

// TestLimiterEviction: stale failure entries are evicted; recent and
// locked-out ones survive.
func TestLimiterEviction(t *testing.T) {
	resetTestLimiter()
	now := time.Now()
	recordLoginFailure("192.0.2.1", now.Add(-2*time.Hour)) // stale, not locked
	recordLoginFailure("192.0.2.2", now.Add(-10*time.Minute))
	// Trigger cleanup via a fresh failure from another IP.
	recordLoginFailure("192.0.2.3", now)

	loginLimiter.mu.Lock()
	_, stale := loginLimiter.byIP["192.0.2.1"]
	_, recent := loginLimiter.byIP["192.0.2.2"]
	loginLimiter.mu.Unlock()
	if stale {
		t.Fatal("entry idle >1h should be evicted")
	}
	if !recent {
		t.Fatal("recent entry must survive cleanup")
	}
	resetTestLimiter()
}
