package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// The rule these cover: a STORED credential only ever travels to the STORED
// origin. Every one of these endpoints lets the settings form test an address
// that hasn't been saved yet, and each fell back to the saved credential when
// the caller omitted it — which turns "test an unsaved URL" into "send my
// saved key to any host you name".
//
// Every credential value here is invented.

// postIntegration posts a JSON object and decodes the reply. (trackertest_test
// already has a postJSON taking a raw string body.)
func postIntegration(t *testing.T, router http.Handler, path string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// setStored saves integration credentials the way a configured instance has them.
func setStored(t *testing.T, d *Deps, mutate func(*models.Settings)) {
	t.Helper()
	s := d.Cfg.Settings()
	mutate(&s)
	if err := d.Cfg.UpdateSettings(s); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
}

// collector stands in for the attacker's host: it records whether any
// credential arrived.
type collector struct {
	srv     *httptest.Server
	gotKey  string
	gotAuth string
	gotForm string
	hits    int
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits++
		if v := r.Header.Get("X-API-Key"); v != "" {
			c.gotKey = v
		}
		if v := r.Header.Get("X-Api-Key"); v != "" {
			c.gotKey = v
		}
		if v := r.Header.Get("Authorization"); v != "" {
			c.gotAuth = v
		}
		_ = r.ParseForm()
		if v := r.PostFormValue("password"); v != "" {
			c.gotForm = v
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func TestQUIWillNotSendTheStoredKeyToAnotherHost(t *testing.T) {
	d := testDeps(t)
	attacker := newCollector(t)
	setStored(t, d, func(s *models.Settings) {
		s.QUIURL = "http://qui.internal:7476"
		s.QUIAPIKey = "stored-qui-key-invented"
	})
	router := NewRouter(d)

	code, body := postIntegration(t, router, "/api/qui/instances", map[string]string{"url": attacker.srv.URL})

	if attacker.hits > 0 {
		t.Errorf("Yata contacted the caller's host at all (%d hits)", attacker.hits)
	}
	if attacker.gotKey != "" {
		t.Fatalf("the stored QUI key was sent to another host: %q", attacker.gotKey)
	}
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body %v)", code, body)
	}
}

// Supplying the key explicitly is the legitimate way to test another instance,
// and it must keep working.
func TestQUIAcceptsAnExplicitKeyForAnotherHost(t *testing.T) {
	d := testDeps(t)
	other := newCollector(t)
	setStored(t, d, func(s *models.Settings) {
		s.QUIURL = "http://qui.internal:7476"
		s.QUIAPIKey = "stored-qui-key-invented"
	})
	router := NewRouter(d)

	code, _ := postIntegration(t, router, "/api/qui/instances",
		map[string]string{"url": other.srv.URL, "key": "caller-supplied-key"})

	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
	if other.gotKey != "caller-supplied-key" {
		t.Errorf("key sent = %q, want the caller's own", other.gotKey)
	}
	if other.gotKey == "stored-qui-key-invented" {
		t.Error("the stored key leaked despite an explicit one being supplied")
	}
}

// The CSRF shape of the original bug: a safe method skips the cross-site
// check, and a top-level navigation still carries the session cookie.
func TestQUIInstancesRejectsGET(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	req := httptest.NewRequest(http.MethodGet, "/api/qui/instances?url=http://attacker.invalid/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("the instance lookup is still reachable by GET")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestProwlarrWillNotSendTheStoredKeyToAnotherHost(t *testing.T) {
	d := testDeps(t)
	attacker := newCollector(t)
	setStored(t, d, func(s *models.Settings) {
		s.ProwlarrURL = "http://prowlarr.internal:9696"
		s.ProwlarrAPIKey = "stored-prowlarr-key-invented"
	})
	router := NewRouter(d)

	code, _ := postIntegration(t, router, "/api/prowlarr/indexers", map[string]string{"url": attacker.srv.URL})

	if attacker.gotKey != "" {
		t.Fatalf("the stored Prowlarr key was sent to another host: %q", attacker.gotKey)
	}
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestJackettWillNotSendTheStoredPasswordToAnotherHost(t *testing.T) {
	d := testDeps(t)
	attacker := newCollector(t)
	setStored(t, d, func(s *models.Settings) {
		s.JackettURL = "http://jackett.internal:9117"
		s.JackettAdminPassword = "stored-jackett-password-invented"
	})
	router := NewRouter(d)

	code, _ := postIntegration(t, router, "/api/jackett/indexers", map[string]string{"url": attacker.srv.URL})

	if attacker.gotForm != "" {
		t.Fatalf("the stored Jackett password was POSTed to another host: %q", attacker.gotForm)
	}
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

// Using the stored credential against the stored address is the everyday case.
func TestStoredCredentialsStillWorkForTheStoredAddress(t *testing.T) {
	d := testDeps(t)
	own := newCollector(t)
	setStored(t, d, func(s *models.Settings) {
		s.ProwlarrURL = own.srv.URL
		s.ProwlarrAPIKey = "stored-prowlarr-key-invented"
	})
	router := NewRouter(d)

	code, _ := postIntegration(t, router, "/api/prowlarr/indexers", map[string]string{})

	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
	if own.gotKey != "stored-prowlarr-key-invented" {
		t.Errorf("stored key was not used for its own address (got %q)", own.gotKey)
	}
}

// Non-HTTP destinations are refused before any connection is attempted.
func TestIntegrationEndpointsRejectNonHTTPSchemes(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	for _, path := range []string{"/api/prowlarr/indexers", "/api/jackett/indexers", "/api/qui/instances"} {
		code, body := postIntegration(t, router, path, map[string]string{
			"url": "file:///etc/passwd", "api_key": "x", "admin_password": "x", "key": "x",
		})
		if code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, code)
			continue
		}
		if msg, _ := body["error"].(string); !strings.Contains(msg, "http") {
			t.Errorf("%s: error %q should explain the scheme restriction", path, msg)
		}
	}
}
