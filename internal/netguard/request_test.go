package netguard

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ── JoinPath ────────────────────────────────────────────────────────────────

// A def path is appended to the tracker URL by string concatenation, so a path
// that does not start with "/" can rewrite the authority — and the request,
// with the key attached, goes to a host the user never typed.
func TestJoinPathRefusesPathsThatChangeTheHost(t *testing.T) {
	for _, path := range []string{
		"@evil.example/api/user",      // userinfo: host becomes evil.example
		".evil.example/api/user",      // host becomes tracker.example.evil.example
		":8443/api/user",              // a different port is a different service
		"evil/api",                    // host becomes tracker.exampleevil
		"%2F%2Fevil.example/api/user", // not a valid host
	} {
		got, err := JoinPath("https://tracker.example", path)
		if err == nil {
			t.Errorf("JoinPath(%q) = %q, want a refusal", path, got)
			continue
		}
		if !IsBlocked(err) {
			t.Errorf("JoinPath(%q) error %v is not an ErrBlocked", path, err)
		}
		if strings.Contains(err.Error(), "evil") {
			t.Errorf("JoinPath(%q) error %q repeats the path", path, err)
		}
	}
}

func TestJoinPathAcceptsOrdinaryPaths(t *testing.T) {
	for _, tc := range []struct{ base, path, want string }{
		{"https://tracker.example", "/api/user", "https://tracker.example/api/user"},
		{"https://tracker.example/", "/api/user", "https://tracker.example/api/user"},
		{"https://Tracker.Example:8443/", "/api.php?x=1", "https://Tracker.Example:8443/api.php?x=1"},
		{"http://192.168.1.5:8080", "/users/bob", "http://192.168.1.5:8080/users/bob"},
		{"https://tracker.example", "", "https://tracker.example"},
		// "//" in the PATH is still a path once a host precedes it.
		{"https://tracker.example", "//api", "https://tracker.example//api"},
	} {
		got, err := JoinPath(tc.base, tc.path)
		if err != nil {
			t.Errorf("JoinPath(%q, %q) = %v", tc.base, tc.path, err)
			continue
		}
		if got != tc.want {
			t.Errorf("JoinPath(%q, %q) = %q, want %q", tc.base, tc.path, got, tc.want)
		}
	}
}

func TestJoinPathRejectsABaseWithNoHost(t *testing.T) {
	if _, err := JoinPath("not a url", "/api"); err == nil {
		t.Error("JoinPath accepted a base URL with no host")
	}
}

// ── ReadLimited ─────────────────────────────────────────────────────────────

func TestReadLimitedReadsUpToTheCap(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 100)
	got, err := ReadLimited(bytes.NewReader(body), 100)
	if err != nil {
		t.Fatalf("a body exactly at the cap was refused: %v", err)
	}
	if len(got) != 100 {
		t.Errorf("read %d bytes, want 100", len(got))
	}
}

func TestReadLimitedRefusesAnOversizedBody(t *testing.T) {
	body := "secret-token " + strings.Repeat("x", 200)
	_, err := ReadLimited(strings.NewReader(body), 100)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "xxx") {
		t.Errorf("error %q carries body content", err)
	}
}

// An endless body must stop at the cap, not after the whole stream.
func TestReadLimitedStopsReadingAnEndlessBody(t *testing.T) {
	r := &countingReader{}
	if _, err := ReadLimited(r, 1<<10); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	if r.n > 1<<11 {
		t.Errorf("read %d bytes from an endless body with a 1 KiB cap", r.n)
	}
}

type countingReader struct{ n int }

func (r *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.n += len(p)
	return len(p), nil
}

// ── Credentials across redirects ────────────────────────────────────────────

var trackerLike = Policy{AllowPrivate: true, CredentialHeaders: []string{"X-API-Key"}, PinBodyOrigin: true}

// net/http strips Authorization and Cookie on a cross-domain redirect, but not
// a custom header — so without CredentialHeaders an X-API-Key follows a 302
// wherever it points.
func TestCredentialHeaderIsDroppedOnCrossOriginRedirect(t *testing.T) {
	var leaked string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/collect", http.StatusFound)
	}))
	defer hop.Close()

	req, _ := http.NewRequest(http.MethodGet, hop.URL, nil)
	req.Header.Set("X-API-Key", "sekrit")
	resp, err := Client(5*time.Second, trackerLike).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if leaked != "" {
		t.Errorf("the redirect target received X-API-Key %q", leaked)
	}
}

// The header must not come back on a later hop that returns to the original
// origin through a foreign one: that origin chose the final destination.
func TestCredentialHeaderStaysDroppedForTheRestOfTheChain(t *testing.T) {
	var gotOnReturn = "unset"
	mux := http.NewServeMux()
	origin := httptest.NewServer(mux)
	defer origin.Close()
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, origin.URL+"/back", http.StatusFound)
	}))
	defer foreign.Close()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL, http.StatusFound)
	})
	mux.HandleFunc("/back", func(w http.ResponseWriter, r *http.Request) {
		gotOnReturn = r.Header.Get("X-API-Key")
	})

	req, _ := http.NewRequest(http.MethodGet, origin.URL+"/start", nil)
	req.Header.Set("X-API-Key", "sekrit")
	resp, err := Client(5*time.Second, trackerLike).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if gotOnReturn != "" {
		t.Errorf("X-API-Key = %q after the chain left the origin, want it dropped", gotOnReturn)
	}
}

// Same-origin redirects keep the key: a tracker that moves its API behind a
// trailing slash must not suddenly fail authentication.
func TestCredentialHeaderSurvivesSameOriginRedirect(t *testing.T) {
	var got string
	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-API-Key")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api", nil)
	req.Header.Set("X-API-Key", "sekrit")
	resp, err := Client(5*time.Second, trackerLike).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if got != "sekrit" {
		t.Errorf("X-API-Key = %q on a same-origin redirect, want it kept", got)
	}
}

// A 307/308 makes Go resend the body, and a JSON-RPC tracker carries its key
// there. The redirect is refused outright: there is no header to strip.
func TestBodyIsNotReplayedToAnotherOrigin(t *testing.T) {
	var reached bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		io.Copy(io.Discard, r.Body) //nolint:errcheck
	}))
	defer target.Close()
	for _, code := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		reached = false
		hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/rpc", code)
		}))
		req, _ := http.NewRequest(http.MethodPost, hop.URL, strings.NewReader(`{"params":["sekrit"]}`))
		resp, err := Client(5*time.Second, trackerLike).Do(req)
		hop.Close()
		if err == nil {
			resp.Body.Close()
			t.Errorf("%d: followed a body-replaying redirect to another origin", code)
		} else if !IsBlocked(err) {
			t.Errorf("%d: error %v is not an ErrBlocked", code, err)
		}
		if reached {
			t.Errorf("%d: the request body reached the redirect target", code)
		}
	}
}

// The same 307 within one origin is ordinary and still followed.
func TestBodyReplayWithinTheOriginIsFollowed(t *testing.T) {
	var got string
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/old", strings.NewReader("payload"))
	resp, err := Client(5*time.Second, trackerLike).Do(req)
	if err != nil {
		t.Fatalf("a same-origin 307 was refused: %v", err)
	}
	resp.Body.Close()
	if got != "payload" {
		t.Errorf("body after same-origin 307 = %q, want payload", got)
	}
}

// A 302 turns a POST into a body-less GET, so it carries nothing to protect and
// is not refused — only the credential headers are dropped.
func TestBodylessRedirectToAnotherOriginIsFollowed(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer hop.Close()
	req, _ := http.NewRequest(http.MethodPost, hop.URL, strings.NewReader("payload"))
	resp, err := Client(5*time.Second, trackerLike).Do(req)
	if err != nil {
		t.Fatalf("a 302 (POST becomes GET) was refused: %v", err)
	}
	resp.Body.Close()
}

// The scheme-upgrade allowance, and its limits. Exercised on the redirect hook
// directly since httptest cannot serve http and https on one host and port pair.
func TestCredentialOriginAllowsOnlyAPlainHTTPSUpgrade(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		keep     bool
	}{
		{"https://t.example/a", "https://t.example/b", true},
		{"https://t.example/a", "https://T.EXAMPLE:443/b", true},
		{"http://t.example/a", "https://t.example/a", true},  // upgrade
		{"https://t.example/a", "http://t.example/a", false}, // downgrade
		{"http://t.example:8080/a", "https://t.example/", false},
		{"https://t.example/a", "https://api.t.example/a", false},
		{"https://t.example/a", "https://t.example.evil/a", false},
		{"https://t.example/a", "https://t.example:8443/a", false},
	} {
		first, _ := http.NewRequest(http.MethodGet, tc.from, nil)
		next, _ := http.NewRequest(http.MethodGet, tc.to, nil)
		next.Header.Set("X-API-Key", "sekrit")
		if err := checkRedirect(trackerLike)(next, []*http.Request{first}); err != nil {
			t.Fatalf("%s → %s: %v", tc.from, tc.to, err)
		}
		if kept := next.Header.Get("X-API-Key") != ""; kept != tc.keep {
			t.Errorf("%s → %s: key kept = %v, want %v", tc.from, tc.to, kept, tc.keep)
		}
	}
}

// A policy without CredentialHeaders keeps the old behaviour: other headers
// are not the hook's business.
func TestRedirectLeavesHeadersAloneWithoutCredentialHeaders(t *testing.T) {
	first, _ := http.NewRequest(http.MethodGet, "https://a.example/", nil)
	next := &http.Request{Method: http.MethodGet, URL: &url.URL{Scheme: "https", Host: "b.example"}, Header: http.Header{}}
	next.Header.Set("X-API-Key", "k")
	if err := checkRedirect(lan)(next, []*http.Request{first}); err != nil {
		t.Fatal(err)
	}
	if next.Header.Get("X-API-Key") != "k" {
		t.Error("a policy with no CredentialHeaders deleted a header")
	}
}
