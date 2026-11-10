package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
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
		// same-site proves nothing on its own: Origin must still match.
		{"sec-fetch same-site, other port", map[string]string{
			"Sec-Fetch-Site": "same-site", "Origin": "http://yata.local:8080"}, true},
		{"sec-fetch same-site, sibling subdomain", map[string]string{
			"Sec-Fetch-Site": "same-site", "Origin": "http://other.yata.local:8420"}, true},
		{"sec-fetch same-site, matching origin", map[string]string{
			"Sec-Fetch-Site": "same-site", "Origin": "http://yata.local:8420"}, false},
		{"sec-fetch same-site, no origin", map[string]string{"Sec-Fetch-Site": "same-site"}, true},
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

// TestNoNetworkReachableReset: the account+data wipe that used to live at
// /api/auth/reset is gone for good. It was gated on a code printed to the log
// file, so anyone handed a log for debugging could erase the instance.
// Recovery is now the 2FA recovery codes, or the local -reset-auth flag.
func TestNoNetworkReachableReset(t *testing.T) {
	d := testDeps(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("hunter2hunter2hunter2"), bcrypt.DefaultCost)
	if err := d.DB.SetUser("admin", string(hash)); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(d)

	for _, path := range []string{"/api/auth/reset", "/api/auth/wipe"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{}`)))
		req.RemoteAddr = "203.0.113.7:4444"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: want 404 (route must not exist), got %d", path, rec.Code)
		}
	}
	if _, ok, _ := d.DB.GetUser(); !ok {
		t.Fatal("the account must still be there")
	}
}

// TestClientIPProxyHeaders: X-Forwarded-For is honoured only with
// trust_proxy_headers on, and only its rightmost entry.
func TestClientIPProxyHeaders(t *testing.T) {
	d := testDeps(t)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = "172.16.0.2:9999" // the proxy
	// The client sent "203.0.113.66"; the proxy appended the address it saw.
	r.Header.Set("X-Forwarded-For", "203.0.113.66, 198.51.100.4")

	if ip := clientIP(d, r); ip != "172.16.0.2" {
		t.Fatalf("trust off: want proxy address, got %q", ip)
	}
	s := d.Cfg.Settings()
	s.TrustProxyHeaders = true
	if err := d.Cfg.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	if ip := clientIP(d, r); ip != "198.51.100.4" {
		t.Fatalf("trust on: want the rightmost X-Forwarded-For entry, got %q", ip)
	}

	// Rotating the client-supplied part must not change the bucket.
	r2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r2.RemoteAddr = "172.16.0.2:9999"
	r2.Header.Set("X-Forwarded-For", "192.0.2.123, 198.51.100.4")
	if ip := clientIP(d, r2); ip != "198.51.100.4" {
		t.Fatalf("a spoofed leading entry changed the client address to %q", ip)
	}
	// A proxy that adds its own header line puts it after the client's.
	r3 := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r3.RemoteAddr = "172.16.0.2:9999"
	r3.Header.Add("X-Forwarded-For", "192.0.2.200")
	r3.Header.Add("X-Forwarded-For", "198.51.100.4")
	if ip := clientIP(d, r3); ip != "198.51.100.4" {
		t.Fatalf("multiple header lines: want the last line's entry, got %q", ip)
	}
	// A trailing empty entry falls back to the connection address.
	r4 := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r4.RemoteAddr = "172.16.0.2:9999"
	r4.Header.Set("X-Forwarded-For", "198.51.100.4, ")
	if ip := clientIP(d, r4); ip != "172.16.0.2" {
		t.Fatalf("empty rightmost entry: want the connection address, got %q", ip)
	}
	// Only a valid IP counts, keyed canonically.
	for _, tc := range []struct{ xff, want string }{
		{"not-an-ip", "172.16.0.2"},
		{strings.Repeat("a", 4096), "172.16.0.2"},
		{"2001:DB8:0:0::1", "2001:db8::1"},
		{"198.51.100.4:51234", "198.51.100.4"},
		{"[2001:db8::1]:443", "2001:db8::1"},
	} {
		rr := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		rr.RemoteAddr = "172.16.0.2:9999"
		rr.Header.Set("X-Forwarded-For", tc.xff)
		if ip := clientIP(d, rr); ip != tc.want {
			t.Fatalf("X-Forwarded-For %.40q: want %q, got %q", tc.xff, tc.want, ip)
		}
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
	recordLoginFailure(t, "192.0.2.1", now.Add(-2*time.Hour)) // stale, not locked
	recordLoginFailure(t, "192.0.2.2", now.Add(-10*time.Minute))
	// An attempt still being evaluated must never be evicted, however old.
	inFlight, _ := reserveAttempt("192.0.2.4", now.Add(-2*time.Hour))
	defer inFlight.release()
	// Trigger cleanup via a fresh failure from another IP.
	recordLoginFailure(t, "192.0.2.3", now)

	loginLimiter.mu.Lock()
	_, stale := loginLimiter.byIP["192.0.2.1"]
	_, recent := loginLimiter.byIP["192.0.2.2"]
	_, held := loginLimiter.byIP["192.0.2.4"]
	loginLimiter.mu.Unlock()
	if stale {
		t.Fatal("entry idle >1h should be evicted")
	}
	if !held {
		t.Fatal("an entry with an attempt in flight must survive cleanup")
	}
	if !recent {
		t.Fatal("recent entry must survive cleanup")
	}
	resetTestLimiter()
}
