package scrape

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/netguard"
)

const minimalProfile = `<html><body><time class="profile__registration" datetime="2024-01-02 10:00:00">x</time></body></html>`

// The profile path is appended to the tracker URL, so one that does not begin
// with "/" can name another host — which then receives the session cookie.
// Defs are validated at load, but Profile is handed a Spec and enforces the
// rule itself before sending anything.
func TestProfileRefusesAPathThatChangesTheHost(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write([]byte(minimalProfile)) //nolint:errcheck
	}))
	defer ts.Close()

	for _, path := range []string{"@evil.example/users/{username}", ".evil.example/u/{id}", "evil/{username}"} {
		tr := models.Tracker{URL: ts.URL, Username: "bob", SessionCookie: "session=abc"}
		_, serr := Profile(tr, Spec{ProfilePath: path, KnownUserID: "7"})
		if serr == nil || serr.Kind != "bad_profile_path" {
			t.Errorf("path %q: err = %s, want bad_profile_path", path, kindOf(serr))
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d requests were sent for refused paths", n)
	}
}

func TestProfileFetchesAnOrdinaryPath(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(minimalProfile)) //nolint:errcheck
	}))
	defer ts.Close()

	tr := models.Tracker{URL: ts.URL + "/", Username: "bob", SessionCookie: "session=abc"}
	got, serr := Profile(tr, Spec{})
	if serr != nil {
		t.Fatalf("Profile: %v", serr)
	}
	if gotPath != "/users/bob" {
		t.Errorf("requested %q, want /users/bob", gotPath)
	}
	if got["join_date"] != "2024-01-02" {
		t.Errorf("join_date = %q", got["join_date"])
	}
}

// A page past the cap is refused rather than read into memory whole.
func TestProfileRefusesAnOversizedPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(minimalProfile)) //nolint:errcheck
		chunk := []byte(strings.Repeat("<p>x</p>", 8<<10))
		for written := 0; written <= netguard.MaxResponseBytes; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	tr := models.Tracker{URL: ts.URL, Username: "bob", SessionCookie: "session=abc"}
	_, serr := Profile(tr, Spec{})
	if serr == nil || serr.Kind != "read_error" {
		t.Fatalf("err = %s, want read_error", kindOf(serr))
	}
}

func kindOf(e *Error) string {
	if e == nil {
		return "<nil>"
	}
	return e.Kind
}
