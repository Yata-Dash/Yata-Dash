package fetch

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/netguard"
)

func TestGazelleGamesKeyDoesNotFollowACrossOriginRedirect(t *testing.T) {
	var leaked atomic.Value
	leaked.Store("")
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if k := r.Header.Get("X-API-Key"); k != "" {
			leaked.Store(k)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer elsewhere.Close()
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.RequestURI(), http.StatusFound)
	}))
	defer tracker.Close()

	c := NewClient(gazelleGamesRegistry(t, tracker.URL), "")
	_, _ = c.Fetch(models.Tracker{URL: tracker.URL, Type: "gazelle_games", APIKey: "sekrit"})
	if k := leaked.Load().(string); k != "" {
		t.Errorf("the redirect target received X-API-Key %q", k)
	}
}

// A JSON-RPC key lives in the body; a cross-origin 307/308 must be refused.
func TestJSONRPCBodyIsNotReplayedToAnotherOrigin(t *testing.T) {
	var reached atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		io.Copy(io.Discard, r.Body) //nolint:errcheck
	}))
	defer elsewhere.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/", http.StatusTemporaryRedirect)
	}))
	defer api.Close()

	c := NewClient(btnRegistry(t, api.URL), "")
	_, ferr := c.Fetch(models.Tracker{URL: "https://broadcasthe.test", Type: "custom", APIKey: "sekrit"})
	if ferr == nil {
		t.Fatal("a 307 to another origin was followed")
	}
	if ferr.Kind != "blocked_destination" {
		t.Errorf("Kind = %q, want blocked_destination", ferr.Kind)
	}
	if reached.Load() {
		t.Error("the JSON-RPC body (and the key in it) reached the redirect target")
	}
}

// The shipped policies, not just whatever the suite happens to install.
func TestTrackerPoliciesGuardCredentialsAcrossRedirects(t *testing.T) {
	for name, p := range map[string]netguard.Policy{"TrackerPolicy": TrackerPolicy, "DefBaseURLPolicy": DefBaseURLPolicy} {
		if !p.PinBodyOrigin {
			t.Errorf("%s does not refuse a cross-origin body replay", name)
		}
		var hasKey bool
		for _, h := range p.CredentialHeaders {
			hasKey = hasKey || strings.EqualFold(h, "X-API-Key")
		}
		if !hasKey {
			t.Errorf("%s does not drop X-API-Key on a cross-origin redirect", name)
		}
	}
}

func TestOversizedAPIResponseIsRefused(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":"`) //nolint:errcheck
		chunk := []byte(strings.Repeat("A", 64<<10))
		for written := 0; written <= netguard.MaxResponseBytes; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		io.WriteString(w, `"}`) //nolint:errcheck
	}))
	defer ts.Close()

	c := NewClient(customRegistry(t, ts.URL), "")
	_, ferr := c.Fetch(models.Tracker{URL: ts.URL, Type: "custom", APIKey: "sekrit", JoinDate: "2024-01-01"})
	if ferr == nil {
		t.Fatal("an oversized response was accepted")
	}
	if ferr.Kind != "read_error" {
		t.Errorf("Kind = %q, want read_error", ferr.Kind)
	}
	if strings.Contains(ferr.Error(), "AAAA") || strings.Contains(ferr.Error(), "sekrit") {
		t.Errorf("error %q carries body content or the key", ferr.Error())
	}
}
