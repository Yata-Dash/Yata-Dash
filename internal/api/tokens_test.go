package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// mintToken creates a token through the real handler and returns its
// plaintext + id.
func mintToken(t *testing.T, d *Deps, name string) (token, id string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(`{"name":"`+name+`"}`))
	rec := httptest.NewRecorder()
	createToken(d)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("create token: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
		Info  struct {
			ID     string `json:"id"`
			Prefix string `json:"prefix"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Token, out.Info.ID
}

// TestTokenLifecycle covers create → list (no secrets leaked) → revoke.
func TestTokenLifecycle(t *testing.T) {
	d := testDeps(t)

	// Nameless create is rejected.
	rec := httptest.NewRecorder()
	createToken(d)(rec, httptest.NewRequest("POST", "/api/tokens", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("nameless create: status %d, want 400", rec.Code)
	}

	token, id := mintToken(t, d, "Homepage widget")
	if !strings.HasPrefix(token, "yata_") || len(token) != len("yata_")+40 {
		t.Errorf("token format = %q, want yata_ + 40 hex chars", token)
	}

	// List: metadata only — the plaintext and hash must not appear anywhere.
	rec = httptest.NewRecorder()
	listTokens(d)(rec, httptest.NewRequest("GET", "/api/tokens", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "Homepage widget") {
		t.Errorf("list missing token name: %s", body)
	}
	if strings.Contains(body, token) || strings.Contains(body, hashToken(token)) {
		t.Errorf("list leaks token material: %s", body)
	}

	// The prefix shown must actually match the token start.
	var list []tokenView
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || !strings.HasPrefix(token, strings.TrimSuffix(list[0].Prefix, "…")) {
		t.Errorf("prefix %q doesn't match token", list[0].Prefix)
	}

	// Revoke; a second revoke of the same id is a 404.
	if ok, err := d.DB.DeleteAPIToken(id); err != nil || !ok {
		t.Fatalf("revoke: ok=%v err=%v", ok, err)
	}
	if ok, _ := d.DB.DeleteAPIToken(id); ok {
		t.Error("second revoke reported found")
	}
}

// TestTokenAuthGating is the security contract: with an account configured,
// a token unlocks ONLY the read-only integration endpoints — never the
// session-only API — and revocation cuts access immediately.
func TestTokenAuthGating(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	// Configure login protection so the API actually requires auth.
	hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	if err := d.DB.SetUser("admin", string(hash)); err != nil {
		t.Fatal(err)
	}

	token, id := mintToken(t, d, "gating test")

	get := func(path string, hdr map[string]string) int {
		req := httptest.NewRequest("GET", path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	bearer := map[string]string{"Authorization": "Bearer " + token}

	// No credentials → 401 on the integration surface.
	if c := get("/api/summary", nil); c != 401 {
		t.Errorf("summary without auth: %d, want 401", c)
	}
	// All three presentation forms work.
	if c := get("/api/summary", bearer); c != 200 {
		t.Errorf("summary with Bearer: %d, want 200", c)
	}
	if c := get("/api/summary", map[string]string{"X-Api-Token": token}); c != 200 {
		t.Errorf("summary with X-Api-Token: %d, want 200", c)
	}
	if c := get("/api/summary?token="+token, nil); c != 200 {
		t.Errorf("summary with ?token=: %d, want 200", c)
	}
	if c := get("/api/history/series?range=48h", bearer); c != 200 {
		t.Errorf("history/series with Bearer: %d, want 200", c)
	}

	// READ-ONLY GUARANTEE: session-only endpoints reject the token.
	for _, path := range []string{"/api/trackers", "/api/settings", "/api/stats", "/api/tokens", "/api/config/export"} {
		if c := get(path, bearer); c != 401 {
			t.Errorf("token accepted on session-only %s: %d, want 401", path, c)
		}
	}

	// Wrong token → 401; revoked token → 401.
	if c := get("/api/summary", map[string]string{"Authorization": "Bearer yata_" + strings.Repeat("0", 40)}); c != 401 {
		t.Errorf("summary with bogus token: %d, want 401", c)
	}
	if _, err := d.DB.DeleteAPIToken(id); err != nil {
		t.Fatal(err)
	}
	if c := get("/api/summary", bearer); c != 401 {
		t.Errorf("summary with revoked token: %d, want 401", c)
	}
}

// TestSummaryShape seeds a tracker with stored stats and checks the summary
// payload: display strings + GiB numbers + totals, all from stored data.
func TestSummaryShape(t *testing.T) {
	d := testDeps(t)
	tr := models.Tracker{ID: "tr-sum", Name: "Test Tracker", URL: "https://example.org", Type: "unit3d", Enabled: true}
	if err := d.Cfg.AddTracker(tr); err != nil {
		t.Fatal(err)
	}
	err := d.Stats.SaveAPI("tr-sum", map[string]any{
		"username":    "someone",
		"group":       "Power User",
		"uploaded":    "1.00 TiB",
		"downloaded":  "512.00 GiB",
		"buffer":      "512.00 GiB",
		"ratio":       "2.0",
		"seeding":     42,
		"unread_mail": "true",
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	getSummary(d)(rec, httptest.NewRequest("GET", "/api/summary", nil))
	if rec.Code != 200 {
		t.Fatalf("summary: status %d", rec.Code)
	}
	var out struct {
		Totals   summaryTotals    `json:"totals"`
		Trackers []summaryTracker `json:"trackers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Totals.Trackers != 1 || out.Totals.Enabled != 1 {
		t.Errorf("totals counts = %+v", out.Totals)
	}
	if out.Totals.UploadedGiB != 1024 || out.Totals.DownloadedGiB != 512 {
		t.Errorf("totals sizes = up %v down %v, want 1024/512", out.Totals.UploadedGiB, out.Totals.DownloadedGiB)
	}
	if out.Totals.Ratio == nil || *out.Totals.Ratio != 2 {
		t.Errorf("totals ratio = %v, want 2", out.Totals.Ratio)
	}
	st := out.Trackers[0]
	if st.Uploaded != "1.00 TiB" || st.UploadedGiB == nil || *st.UploadedGiB != 1024 {
		t.Errorf("uploaded = %q / %v", st.Uploaded, st.UploadedGiB)
	}
	if st.Username != "someone" || st.Group != "Power User" || !st.UnreadMail {
		t.Errorf("identity fields = %+v", st)
	}
	if st.Seeding == nil || *st.Seeding != 42 {
		t.Errorf("seeding = %v, want 42", st.Seeding)
	}
	if st.Status != "unknown" { // never refreshed since "boot" in this test
		t.Errorf("status = %q, want unknown", st.Status)
	}
	if st.UpdatedAt == 0 {
		t.Error("updated_at not set from stored layer")
	}
}

// TestTokenLastUsedTouch — a token hit updates last_used_at (throttled writes
// are fine here: the first use always writes).
func TestTokenLastUsedTouch(t *testing.T) {
	d := testDeps(t)
	token, id := mintToken(t, d, "touch test")
	req := httptest.NewRequest("GET", "/api/summary", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if !tokenAuthenticated(d, req) {
		t.Fatal("token not accepted")
	}
	list, err := d.DB.ListAPITokens()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v", err)
	}
	if list[0].ID != id || list[0].LastUsedAt == 0 {
		t.Errorf("last_used_at not touched: %+v", list[0])
	}
	if time.Since(time.Unix(list[0].LastUsedAt, 0)) > time.Minute {
		t.Errorf("last_used_at implausible: %d", list[0].LastUsedAt)
	}
}
