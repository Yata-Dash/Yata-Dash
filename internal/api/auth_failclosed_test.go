package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The bug these cover: authConfigured returned `err == nil && ok`, so a
// database error was indistinguishable from "no account exists" — and "no
// account exists" is the state in which Yata is deliberately wide open. A
// failed lookup therefore unlocked every protected route.
//
// The error is induced by closing the database handle rather than by mocking,
// so what is exercised is a real store error travelling the real path.

func closedDB(t *testing.T) *Deps {
	t.Helper()
	d := testDeps(t)
	if err := d.DB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	return d
}

// Guards the tests below from passing vacuously. A closed handle has to
// produce the exact condition the old code mishandled: GetUser returning an
// error, which `err == nil && ok` scored as "not configured" — the open state.
// If a future store change made GetUser succeed here, every test in this file
// would still pass while testing nothing.
func TestClosedDatabaseReproducesTheFailOpenCondition(t *testing.T) {
	d := closedDB(t)

	_, ok, err := d.DB.GetUser()
	if err == nil {
		t.Fatal("a closed database no longer errors on GetUser — these tests need a new way to induce it")
	}
	if oldVerdict := err == nil && ok; oldVerdict {
		t.Fatal("expected the old predicate to read as unconfigured")
	}
	if got := authStateOf(d); got != authUnknown {
		t.Errorf("authStateOf = %v, want authUnknown", got)
	}
}

func TestProtectedRouteIsClosedWhenAccountCannotBeRead(t *testing.T) {
	d := closedDB(t)
	router := NewRouter(d)

	req := httptest.NewRequest(http.MethodGet, "/api/trackers", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("a database error opened a protected route (status %d, body %s)",
			rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// Setup writes the account row and needs no session, so treating a failed
// lookup as "unconfigured" would let a database error hand the account to
// whoever asks first.
func TestSetupIsRefusedWhenAccountCannotBeRead(t *testing.T) {
	d := closedDB(t)
	router := NewRouter(d)

	body, _ := json.Marshal(authCreds{Username: "someone", Password: "correct horse battery"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
		t.Fatalf("a database error allowed account creation (status %d)", rec.Code)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// The SPA boots off /auth/status. Reporting "configured: false" on a failed
// lookup tells it the instance is open and shows the setup screen.
func TestStatusDoesNotClaimUnconfiguredOnError(t *testing.T) {
	d := closedDB(t)
	router := NewRouter(d)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if configured, present := out["configured"]; present && configured == false {
		t.Error(`reported "configured": false on a database error`)
	}
	if authed, present := out["authenticated"]; present && authed == true {
		t.Error(`reported "authenticated": true on a database error`)
	}
}

// The open first-run instance is a deliberate feature; fixing the failure mode
// must not break it.
func TestUnconfiguredInstanceStaysOpen(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	req := httptest.NewRequest(http.MethodGet, "/api/trackers", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("first-run instance should be open, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// …and a configured instance must still answer 401, not 503 — the states have
// to stay distinguishable for the SPA's session-expiry handling to work.
func TestConfiguredInstanceStillReturns401WithoutSession(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	body, _ := json.Marshal(authCreds{Username: "someone", Password: "correct horse battery"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/trackers", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
