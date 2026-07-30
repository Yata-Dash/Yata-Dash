// Package netguard constrains the server-side HTTP requests Yata makes on a
// user's behalf.
//
// Several endpoints take a destination from the caller — the Prowlarr,
// Jackett and QUI integrations, notification destinations, and the tracker
// fetchers, whose URL comes from a definition. Left unconstrained those are
// server-side request forgery: the caller chooses where Yata connects, and
// Yata connects from inside the network with credentials attached.
//
// The awkward part is that most of them are SUPPOSED to reach private
// addresses. Prowlarr, Jackett, QUI and a self-hosted Gotify are normally on
// localhost or the LAN — that is the ordinary deployment, not an attack. So
// "reject private destinations", the usual advice, would break the app's main
// use case. What the policy blocks is therefore split: destinations that are
// never legitimate for anything (see Policy.AllowPrivate) are refused
// everywhere, and the private-range question is answered per caller.
//
// Enforcement happens at CONNECT time, in the dialer, rather than by
// inspecting the URL. Checking a hostname up front and connecting afterwards
// leaves a window in which DNS can answer differently the second time
// (rebinding), and it misses redirect targets entirely. The dialer sees the
// address actually being connected to, on every hop, which closes both.
//
// Proxies are the one case the dialer cannot cover. With HTTP_PROXY/HTTPS_PROXY
// set, the socket goes to the proxy and the real destination never reaches
// Control — so the address check would silently stop applying at exactly the
// moment an operator thinks they have added a layer. Proxied requests instead
// get a pre-flight resolve-and-check (see proxyGuard), which is weaker: it
// resolves separately from the connection, so a name that answers differently
// the second time defeats it, and the proxy's own DNS may not agree with ours
// at all. It is defence in depth over a proxy the operator configured, not a
// boundary against a hostile one.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked is the class of every refusal this package makes. Callers report
// it as a destination problem rather than as a connection failure.
var ErrBlocked = errors.New("destination not permitted")

// Policy is what a particular caller is allowed to reach.
type Policy struct {
	// AllowPrivate permits loopback, RFC1918/ULA and carrier-grade NAT
	// destinations. True for the integrations that are normally on localhost
	// or the LAN (Prowlarr, Jackett, QUI, a self-hosted notification service).
	//
	// It does NOT relax the destinations that are never a legitimate target
	// for any of them: link-local (which is where cloud instance metadata
	// lives, at 169.254.169.254), multicast, and the unspecified address.
	// Those are refused whatever this is set to.
	AllowPrivate bool

	// PinOrigin refuses any redirect that leaves the original scheme, host or
	// port. For the LAN integrations this is what stops "point Yata at a host
	// I control, which answers 302 to somewhere inside your network" — the
	// destination check alone would allow that hop when AllowPrivate is set.
	PinOrigin bool
}

// maxRedirects caps a redirect chain. Go's own default is 10; this is stated
// explicitly because the redirect hook replaces that default.
const maxRedirects = 10

// Validate parses raw and checks its shape against the policy. It performs no
// DNS lookup: resolution is checked in the dialer, where the result cannot
// change between the check and the connection.
func Validate(raw string, p Policy) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: no URL given", ErrBlocked)
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: not a valid URL", ErrBlocked)
	}
	if err := validateURL(u, p); err != nil {
		return nil, err
	}
	return u, nil
}

func validateURL(u *url.URL, _ Policy) error {
	// http and https only. Without this, a destination of file:///…, or one of
	// the schemes a proxy might handle, turns a request-forgery bug into a
	// file read.
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%w: only http and https URLs are allowed, got %q", ErrBlocked, u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%w: URL has no host", ErrBlocked)
	}
	return nil
}

// CheckIP reports whether an address may be connected to under the policy.
// Exported for tests and for callers that have already resolved a destination.
func CheckIP(ip net.IP, p Policy) error {
	switch {
	case ip == nil:
		return fmt.Errorf("%w: unparseable address", ErrBlocked)
	case ip.IsUnspecified():
		return fmt.Errorf("%w: unspecified address %s", ErrBlocked, ip)
	case ip.IsMulticast(), ip.IsInterfaceLocalMulticast(), ip.IsLinkLocalMulticast():
		return fmt.Errorf("%w: multicast address %s", ErrBlocked, ip)
	case ip.IsLinkLocalUnicast():
		// 169.254.0.0/16 and fe80::/10. Cloud instance metadata lives at
		// 169.254.169.254 and hands out credentials to anything that asks, so
		// this stays blocked even for callers allowed onto the LAN.
		return fmt.Errorf("%w: link-local address %s", ErrBlocked, ip)
	}
	if p.AllowPrivate {
		return nil
	}
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("%w: loopback address %s", ErrBlocked, ip)
	case ip.IsPrivate():
		return fmt.Errorf("%w: private address %s", ErrBlocked, ip)
	case isCGNAT(ip):
		return fmt.Errorf("%w: carrier-grade NAT address %s", ErrBlocked, ip)
	}
	return nil
}

// cgnat is 100.64.0.0/10 — shared address space, and what Tailscale hands out,
// so it is a private destination in every sense that matters here even though
// net.IP.IsPrivate does not count it.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && cgnat.Contains(v4)
}

// SameOrigin reports whether two URLs share a scheme, host and port, with
// default ports normalised so "https://h" and "https://h:443" match.
//
// Used both for redirect pinning and for deciding whether a stored credential
// may be sent to a URL the caller supplied.
func SameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		port(a) == port(b)
}

// SameOriginStr is SameOrigin for raw strings. An unparseable side is never
// the same origin as anything, so a malformed stored URL fails closed.
func SameOriginStr(a, b string) bool {
	ua, err := url.Parse(strings.TrimSpace(a))
	if err != nil {
		return false
	}
	ub, err := url.Parse(strings.TrimSpace(b))
	if err != nil {
		return false
	}
	return SameOrigin(ua, ub)
}

func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// IsPublicHost reports whether host is known to live entirely on the public
// internet. It exists to answer a DISCLOSURE question, not an access one: may
// what this destination said be shown to the user?
//
// The distinction matters because a private destination's reply is, by
// definition, something the requester could not otherwise read — which is what
// turns a request-forgery bug into a way to read internal services. A public
// destination's reply is the service's own error message, which is exactly
// what a user debugging a mistyped webhook needs to see.
//
// Conservative in every direction: a name that resolves to a mix of public and
// private addresses is not public, and a name that will not resolve is not
// public either. Callers withhold on false, so an uncertain answer withholds.
func IsPublicHost(ctx context.Context, host string) bool {
	if host == "" {
		return false
	}
	strict := Policy{} // AllowPrivate false: the public-only policy
	if ip := net.ParseIP(host); ip != nil {
		return CheckIP(ip, strict) == nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		if CheckIP(a.IP, strict) != nil {
			return false
		}
	}
	return true
}

// Client returns an HTTP client that enforces the policy on every connection
// and every redirect.
func Client(timeout time.Duration, p Policy) *http.Client {
	return Wrap(&http.Client{Timeout: timeout}, p)
}

// Wrap applies the policy to an existing client, preserving its timeout and
// cookie jar. The jar matters: Jackett's import authenticates with a session
// cookie, so it needs the jar and the policy at once.
func Wrap(c *http.Client, p Policy) *http.Client {
	if c == nil {
		c = &http.Client{}
	}
	base, _ := http.DefaultTransport.(*http.Transport)
	tr := base.Clone()
	tr.DialContext = guardedDial(p)
	return &http.Client{
		Timeout:       c.Timeout,
		Jar:           c.Jar,
		Transport:     &proxyGuard{base: tr, policy: p},
		CheckRedirect: checkRedirect(p),
	}
}

// proxyGuard restores a destination check for requests that go through a
// proxy, where the guarded dialer sees only the proxy's address.
//
// ProxyFromEnvironment is deliberately left in place rather than cleared:
// an operator who set HTTP_PROXY did so because the machine has no other route
// out, and silently ignoring it would break every outbound request instead of
// securing it. So the check moves earlier — resolve the destination and vet it
// before handing the request to the proxy.
//
// Unproxied requests fall straight through, because the dialer already checks
// them at connect time and that is strictly the better place: it cannot be
// raced by DNS, and it sees every redirect hop.
type proxyGuard struct {
	base   *http.Transport
	policy Policy
}

func (g *proxyGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if g.base.Proxy != nil {
		proxyURL, err := g.base.Proxy(req)
		if err != nil {
			return nil, err
		}
		if proxyURL != nil {
			if err := checkDestination(req.Context(), req.URL.Hostname(), g.policy); err != nil {
				return nil, err
			}
		}
	}
	return g.base.RoundTrip(req)
}

// checkDestination resolves host and applies the policy to every address it
// answers with. Used only on the proxy path.
//
// A resolution failure is NOT treated as a refusal. Proxies commonly exist
// precisely because the local host cannot resolve or route to the destination
// itself, so failing closed here would break the ordinary split-horizon setup
// while blocking nothing an attacker could not simply route around. The proxy
// reports the failure on its own terms instead.
//
// Every returned address must pass: the proxy chooses which one it connects
// to, so one bad address in the set is enough to refuse.
func checkDestination(ctx context.Context, host string, p Policy) error {
	if host == "" {
		return fmt.Errorf("%w: URL has no host", ErrBlocked)
	}
	if ip := net.ParseIP(host); ip != nil {
		return CheckIP(ip, p)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if err := CheckIP(a.IP, p); err != nil {
			return err
		}
	}
	return nil
}

func guardedDial(p Policy) func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		// Control runs after the address is resolved and before the socket
		// connects, for each address tried. That is the only point at which
		// the destination is both known and not yet contacted.
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("%w: unparseable address %q", ErrBlocked, address)
			}
			return CheckIP(net.ParseIP(host), p)
		},
	}
	return d.DialContext
}

func checkRedirect(p Policy) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("%w: too many redirects", ErrBlocked)
		}
		if err := validateURL(req.URL, p); err != nil {
			return err
		}
		if p.PinOrigin && len(via) > 0 && !SameOrigin(req.URL, via[0].URL) {
			return fmt.Errorf("%w: redirect to a different origin (%s)", ErrBlocked, req.URL.Host)
		}
		return nil
	}
}

// IsBlocked reports whether err came from this package's policy rather than
// from the network. http.Client wraps CheckRedirect and dial errors in
// *url.Error, so a plain errors.Is on the caller's side would miss it.
func IsBlocked(err error) bool {
	return errors.Is(err, ErrBlocked)
}
