package fetch

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/netguard"
)

// api.base_url lets a definition override the URL the user typed, which makes
// it the one place a def file decides where Yata sends a request. Under the
// shipped policy that destination must be on the public internet — a tracker's
// API never lives inside the user's own network, so a def that says otherwise
// is either broken or hostile.
//
// TestMain relaxes the policy for the rest of the suite (httptest binds to
// loopback), so this test rebuilds a client with the SHIPPED policy to prove
// the restriction is real rather than notional.
func TestDefSuppliedBaseURLCannotReachPrivateAddresses(t *testing.T) {
	var reached bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := NewClient(btnRegistry(t, ts.URL), "")
	// The shipped policy, not the relaxed one TestMain installs.
	c.HTTPDefBase = netguard.Client(5*time.Second, netguard.Policy{})

	_, ferr := c.Fetch(models.Tracker{URL: "https://broadcasthe.test", Type: "custom", APIKey: "sekrit"})
	if ferr == nil {
		t.Fatal("a def pointed its api.base_url at loopback and the fetch succeeded")
	}
	if ferr.Kind != "blocked_destination" {
		t.Errorf("Kind = %q, want blocked_destination — a refused destination is a def "+
			"bug and must not read as an ordinary connection failure", ferr.Kind)
	}
	if reached {
		t.Error("the request actually arrived; it must be refused before connecting")
	}
}

// The user's OWN tracker URL keeps the permissive policy. Someone pointing
// Yata at their own network is making a decision about their own machine, and
// unlike a def that instruction cannot arrive from a stranger. This pins the
// distinction so the two are not later collapsed into one policy by accident.
func TestUserSuppliedTrackerURLIsNotRestrictedToPublicAddresses(t *testing.T) {
	if !TrackerPolicy.AllowPrivate {
		t.Fatal("TrackerPolicy no longer allows private addresses — if that was " +
			"deliberate, self-hosted and Tailscale-reached trackers stop working " +
			"and this test should be replaced, not just deleted")
	}
}
