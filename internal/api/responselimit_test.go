package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/netguard"
)

// oversizedServer serves valid JSON just past the cap, so only the cap refuses it.
func oversizedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"pad":"`)) //nolint:errcheck
		chunk := []byte(strings.Repeat("Z", 64<<10))
		for written := 0; written <= netguard.MaxResponseBytes; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		w.Write([]byte(`"}]`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestQUIFetchRefusesAnOversizedResponse(t *testing.T) {
	srv := oversizedServer(t)
	_, _, err := quiFetch(srv.URL+"/api/instances", "qui-key")
	if err == nil {
		t.Fatal("an oversized QUI response was accepted")
	}
	// The error reaches the browser: no body content, no key.
	if strings.Contains(err.Error(), "ZZZZ") || strings.Contains(err.Error(), "qui-key") {
		t.Errorf("error %q carries body content or the key", err)
	}
}

func TestJackettReadsAreCapped(t *testing.T) {
	srv := oversizedServer(t)
	client, err := jackettClient()
	if err != nil {
		t.Fatal(err)
	}
	if list, msg, _ := jackettList(client, srv.URL); list != nil || msg != "read_error" {
		t.Errorf("jackettList = (%d entries, %q), want read_error", len(list), msg)
	}
	if items := jackettConfig(client, srv.URL, "x"); items != nil {
		t.Errorf("jackettConfig decoded %d items from an oversized body", len(items))
	}
}
