package defs

import "testing"

// api.base_url is the only field in a definition that chooses where Yata sends
// a request — it overrides the URL the user typed. Bundled defs are reviewed
// here, but contributed ones are the direction of travel, and a reviewer
// checking field mappings would not reliably notice an address pointed inward.

func TestDefBaseURLRejectsNonPublicAddresses(t *testing.T) {
	bad := []struct {
		url string
		why string
	}{
		{"http://192.168.1.1/api", "the user's router"},
		{"http://10.0.0.5:8080/api", "an internal server"},
		{"http://127.0.0.1:9999/api", "the machine Yata runs on"},
		{"http://[::1]/api", "loopback over IPv6"},
		{"http://169.254.169.254/latest/meta-data/", "cloud instance metadata"},
		{"http://100.64.1.2/api", "a tailnet address"},
		{"file:///etc/passwd", "not even HTTP"},
		{"ftp://example.invalid/x", "a scheme Yata does not speak"},
	}
	for _, c := range bad {
		if err := validateDefBaseURL(c.url); err == nil {
			t.Errorf("validateDefBaseURL(%q) was accepted — %s", c.url, c.why)
		}
	}
}

func TestDefBaseURLAcceptsRealTrackerAPIs(t *testing.T) {
	good := []string{
		"",                               // absent: the tracker's own URL is used
		"https://api.broadcasthe.net",    // the one bundled def that sets it
		"https://api.example.org/v2",     // an ordinary public API host
		"http://tracker.example.invalid", // a name, resolved and checked at connect time
		"https://93.184.216.34/api",      // a literal public address
	}
	for _, u := range good {
		if err := validateDefBaseURL(u); err != nil {
			t.Errorf("validateDefBaseURL(%q) = %v, want nil", u, err)
		}
	}
}

// The load-time check only sees literal addresses. A def naming a HOSTNAME
// that resolves inward passes here on purpose and is stopped in the dialer —
// this records that split so nobody later mistakes this for the enforcement.
func TestDefBaseURLDoesNotClaimToResolveNames(t *testing.T) {
	if err := validateDefBaseURL("http://internal.example.invalid/api"); err != nil {
		t.Fatalf("a hostname should pass the load-time check, got %v", err)
	}
}

// Every bundled def must satisfy the rule the loader enforces.
func TestBundledDefsHavePublicBaseURLs(t *testing.T) {
	reg, err := Load("../../defs")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, td := range reg.Trackers() {
		if td.API == nil {
			continue
		}
		if err := validateDefBaseURL(td.API.BaseURL); err != nil {
			t.Errorf("bundled def %q: %v", td.Key, err)
		}
	}
}
