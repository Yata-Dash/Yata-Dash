package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHostAllowedDefaults(t *testing.T) {
	cases := []struct {
		host string
		want bool
		why  string
	}{
		// IP literals can never be the product of a rebind — no DNS was used.
		{"127.0.0.1:8420", true, "loopback by address"},
		{"127.0.0.1", true, "loopback, no port"},
		{"192.168.1.50:8420", true, "LAN address"},
		{"100.64.1.2:8420", true, "Tailscale address"},
		{"[::1]:8420", true, "IPv6 loopback"},
		{"[fd00::1]:8420", true, "IPv6 ULA"},
		{"203.0.113.7:8420", true, "public address — still an address"},
		// Resolved by the OS, not by anyone's nameserver.
		{"localhost:8420", true, "localhost"},
		{"LOCALHOST", true, "case-insensitive"},
		// Names have to be configured.
		{"yata.example.com", false, "an unconfigured hostname"},
		{"attacker.example:8420", false, "the rebinding case"},
		{"localhost.attacker.example", false, "a name that merely starts with localhost"},
		{"", false, "no Host header at all"},
	}
	for _, c := range cases {
		if got := hostAllowed(c.host, nil); got != c.want {
			t.Errorf("hostAllowed(%q) = %v, want %v (%s)", c.host, got, c.want, c.why)
		}
	}
}

func TestHostAllowedWithConfiguredNames(t *testing.T) {
	allowed := []string{"yata.example.com", "Yata.Internal:8420"}
	for _, host := range []string{"yata.example.com", "yata.example.com:8420", "YATA.EXAMPLE.COM", "yata.internal"} {
		if !hostAllowed(host, allowed) {
			t.Errorf("hostAllowed(%q) = false, want true once configured", host)
		}
	}
	if hostAllowed("other.example.com", allowed) {
		t.Error("an unlisted hostname was allowed")
	}
}

// The escape hatch, for anyone whose setup this gets wrong.
func TestHostWildcardDisablesTheCheck(t *testing.T) {
	for _, host := range []string{"anything.example", "attacker.example:8420"} {
		if !hostAllowed(host, []string{"*"}) {
			t.Errorf("hostAllowed(%q) with \"*\" = false, want true", host)
		}
	}
}

// A blank entry (a trailing comma, a stray space) must not become a wildcard
// or match a request with no Host.
func TestBlankAllowedEntryMatchesNothing(t *testing.T) {
	if hostAllowed("", []string{"", "  "}) {
		t.Error("a blank allowed entry matched an empty Host")
	}
	if hostAllowed("attacker.example", []string{"", "  "}) {
		t.Error("a blank allowed entry matched a hostname")
	}
}

func hostGuardRouter(t *testing.T, allowed ...string) http.Handler {
	t.Helper()
	d := testDeps(t)
	d.AllowedHosts = allowed
	return NewRouter(d)
}

func requestWithHost(t *testing.T, router http.Handler, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// The attack in full: the browser reports a rebound request as same-origin, so
// the cross-site check passes it. Only the Host tells the difference.
func TestRebindingAttemptIsRefusedEvenWhenItLooksSameOrigin(t *testing.T) {
	router := hostGuardRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/trackers", nil)
	req.Host = "attacker.example:8420"
	req.Header.Set("Sec-Fetch-Site", "same-origin") // what a rebound fetch sends
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — a rebound request was served", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "host_not_allowed") {
		t.Errorf("body = %q, want the host_not_allowed code", rec.Body.String())
	}
}

// An unconfigured instance is the dangerous case: no session is needed, so a
// rebound page could otherwise read every credential.
func TestRebindingCannotReachTheConfigExportOnAnOpenInstance(t *testing.T) {
	router := hostGuardRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/config/export", strings.NewReader("{}"))
	req.Host = "attacker.example"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "settings") {
		t.Error("config content was served to a rebound host")
	}
}

func TestOrdinaryLocalAccessStillWorks(t *testing.T) {
	router := hostGuardRouter(t)
	for _, host := range []string{"localhost:8423", "127.0.0.1:8423", "192.168.1.20:8423", "[::1]:8423"} {
		if rec := requestWithHost(t, router, host, "/api/trackers"); rec.Code != http.StatusOK {
			t.Errorf("host %q: status = %d, want 200", host, rec.Code)
		}
	}
}

func TestConfiguredProxyHostWorks(t *testing.T) {
	router := hostGuardRouter(t, "yata.example.com")
	if rec := requestWithHost(t, router, "yata.example.com", "/api/trackers"); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for the configured proxy hostname", rec.Code)
	}
}

// Someone who has put Yata behind a proxy meets this on every page load, so
// the refusal has to say what to do rather than being a bare 403.
func TestRefusalForAPageExplainsTheFix(t *testing.T) {
	router := hostGuardRouter(t)
	rec := requestWithHost(t, router, "yata.example.com", "/")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"allowed-hosts", "YATA_ALLOWED_HOSTS", "yata.example.com", "rebinding"} {
		if !strings.Contains(body, want) {
			t.Errorf("the explanation is missing %q:\n%s", want, body)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain so a browser shows it", ct)
	}
}

// Static assets are not secret, but serving the shell to a rebound origin only
// makes the failure confusing later.
func TestStaticAssetsAreAlsoGuarded(t *testing.T) {
	router := hostGuardRouter(t)
	if rec := requestWithHost(t, router, "attacker.example", "/static/dashboard.js"); rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// The point of putting the list in settings: someone who sets Yata up on
// localhost can add the domain they'll use from a phone later, and it has to
// work without a restart they would have to be at the machine to perform.
func TestHostAddedInSettingsAppliesWithoutRestart(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	if rec := requestWithHost(t, router, "yata.example.com", "/api/trackers"); rec.Code != http.StatusForbidden {
		t.Fatalf("precondition: status = %d, want 403 before the name is added", rec.Code)
	}

	s := d.Cfg.Settings()
	s.AllowedHosts = []string{"yata.example.com"}
	if err := d.Cfg.UpdateSettings(s); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	// Same router, same process — no restart.
	if rec := requestWithHost(t, router, "yata.example.com", "/api/trackers"); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — the settings list is not being read live", rec.Code)
	}
	if rec := requestWithHost(t, router, "other.example.com", "/api/trackers"); rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 — an unrelated name should still be refused", rec.Code)
	}
}

// Flag/env and settings are a union, not a precedence: a name added in the UI
// must work whether or not the deployment also passed a flag.
func TestFlagAndSettingsHostsAreBothHonoured(t *testing.T) {
	d := testDeps(t)
	d.AllowedHosts = []string{"from-flag.example"}
	s := d.Cfg.Settings()
	s.AllowedHosts = []string{"from-settings.example"}
	if err := d.Cfg.UpdateSettings(s); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	router := NewRouter(d)

	for _, host := range []string{"from-flag.example", "from-settings.example"} {
		if rec := requestWithHost(t, router, host, "/api/trackers"); rec.Code != http.StatusOK {
			t.Errorf("host %q: status = %d, want 200", host, rec.Code)
		}
	}
}

// Turning the check off entirely is a deployment decision, so it must not be
// reachable through the API — otherwise the one setting that disables a
// security control could be flipped from a browser session.
func TestWildcardIsRefusedFromTheAPIButWorksFromTheFlag(t *testing.T) {
	if _, err := cleanAllowedHosts([]string{"*"}); err == nil {
		t.Error("the API accepted a \"*\" wildcard")
	}
	if !hostAllowed("anything.example", []string{"*"}) {
		t.Error("the wildcard should still work when set by flag or env")
	}
}

func TestCleanAllowedHostsRejectsThingsThatAreNotHostnames(t *testing.T) {
	for _, bad := range []string{"https://yata.example.com", "yata.example.com/path", "two hosts", "a@b"} {
		if _, err := cleanAllowedHosts([]string{bad}); err == nil {
			t.Errorf("cleanAllowedHosts accepted %q", bad)
		}
	}
	got, err := cleanAllowedHosts([]string{" yata.example.com ", "", "  ", "box.tailnet.ts.net:8420"})
	if err != nil {
		t.Fatalf("rejected a valid list: %v", err)
	}
	if len(got) != 2 || got[0] != "yata.example.com" || got[1] != "box.tailnet.ts.net:8420" {
		t.Errorf("cleanAllowedHosts = %v, want the two real entries trimmed", got)
	}
}
