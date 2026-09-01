package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
)

// jsonOK writes a successful JSON response.
//
// It marshals into a BUFFER first, because encoding straight to the
// ResponseWriter makes a failure invisible: the 200 header goes out with the
// first byte, so a value encoding/json refuses (±Inf and NaN are the ones that
// happen in practice) leaves the client holding a 200 with an empty body, and
// the discarded error means nothing is logged. That is precisely how issue #40
// hid — an infinite ratio in a pathways response, showing up as nothing more
// than "http 0" in the access log.
//
// Buffering means the status can still be changed when encoding fails, so the
// caller gets a real 500 and the reason is written to the log.
func jsonOK(w http.ResponseWriter, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		log.Printf("api: response encoding failed: %v", err)
		jsonError(w, "encoding_error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(buf.Bytes())
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
