package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	jsonStatus(w, status, map[string]string{"error": msg})
}

// upstreamStatus maps an upstream service's auth-failure statuses to 502
// before they reach the browser. A 401 from Yata means "your session
// expired" to the SPA (it re-shows the login gate) and can trigger proxy
// auth popups, so a tracker's or integration's own 401/403 must never be
// forwarded verbatim — the error kind in the body carries the real cause.
func upstreamStatus(code int) int {
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return http.StatusBadGateway
	}
	return code
}

// newID returns a random 16-hex-char identifier.
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
