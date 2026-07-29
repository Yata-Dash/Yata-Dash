// hostguard.go — rejects requests that arrive under a hostname Yata does not
// expect, which is what stops DNS rebinding.
package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
)

// The attack this exists for:
//
// Binding to 127.0.0.1 keeps the network out, but not the browser running on
// the same machine, which executes JavaScript from every site the user visits.
// Normally the same-origin policy stops those pages reading Yata's responses.
// DNS rebinding removes that: attacker.example resolves to the attacker's
// server, the page loads, the DNS record is then re-pointed at 127.0.0.1, and
// the page's own script fetches http://attacker.example:8420/… — which the
// browser treats as same-origin, so the response can be READ.
//
// None of Yata's other defences catch it. The cross-site check sees
// Sec-Fetch-Site: same-origin and allows the request, because as far as the
// browser is concerned it is. The session cookie does not travel (it is scoped
// to localhost, not to the attacker's domain), so a configured instance
// answers 401 — but an instance with no account set up is wide open by design,
// and that is exactly the state most first-run installs sit in.
//
// The defence works because rebinding NEEDS a hostname the attacker controls.
// A request whose Host is an IP literal cannot have been rebound: no DNS was
// involved. So IP literals and localhost pass without configuration, which
// covers direct access — the overwhelming majority of installs — and any other
// hostname has to be named explicitly, which a reverse proxy's operator can do
// and an attacker cannot.

// hostAllowed reports whether a request's Host may be served.
//
// allowed holds extra hostnames from --allowed-hosts / YATA_ALLOWED_HOSTS; an
// entry of "*" disables the check entirely, for anyone whose setup this gets
// wrong and who would otherwise be stuck.
func hostAllowed(rawHost string, allowed []string) bool {
	host := hostOnly(rawHost)
	if host == "" {
		// HTTP/1.1 requires a Host. An empty one is malformed, and matching it
		// against anything would mean guessing.
		return false
	}
	// An IP literal cannot be the product of a rebind.
	if net.ParseIP(host) != nil {
		return true
	}
	// "localhost" is resolved by the OS, not by an attacker's nameserver, and
	// is how most people reach a local install.
	if strings.EqualFold(host, "localhost") {
		return true
	}
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "*" {
			return true
		}
		if a != "" && strings.EqualFold(hostOnly(a), host) {
			return true
		}
	}
	return false
}

// hostOnly strips any port and brackets, so "[::1]:8420" becomes "::1".
func hostOnly(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	if stripped, _, err := net.SplitHostPort(h); err == nil {
		h = stripped
	}
	return strings.Trim(h, "[]")
}

// allowedHostsFor combines the two sources, read fresh on every request.
//
// A union rather than a precedence order: both answer the same question, "what
// names is this instance known by", and a user who adds a name in Settings
// expects it to work whether or not a flag was also passed. Reading the
// settings live is what makes the UI field useful — someone who sets Yata up
// on localhost can add their domain before they travel, without a restart they
// would have to be at the machine to perform.
func allowedHostsFor(d *Deps) []string {
	hosts := d.AllowedHosts
	if d != nil && d.Cfg != nil {
		if fromSettings := d.Cfg.Settings().AllowedHosts; len(fromSettings) > 0 {
			hosts = append(append([]string{}, hosts...), fromSettings...)
		}
	}
	return hosts
}

// hostWarned keeps the rejection log to one line per unknown hostname. A
// rebinding attempt retries in a loop, and a misconfigured proxy sends every
// request under the same name — neither is worth thousands of identical lines.
var hostWarned sync.Map

// hostGuard rejects requests whose Host is a name Yata was not told to answer
// to. It runs before everything, including the static assets: they are not
// secret, but serving the app shell to a rebound origin only makes the failure
// confusing later.
func hostGuard(d *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hostAllowed(r.Host, allowedHostsFor(d)) {
				next.ServeHTTP(w, r)
				return
			}
			host := hostOnly(r.Host)
			if _, seen := hostWarned.LoadOrStore(host, true); !seen {
				d.logWarnf("security: refused a request for host %q — if this is your own "+
					"reverse proxy or hostname, add it: YATA_ALLOWED_HOSTS=%s (Docker), "+
					"\"allowed_hosts\": [%q] in config.json's server block, or "+
					"--allowed-hosts=%s. If it is not yours, this was probably a "+
					"DNS-rebinding attempt and nothing was served.", host, host, host, host)
			}
			// The message matters more than the status: a user who has put
			// Yata behind a proxy hits this on every page and needs to know
			// what to do about it, not a bare 403.
			if strings.HasPrefix(r.URL.Path, "/api/") {
				jsonError(w, "host_not_allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(
				"Yata refused this request because it arrived for the hostname \"" + host + "\",\n" +
					"which this instance was not told to answer to.\n\n" +
					"If you reach Yata through a reverse proxy or a custom hostname, add it\n" +
					"in whichever of these suits how you run Yata, then restart it:\n\n" +
					"  Docker / compose      environment:\n" +
					"                          - YATA_ALLOWED_HOSTS=" + host + "\n\n" +
					"  config.json           \"server\": { ..., \"allowed_hosts\": [\"" + host + "\"] }\n\n" +
					"  command line          --allowed-hosts=" + host + "\n\n" +
					"Comma-separate (or list) several if you use more than one name.\n" +
					"Access by IP address or via localhost always works and needs no setting.\n\n" +
					"This check exists to block DNS rebinding, where a web page you visit\n" +
					"re-points its own domain at your machine to read this app's data.\n"))
		})
	}
}
