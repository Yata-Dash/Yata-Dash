package netguard

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

var lan = Policy{AllowPrivate: true}
var public = Policy{}

func TestValidateRejectsNonHTTPSchemes(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"gopher://example.invalid:70/_x",
		"ftp://example.invalid/x",
		"dict://example.invalid:2628/",
		"jar:http://example.invalid!/",
	} {
		if _, err := Validate(raw, lan); err == nil {
			t.Errorf("Validate(%q) accepted a non-HTTP scheme", raw)
		} else if !IsBlocked(err) {
			t.Errorf("Validate(%q) error %v is not an ErrBlocked", raw, err)
		}
	}
}

func TestValidateAcceptsOrdinaryURLs(t *testing.T) {
	for _, raw := range []string{
		"http://localhost:9696",
		"https://prowlarr.lan/api",
		"http://192.168.1.10:8080",
		"HTTPS://Tracker.Example/api/user",
	} {
		if _, err := Validate(raw, lan); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", raw, err)
		}
	}
}

func TestValidateRejectsEmptyAndHostless(t *testing.T) {
	for _, raw := range []string{"", "   ", "http://", "https:///path"} {
		if _, err := Validate(raw, lan); err == nil {
			t.Errorf("Validate(%q) accepted a URL with no host", raw)
		}
	}
}

// Cloud instance metadata hands credentials to anything that can reach it, and
// no integration Yata talks to is ever on a link-local address — so it stays
// blocked even for the callers allowed onto the LAN.
func TestLinkLocalIsBlockedEvenWhenPrivateIsAllowed(t *testing.T) {
	for _, addr := range []string{"169.254.169.254", "169.254.1.1", "fe80::1"} {
		if err := CheckIP(net.ParseIP(addr), lan); err == nil {
			t.Errorf("CheckIP(%s) with AllowPrivate allowed a link-local address", addr)
		}
	}
}

func TestAlwaysBlockedRegardlessOfPolicy(t *testing.T) {
	cases := []string{"0.0.0.0", "::", "224.0.0.1", "ff02::1"}
	for _, p := range []Policy{lan, public} {
		for _, addr := range cases {
			if err := CheckIP(net.ParseIP(addr), p); err == nil {
				t.Errorf("CheckIP(%s, %+v) allowed an always-blocked address", addr, p)
			}
		}
	}
}

func TestPrivateRangesFollowThePolicy(t *testing.T) {
	private := []string{"127.0.0.1", "::1", "10.0.0.5", "192.168.1.1", "172.16.0.1", "fd00::1", "100.64.0.1"}
	for _, addr := range private {
		if err := CheckIP(net.ParseIP(addr), lan); err != nil {
			t.Errorf("CheckIP(%s) with AllowPrivate = %v, want nil", addr, err)
		}
		if err := CheckIP(net.ParseIP(addr), public); err == nil {
			t.Errorf("CheckIP(%s) without AllowPrivate allowed a private address", addr)
		}
	}
}

func TestPublicAddressesAlwaysAllowed(t *testing.T) {
	for _, addr := range []string{"93.184.216.34", "1.1.1.1", "2606:4700::1111"} {
		for _, p := range []Policy{lan, public} {
			if err := CheckIP(net.ParseIP(addr), p); err != nil {
				t.Errorf("CheckIP(%s, %+v) = %v, want nil", addr, p, err)
			}
		}
	}
}

func TestSameOriginNormalisesDefaultPorts(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"https://host.example", "https://host.example:443", true},
		{"http://host.example", "http://host.example:80", true},
		{"http://HOST.example/x", "http://host.example/y", true},
		{"https://host.example", "http://host.example", false},
		{"https://host.example", "https://other.example", false},
		{"http://host.example:8080", "http://host.example:9090", false},
	}
	for _, c := range cases {
		if got := SameOriginStr(c.a, c.b); got != c.want {
			t.Errorf("SameOriginStr(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSameOriginFailsClosedOnGarbage(t *testing.T) {
	if SameOriginStr("://nonsense", "https://host.example") {
		t.Error("an unparseable URL matched an origin")
	}
}

// The dialer, not the URL parser, is what actually stops a connection — this
// drives a real client at a real listener to prove it.
func TestClientBlocksLoopbackUnderPublicPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := Client(5*time.Second, public)
	_, err := c.Get(srv.URL) //nolint:bodyclose
	if err == nil {
		t.Fatal("connected to loopback under a policy that forbids it")
	}
	if !IsBlocked(err) {
		t.Errorf("error %v is not an ErrBlocked", err)
	}
}

func TestClientAllowsLoopbackUnderLANPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := Client(5*time.Second, lan)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("LAN policy blocked a legitimate loopback destination: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

// The attack the LAN integrations need protecting from: the operator points
// Yata at a host they control, which answers 302 to somewhere inside the
// network. AllowPrivate would permit the second hop on its own.
func TestPinOriginRefusesCrossOriginRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer target.Close()
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/internal", http.StatusFound)
	}))
	defer hop.Close()

	c := Client(5*time.Second, Policy{AllowPrivate: true, PinOrigin: true})
	resp, err := c.Get(hop.URL) //nolint:bodyclose
	if err == nil {
		resp.Body.Close()
		t.Fatal("followed a redirect to a different origin")
	}
	if !IsBlocked(err) {
		t.Errorf("error %v is not an ErrBlocked", err)
	}
}

// Same-origin redirects are ordinary — Jackett's login flow relies on them.
func TestPinOriginAllowsSameOriginRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/end", http.StatusFound)
	})
	mux.HandleFunc("/end", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := Client(5*time.Second, Policy{AllowPrivate: true, PinOrigin: true})
	resp, err := c.Get(srv.URL + "/start")
	if err != nil {
		t.Fatalf("a same-origin redirect was refused: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// A redirect must not be able to reach an address the initial URL could not.
func TestRedirectToBlockedSchemeIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	defer srv.Close()

	c := Client(5*time.Second, lan)
	resp, err := c.Get(srv.URL) //nolint:bodyclose
	if err == nil {
		resp.Body.Close()
		t.Fatal("followed a redirect to a file:// URL")
	}
	if !IsBlocked(err) {
		t.Errorf("error %v is not an ErrBlocked", err)
	}
}

func TestWrapPreservesCookieJar(t *testing.T) {
	jar := &recordingJar{}
	wrapped := Wrap(&http.Client{Timeout: 3 * time.Second, Jar: jar}, lan)
	if wrapped.Jar != jar {
		t.Error("Wrap dropped the cookie jar; Jackett's login flow depends on it")
	}
	if wrapped.Timeout != 3*time.Second {
		t.Errorf("Wrap changed the timeout to %v", wrapped.Timeout)
	}
}

type recordingJar struct{ http.CookieJar }

func (j *recordingJar) SetCookies(*url.URL, []*http.Cookie) {}
func (j *recordingJar) Cookies(*url.URL) []*http.Cookie     { return nil }

func TestBlockedErrorsAreRecognisableThroughURLError(t *testing.T) {
	c := Client(2*time.Second, public)
	_, err := c.Get("http://127.0.0.1:1/") //nolint:bodyclose
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !IsBlocked(err) {
		t.Fatalf("IsBlocked lost the sentinel through *url.Error: %v", err)
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("error should say why: %v", err)
	}
}

// With a proxy configured the guarded dialer only ever sees the proxy's
// address, so the destination check has to happen before the request is handed
// over. Without this, setting HTTP_PROXY silently disabled the address policy —
// including the link-local block that keeps Yata off cloud metadata endpoints.

// proxyTo makes an already-wrapped client behave as if HTTP_PROXY were set,
// without touching the process environment (which other tests share).
func proxyTo(t *testing.T, c *http.Client, proxy string) {
	t.Helper()
	g, ok := c.Transport.(*proxyGuard)
	if !ok {
		t.Fatalf("Wrap no longer installs a proxyGuard, got %T", c.Transport)
	}
	u, err := url.Parse(proxy)
	if err != nil {
		t.Fatal(err)
	}
	g.base.Proxy = func(*http.Request) (*url.URL, error) { return u, nil }
}

func TestProxiedRequestStillChecksTheDestination(t *testing.T) {
	// lan policy: loopback and RFC1918 are permitted, link-local never is.
	c := Client(2*time.Second, lan)
	proxyTo(t, c, "http://127.0.0.1:9")

	_, err := c.Get("http://169.254.169.254/latest/meta-data/") //nolint:bodyclose
	if err == nil {
		t.Fatal("a proxy let a link-local destination through")
	}
	if !IsBlocked(err) {
		t.Fatalf("want a policy refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "link-local") {
		t.Errorf("error should say why: %v", err)
	}
}

func TestProxiedRequestAppliesThePrivatePolicy(t *testing.T) {
	c := Client(2*time.Second, public)
	proxyTo(t, c, "http://127.0.0.1:9")

	_, err := c.Get("http://10.1.2.3/") //nolint:bodyclose
	if err == nil || !IsBlocked(err) {
		t.Fatalf("a proxy let a private destination past the public policy: %v", err)
	}

	// The same destination is legitimate for the LAN integrations, proxy or
	// not — the check must not become a blanket refusal.
	lanClient := Client(2*time.Second, lan)
	proxyTo(t, lanClient, "http://127.0.0.1:9")
	_, err = lanClient.Get("http://10.1.2.3/") //nolint:bodyclose
	if err != nil && IsBlocked(err) {
		t.Errorf("the LAN policy refused a private destination: %v", err)
	}
}

// A name nothing can resolve must not become a refusal: a proxy is often there
// precisely because the local host cannot resolve the destination itself.
func TestUnresolvableHostIsLeftToTheProxy(t *testing.T) {
	if err := checkDestination(context.Background(), "no-such-host.invalid", public); err != nil {
		t.Errorf("resolution failure became a policy refusal: %v", err)
	}
}

func TestUnproxiedRequestsSkipThePreflight(t *testing.T) {
	// Nothing to assert about DNS here — the point is that the dialer, not the
	// pre-flight, is what refuses, and it still does.
	c := Client(2*time.Second, public)
	_, err := c.Get("http://127.0.0.1:1/") //nolint:bodyclose
	if !IsBlocked(err) {
		t.Fatalf("the direct path stopped enforcing the policy: %v", err)
	}
}

// IsPublicHost answers a disclosure question — may this destination's reply be
// shown to the user — so every uncertain answer has to come back false.
func TestIsPublicHostOnLiteralAddresses(t *testing.T) {
	cases := []struct {
		host string
		want bool
		why  string
	}{
		{"8.8.8.8", true, "an ordinary public address"},
		{"2606:4700:4700::1111", true, "public IPv6"},
		{"127.0.0.1", false, "loopback is what httptest and a LAN service look like"},
		{"10.1.2.3", false, "RFC1918"},
		{"192.168.0.10", false, "RFC1918"},
		{"100.64.0.1", false, "CGNAT / Tailscale"},
		{"169.254.169.254", false, "cloud instance metadata"},
		{"", false, "no host at all"},
		{"not an ip", false, "unresolvable, so not known to be public"},
	}
	for _, c := range cases {
		if got := IsPublicHost(context.Background(), c.host); got != c.want {
			t.Errorf("IsPublicHost(%q) = %v, want %v — %s", c.host, got, c.want, c.why)
		}
	}
}
