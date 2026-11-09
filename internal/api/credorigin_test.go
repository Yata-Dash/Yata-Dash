package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// The rule ssrf_test.go covers for the integration test endpoints — a STORED
// credential only ever travels to the STORED origin — for the paths that
// change a URL rather than only test one: the tracker Test's url override, a
// tracker save, and a settings save. A masked (or absent) secret sent with a
// URL on another origin must never reach that origin: not on this request,
// and not on the next refresh.
//
// Every credential value here is invented.

// secretSink is a host that records everything a request carried — URL,
// headers, body — so a test can ask whether a secret arrived by any route.
type secretSink struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen strings.Builder
	hits int
}

func newSecretSink(t *testing.T) *secretSink {
	t.Helper()
	s := &secretSink{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.hits++
		s.seen.WriteString(r.URL.String() + "\n")
		for k, vs := range r.Header {
			s.seen.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
		}
		s.seen.Write(body)
		s.mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *secretSink) received(secret string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Contains(s.seen.String(), secret)
}

const (
	storedTrackerKey    = "stored-tracker-key-invented"
	storedTrackerCookie = "stored-tracker-cookie-invented"
)

// addSecretTracker saves an undefined UNIT3D tracker at url carrying a stored
// key and cookie, scraping allowed, so a Test exercises both channels.
func addSecretTracker(t *testing.T, d *Deps, id, url string) {
	t.Helper()
	if err := d.Cfg.AddTracker(models.Tracker{
		ID: id, Name: "Secret " + id, URL: url, Type: "unit3d", Username: "someone",
		APIKey: storedTrackerKey, SessionCookie: storedTrackerCookie, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerTestWillNotSendStoredSecretsToAnotherHost(t *testing.T) {
	for name, body := range map[string]string{
		"masked": `{"url":%q,"api_key":"` + maskedKey + `","session_cookie":"` + maskedKey + `"}`,
		"absent": `{"url":%q}`,
	} {
		t.Run(name, func(t *testing.T) {
			d := testDeps(t)
			attacker := newSecretSink(t)
			addSecretTracker(t, d, "tleak1", "http://tracker.invalid")
			router := NewRouter(d)

			rec := postJSON(t, router, "/api/trackers/tleak1/test", fmt.Sprintf(body, attacker.srv.URL))
			if rec.Code != http.StatusOK {
				t.Fatalf("test: %d %s", rec.Code, rec.Body.String())
			}
			if attacker.received(storedTrackerKey) {
				t.Error("the stored API key was sent to the override host")
			}
			if attacker.received(storedTrackerCookie) {
				t.Error("the stored session cookie was sent to the override host")
			}
			var res TrackerTestResult
			_ = json.Unmarshal(rec.Body.Bytes(), &res)
			if res.API.Status != "not_configured" || res.API.Detail != "no_key" {
				t.Errorf("api = %+v, want not_configured/no_key — a new host needs its own key", res.API)
			}
		})
	}
}

// Supplying a key for the other host is the legitimate case and still works;
// the stored cookie, left masked, still stays home.
func TestTrackerTestUsesAnExplicitKeyForAnotherHost(t *testing.T) {
	d := testDeps(t)
	other := newSecretSink(t)
	addSecretTracker(t, d, "tleak2", "http://tracker.invalid")
	router := NewRouter(d)

	body := fmt.Sprintf(`{"url":%q,"api_key":"caller-supplied-key","session_cookie":%q}`, other.srv.URL, maskedKey)
	if rec := postJSON(t, router, "/api/trackers/tleak2/test", body); rec.Code != http.StatusOK {
		t.Fatalf("test: %d %s", rec.Code, rec.Body.String())
	}
	if !other.received("caller-supplied-key") {
		t.Error("the caller's own key was not used")
	}
	if other.received(storedTrackerKey) || other.received(storedTrackerCookie) {
		t.Error("a stored secret reached the other host alongside the caller's key")
	}
}

// A url override on the stored origin — a trailing slash, a path — is still
// the same tracker, and the masked secrets are the stored ones.
func TestTrackerTestKeepsStoredSecretsOnTheSameOrigin(t *testing.T) {
	d := testDeps(t)
	own := newSecretSink(t)
	addSecretTracker(t, d, "tleak3", own.srv.URL)
	router := NewRouter(d)

	body := fmt.Sprintf(`{"url":%q,"api_key":%q}`, own.srv.URL+"/", maskedKey)
	if rec := postJSON(t, router, "/api/trackers/tleak3/test", body); rec.Code != http.StatusOK {
		t.Fatalf("test: %d %s", rec.Code, rec.Body.String())
	}
	if !own.received(storedTrackerKey) {
		t.Error("the stored key was not used for its own origin")
	}
}

func putTracker(t *testing.T, router http.Handler, id, body string) models.TrackerView {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/"+id, strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	var v models.TrackerView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Moving a tracker to another host with the secrets left masked clears them:
// the fetcher would otherwise send them to the new host on the next refresh.
// The reply says so, which is what the edit panel shows.
func TestTrackerSaveClearsSecretsWhenTheOriginChanges(t *testing.T) {
	d := testDeps(t)
	addSecretTracker(t, d, "tmove1", "http://tracker.invalid")
	router := NewRouter(d)

	v := putTracker(t, router, "tmove1",
		fmt.Sprintf(`{"url":"http://elsewhere.invalid","api_key":%q,"session_cookie":%q}`, maskedKey, maskedKey))

	got := mustTracker(t, d, "tmove1")
	if got.URL != "http://elsewhere.invalid" {
		t.Fatalf("url not saved: %q", got.URL)
	}
	if got.APIKey != "" || got.SessionCookie != "" {
		t.Errorf("secrets survived the move: key=%q cookie=%q", got.APIKey, got.SessionCookie)
	}
	if v.HasKey || v.HasSession || v.APIKeyMasked != "" {
		t.Errorf("reply still shows stored secrets: has_key=%v has_session=%v", v.HasKey, v.HasSession)
	}
}

func TestTrackerSaveKeepsSecretsOnTheSameOrigin(t *testing.T) {
	d := testDeps(t)
	addSecretTracker(t, d, "tmove2", "http://tracker.invalid")
	router := NewRouter(d)

	putTracker(t, router, "tmove2",
		fmt.Sprintf(`{"url":"HTTP://tracker.invalid:80/sub/","api_key":%q,"session_cookie":%q}`, maskedKey, maskedKey))

	got := mustTracker(t, d, "tmove2")
	if got.APIKey != storedTrackerKey || got.SessionCookie != storedTrackerCookie {
		t.Errorf("a same-origin edit lost the secrets: key=%q cookie=%q", got.APIKey, got.SessionCookie)
	}
}

// A secret typed with the move is the new host's own, and is kept; the one
// left masked is not carried over.
func TestTrackerSaveTakesAFreshSecretWithTheMove(t *testing.T) {
	d := testDeps(t)
	addSecretTracker(t, d, "tmove3", "http://tracker.invalid")
	router := NewRouter(d)

	putTracker(t, router, "tmove3",
		fmt.Sprintf(`{"url":"https://elsewhere.invalid","api_key":"new-host-key","session_cookie":%q}`, maskedKey))

	got := mustTracker(t, d, "tmove3")
	if got.APIKey != "new-host-key" {
		t.Errorf("api key = %q, want the one sent with the move", got.APIKey)
	}
	if got.SessionCookie != "" {
		t.Errorf("the masked cookie followed the tracker to a new host: %q", got.SessionCookie)
	}
}

// Create runs the same applyPayload: the credentials sent with a new tracker
// are kept as they always were.
func TestTrackerCreateKeepsItsCredentials(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	rec := postJSON(t, router, "/api/trackers",
		`{"url":"https://new.invalid/","name":"New","type":"unit3d","api_key":"k-invented","session_cookie":"c-invented"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var v models.TrackerView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	got := mustTracker(t, d, v.ID)
	if got.APIKey != "k-invented" || got.SessionCookie != "c-invented" {
		t.Errorf("create lost its credentials: key=%q cookie=%q", got.APIKey, got.SessionCookie)
	}
}

func putSettingsJSON(t *testing.T, router http.Handler, s models.Settings) models.Settings {
	t.Helper()
	b, _ := json.Marshal(s)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body.String())
	}
	var out models.Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func storeIntegrationSecrets(t *testing.T, d *Deps) {
	t.Helper()
	setStored(t, d, func(s *models.Settings) {
		s.QUIURL, s.QUIAPIKey = "http://qui.internal:7476", "stored-qui-key-invented"
		s.ProwlarrURL, s.ProwlarrAPIKey = "http://prowlarr.internal:9696", "stored-prowlarr-key-invented"
		s.JackettURL, s.JackettAdminPassword = "http://jackett.internal:9117", "stored-jackett-pw-invented"
	})
}

// A settings save that moves an integration to another host with its secret
// masked clears that secret: refreshQUI fires straight after the save, and
// the import endpoints use whatever is stored.
func TestSettingsSaveClearsSecretsWhenTheOriginChanges(t *testing.T) {
	d := testDeps(t)
	storeIntegrationSecrets(t, d)
	router := NewRouter(d)

	s := maskSettings(d.Cfg.Settings())
	s.QUIURL = "http://attacker.invalid:7476"
	s.ProwlarrURL = "http://prowlarr.internal:9697" // another port is another origin
	s.JackettURL = "https://jackett.internal:9117"  // and so is another scheme
	reply := putSettingsJSON(t, router, s)

	got := d.Cfg.Settings()
	if got.QUIAPIKey != "" || got.ProwlarrAPIKey != "" || got.JackettAdminPassword != "" {
		t.Errorf("secrets survived the move: qui=%q prowlarr=%q jackett=%q",
			got.QUIAPIKey, got.ProwlarrAPIKey, got.JackettAdminPassword)
	}
	if reply.QUIAPIKey != "" || reply.ProwlarrAPIKey != "" || reply.JackettAdminPassword != "" {
		t.Errorf("reply still shows stored secrets: qui=%q prowlarr=%q jackett=%q",
			reply.QUIAPIKey, reply.ProwlarrAPIKey, reply.JackettAdminPassword)
	}
}

func TestSettingsSaveKeepsSecretsOnTheSameOrigin(t *testing.T) {
	d := testDeps(t)
	storeIntegrationSecrets(t, d)
	router := NewRouter(d)

	s := maskSettings(d.Cfg.Settings())
	s.QUIURL = "http://qui.internal:7476/" // a trailing slash is the same place
	s.ProwlarrURL = "http://PROWLARR.internal:9696/prowlarr"
	reply := putSettingsJSON(t, router, s) // Jackett untouched

	got := d.Cfg.Settings()
	if got.QUIAPIKey != "stored-qui-key-invented" || got.ProwlarrAPIKey != "stored-prowlarr-key-invented" ||
		got.JackettAdminPassword != "stored-jackett-pw-invented" {
		t.Errorf("a same-origin save lost a secret: qui=%q prowlarr=%q jackett=%q",
			got.QUIAPIKey, got.ProwlarrAPIKey, got.JackettAdminPassword)
	}
	if reply.QUIAPIKey != maskedKey {
		t.Errorf("reply qui key = %q, want the mask", reply.QUIAPIKey)
	}
}

func TestSettingsSaveTakesAFreshSecretWithTheMove(t *testing.T) {
	d := testDeps(t)
	storeIntegrationSecrets(t, d)
	router := NewRouter(d)

	s := maskSettings(d.Cfg.Settings())
	s.QUIURL, s.QUIAPIKey = "http://qui.elsewhere:7476", "new-qui-key"
	putSettingsJSON(t, router, s)

	if got := d.Cfg.Settings().QUIAPIKey; got != "new-qui-key" {
		t.Errorf("qui key = %q, want the one sent with the move", got)
	}
}
