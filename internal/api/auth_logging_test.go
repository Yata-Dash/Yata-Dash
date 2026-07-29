package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/logging"
)

// A password typed one box too high is the commonest way a credential ends up
// in a username field, and the log is meant to be attachable to a GitHub
// issue. Nothing submitted as a username is echoed unless it matches the
// configured account, in which case it is a username by definition.
func TestFailedLoginDoesNotEchoAnUnrecognisedUsername(t *testing.T) {
	d := testDeps(t)
	var out bytes.Buffer
	lg, err := logging.New("", logging.Trace, 100, &out, 1<<20, 1)
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	d.Log = lg
	router := NewRouter(d)

	// Configure an account so the login path is live.
	body, _ := json.Marshal(authCreds{Username: "someone", Password: "correct horse battery"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}

	// The mistake: the password ends up in the username field.
	const leaked = "correct horse battery"
	body, _ = json.Marshal(authCreds{Username: leaked, Password: ""})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login should have failed, got %d", rec.Code)
	}

	if got := out.String(); strings.Contains(got, leaked) {
		t.Errorf("the submitted username was written to the log: %q", got)
	}
	if !strings.Contains(out.String(), "failed login attempt") {
		t.Errorf("the failure should still be recorded: %q", out.String())
	}
}
