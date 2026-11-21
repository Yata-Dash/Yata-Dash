package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

func patchSettingsRaw(t *testing.T, router http.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/settings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// Issue #94: two tabs loaded the settings, each changed a different one. With
// a full-object PUT the second save put the first tab's change back. Each
// PATCH carries only its own field, so both survive.
func TestSettingsPatchKeepsOtherTabsChanges(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	if rec := patchSettingsRaw(t, router, http.MethodPatch, `{"show_favicons":true}`); rec.Code != http.StatusOK {
		t.Fatalf("tab B: %d %s", rec.Code, rec.Body.String())
	}
	if rec := patchSettingsRaw(t, router, http.MethodPatch, `{"show_stat_sources":true}`); rec.Code != http.StatusOK {
		t.Fatalf("tab A: %d %s", rec.Code, rec.Body.String())
	}
	got := d.Cfg.Settings()
	if !got.ShowFavicons || !got.ShowStatSources {
		t.Errorf("show_favicons=%v show_stat_sources=%v, want both true", got.ShowFavicons, got.ShowStatSources)
	}
}

// The reported damage: api_only_mode flipped back by a tab that never touched it.
func TestSettingsPatchLeavesUntouchedFieldsAlone(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)
	setStored(t, d, func(s *models.Settings) {
		s.APIOnlyMode = false
		s.AllowedHosts = []string{"yata.example"}
	})
	if rec := patchSettingsRaw(t, router, http.MethodPatch, `{"theme":"dark"}`); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	got := d.Cfg.Settings()
	if got.APIOnlyMode || len(got.AllowedHosts) != 1 || got.Theme != "dark" {
		t.Errorf("patch changed more than theme: %+v", got)
	}
}

// A page still running the old script must not be able to write a stale
// full object; it is told to reload instead.
func TestSettingsPutIsRefused(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)
	rec := patchSettingsRaw(t, router, http.MethodPut, `{"api_only_mode":true}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "reload") {
		t.Fatalf("PUT: %d %s, want 409 asking for a reload", rec.Code, rec.Body.String())
	}
	if d.Cfg.Settings().APIOnlyMode {
		t.Error("a refused PUT still changed the settings")
	}
}

func TestSettingsPatchRejectsBadInputWithoutSaving(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)
	before := d.Cfg.Settings()
	for _, body := range []string{
		`{"show_favicons":true,"no_such_setting":1}`,   // typo'd key
		`{"show_favicons":true,"allowed_hosts":["*"]}`, // refused hostname
		`{"show_favicons":"yes"}`,                      // wrong type
		`[]`, `not json`,
	} {
		rec := patchSettingsRaw(t, router, http.MethodPatch, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", body, rec.Code, rec.Body.String())
		}
	}
	if d.Cfg.Settings().ShowFavicons != before.ShowFavicons {
		t.Error("a rejected patch was partly applied")
	}
}

// Leaving a secret out of the patch is "keep it", which still follows the
// origin rule: a patch that only moves qui to another host clears its key.
func TestSettingsPatchMoveWithoutKeyClearsIt(t *testing.T) {
	d := testDeps(t)
	storeIntegrationSecrets(t, d)
	router := NewRouter(d)

	if rec := patchSettingsRaw(t, router, http.MethodPatch, `{"show_favicons":true}`); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d", rec.Code)
	}
	if got := d.Cfg.Settings(); got.QUIAPIKey != "stored-qui-key-invented" || got.ProwlarrAPIKey == "" {
		t.Fatal("an unrelated patch dropped a stored secret")
	}
	if rec := patchSettingsRaw(t, router, http.MethodPatch, `{"qui_url":"http://qui.elsewhere:7476"}`); rec.Code != http.StatusOK {
		t.Fatalf("move: %d", rec.Code)
	}
	got := d.Cfg.Settings()
	if got.QUIAPIKey != "" {
		t.Errorf("qui key %q followed the move to another host", got.QUIAPIKey)
	}
	if got.ProwlarrAPIKey != "stored-prowlarr-key-invented" {
		t.Errorf("moving qui touched Prowlarr's key: %q", got.ProwlarrAPIKey)
	}
	if strings.Contains(patchSettingsRaw(t, router, http.MethodPatch, `{}`).Body.String(), "stored-") {
		t.Error("a reply carried a stored secret")
	}
}

// Many tabs saving different settings at once: every change lands.
func TestSettingsConcurrentPatchesAllLand(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)
	fields := []string{"show_favicons", "show_stat_sources", "private_mode", "api_only_mode",
		"highlight_hnr", "show_goal_chips", "auto_interval", "trust_proxy_headers"}
	var wg sync.WaitGroup
	for _, f := range fields {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(`{"`+f+`":true}`)))
			if rec.Code != http.StatusOK {
				t.Errorf("%s: %d %s", f, rec.Code, rec.Body.String())
			}
		}(f)
	}
	wg.Wait()
	reply := patchSettingsRaw(t, router, http.MethodPatch, `{}`).Body.String()
	for _, f := range fields {
		if !strings.Contains(reply, `"`+f+`":true`) {
			t.Errorf("%s was lost: %s", f, reply)
		}
	}
}
