// tokens.go — Yata-issued READ-ONLY API tokens for integrations (homelab
// dashboards, scripts, other apps). Tokens are accepted ONLY on the read-only
// integration endpoints (see the requireAuthOrToken group in server.go); the
// rest of the API stays session-only, so a leaked token can never modify
// anything or read tracker credentials.
//
// The plaintext token ("yata_" + 40 hex chars) is returned exactly once at
// creation; only its SHA-256 hash is stored. Management (list/create/revoke)
// lives behind the normal session auth.
package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// tokenPrefix marks Yata tokens so they're recognisable in configs and logs.
const tokenPrefix = "yata_"

// tokenNameMax keeps labels presentable in the Settings list.
const tokenNameMax = 64

func registerTokens(r chi.Router, d *Deps) {
	r.Get("/tokens", listTokens(d))
	r.Post("/tokens", createToken(d))
	r.Delete("/tokens/{id}", deleteToken(d))
}

// tokenView is the safe list representation — no hash, no plaintext.
type tokenView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"` // 0 = never used
}

func toTokenView(t store.APIToken) tokenView {
	return tokenView{ID: t.ID, Name: t.Name, Prefix: t.Prefix, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
}

// GET /api/tokens — list token metadata, newest first.
func listTokens(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		list, err := d.DB.ListAPITokens()
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		out := make([]tokenView, 0, len(list))
		for _, t := range list {
			out = append(out, toTokenView(t))
		}
		jsonOK(w, out)
	}
}

// POST /api/tokens {"name": "..."} — create a token. The response carries the
// plaintext token ONCE; it is not stored and cannot be shown again.
func createToken(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		name := strings.TrimSpace(body.Name)
		if name == "" {
			jsonError(w, "name_required", http.StatusBadRequest)
			return
		}
		if len(name) > tokenNameMax {
			name = name[:tokenNameMax]
		}

		raw := make([]byte, 20)
		_, _ = rand.Read(raw)
		token := tokenPrefix + hex.EncodeToString(raw)
		rec := store.APIToken{
			ID:        newID(),
			Name:      name,
			Prefix:    token[:len(tokenPrefix)+8] + "…",
			Hash:      hashToken(token),
			CreatedAt: time.Now().Unix(),
		}
		if err := d.DB.CreateAPIToken(rec); err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		d.logInfof("tokens: created read-only API token %q (%s)", name, rec.Prefix)
		jsonOK(w, map[string]any{"token": token, "info": toTokenView(rec)})
	}
}

// DELETE /api/tokens/{id} — revoke a token immediately.
func deleteToken(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		found, err := d.DB.DeleteAPIToken(id)
		if err != nil {
			jsonError(w, "store_error", http.StatusInternalServerError)
			return
		}
		if !found {
			jsonError(w, "not_found", http.StatusNotFound)
			return
		}
		d.logInfof("tokens: API token revoked (%s)", id)
		jsonOK(w, map[string]any{"ok": true})
	}
}

// ── Token authentication (read-only integration endpoints) ──────────────────

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// tokenFromRequest extracts a presented API token. Precedence: Authorization
// Bearer, X-Api-Token header, ?token= query (documented last resort — query
// strings end up in logs). Only "yata_"-prefixed values are considered so
// tracker Bearer tokens pasted in the wrong place can't be mistaken for ours.
func tokenFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if t := strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")); strings.HasPrefix(t, tokenPrefix) {
			return t
		}
	}
	if t := strings.TrimSpace(r.Header.Get("X-Api-Token")); strings.HasPrefix(t, tokenPrefix) {
		return t
	}
	if t := r.URL.Query().Get("token"); strings.HasPrefix(t, tokenPrefix) {
		return t
	}
	return ""
}

// tokenTouchMinAge throttles last-used writes — a dashboard polling every few
// seconds shouldn't turn into a DB write per request.
const tokenTouchMinAge = 60 * time.Second

// tokenAuthenticated reports whether the request carries a valid API token,
// updating its last-used timestamp (throttled).
func tokenAuthenticated(d *Deps, r *http.Request) bool {
	token := tokenFromRequest(r)
	if token == "" {
		return false
	}
	rec, ok, err := d.DB.APITokenByHash(hashToken(token))
	if err != nil || !ok {
		return false
	}
	if now := time.Now(); now.Sub(time.Unix(rec.LastUsedAt, 0)) >= tokenTouchMinAge {
		_ = d.DB.TouchAPIToken(rec.ID, now)
	}
	return true
}

// requireAuthOrToken gates the read-only integration endpoints: a normal
// login session works (the SPA), and so does a valid API token (integrations).
func requireAuthOrToken(d *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Token first: it also updates "last used", which must tick even on
			// instances without login protection (isAuthenticated is vacuously
			// true there and would short-circuit the token check).
			if tokenAuthenticated(d, r) || isAuthenticated(d, r) {
				next.ServeHTTP(w, r)
				return
			}
			jsonError(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}
