package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// Prowlarr import: users who run Prowlarr have already entered every tracker
// URL + API key there. POST /api/prowlarr/indexers proxies Prowlarr's indexer
// list so the UI can offer test-and-select import (same flow qui uses).
// Nothing is persisted by this endpoint — the frontend creates trackers via
// the normal POST /api/trackers for each selected entry.

func registerProwlarr(r chi.Router, d *Deps) {
	r.Post("/prowlarr/indexers", prowlarrIndexers(d))
}

type prowlarrRequest struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
}

// prowlarrIndexer is the trimmed view returned to the frontend. The Jackett
// import reuses it (same UI) — SessionCookie is Jackett-only, since Jackett
// stores session cookies for cookie-auth indexers.
type prowlarrIndexer struct {
	Name          string `json:"name"`
	Privacy       string `json:"privacy"` // private | semiPrivate | public
	BaseURL       string `json:"base_url"`
	HasAPIKey     bool   `json:"has_api_key"`
	APIKey        string `json:"api_key,omitempty"`
	SessionCookie string `json:"session_cookie,omitempty"`
	DefKey        string `json:"def_key"`       // matched Yata def ("" = manual)
	DefApproval   string `json:"def_approval,omitempty"` // approval status of the matched def
	AlreadyAdded  bool   `json:"already_added"` // URL matches an existing tracker
	Enabled       bool   `json:"enabled"`       // enabled in Prowlarr
}

var prowlarrClient = &http.Client{Timeout: 15 * time.Second}

func prowlarrIndexers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req prowlarrRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		req.URL = strings.TrimRight(strings.TrimSpace(req.URL), "/")
		req.APIKey = strings.TrimSpace(req.APIKey)
		// Fall back to the saved credentials (empty field or mask sentinel =
		// "use what's stored") so a returning user can just hit Fetch.
		stored := d.Cfg.Settings()
		if req.URL == "" {
			req.URL = strings.TrimRight(strings.TrimSpace(stored.ProwlarrURL), "/")
		}
		if req.APIKey == "" || req.APIKey == maskedKey {
			req.APIKey = stored.ProwlarrAPIKey
		}
		if req.URL == "" || req.APIKey == "" {
			jsonError(w, "url and api_key are required", http.StatusBadRequest)
			return
		}

		preq, err := http.NewRequest(http.MethodGet, req.URL+"/api/v1/indexer", nil)
		if err != nil {
			jsonError(w, "request_error", http.StatusBadRequest)
			return
		}
		preq.Header.Set("X-Api-Key", strings.TrimSpace(req.APIKey))
		preq.Header.Set("Accept", "application/json")

		resp, err := prowlarrClient.Do(preq)
		if err != nil {
			jsonError(w, "connection_error", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			// 502 not 401: a 401 from Yata means "session expired" to the SPA.
			jsonError(w, "invalid Prowlarr API key", http.StatusBadGateway)
			return
		}
		if resp.StatusCode != http.StatusOK {
			jsonError(w, fmt.Sprintf("prowlarr http_%d", resp.StatusCode), http.StatusBadGateway)
			return
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			jsonError(w, "read_error", http.StatusBadGateway)
			return
		}

		var raw []struct {
			Name        string   `json:"name"`
			Enable      bool     `json:"enable"`
			Privacy     string   `json:"privacy"`
			IndexerURLs []string `json:"indexerUrls"`
			Fields      []struct {
				Name  string `json:"name"`
				Value any    `json:"value"`
			} `json:"fields"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			jsonError(w, "parse_error", http.StatusBadGateway)
			return
		}

		existing := map[string]bool{}
		for _, t := range d.Cfg.Trackers() {
			existing[normHost(t.URL)] = true
		}

		out := make([]prowlarrIndexer, 0, len(raw))
		for _, ix := range raw {
			entry := prowlarrIndexer{
				Name:    ix.Name,
				Privacy: ix.Privacy,
				Enabled: ix.Enable,
			}
			for _, f := range ix.Fields {
				// Field names vary by indexer schema: native C# indexers use
				// "apiKey"/"baseUrl", Cardigann (YAML) definitions use lowercase
				// "apikey"/"baseUrl" — match case-insensitively.
				switch strings.ToLower(f.Name) {
				case "baseurl":
					if s, ok := f.Value.(string); ok && s != "" {
						entry.BaseURL = strings.TrimRight(s, "/")
					}
				case "apikey", "api_key":
					if s, ok := f.Value.(string); ok && s != "" {
						entry.HasAPIKey = true
						entry.APIKey = s
					}
				}
			}
			if entry.BaseURL == "" && len(ix.IndexerURLs) > 0 {
				entry.BaseURL = strings.TrimRight(ix.IndexerURLs[0], "/")
			}
			if entry.BaseURL == "" {
				continue
			}
			if td, ok := d.Reg.TrackerByURL(entry.BaseURL); ok {
				entry.DefKey = td.Key
				entry.DefApproval = td.ApprovalStatus()
			}
			entry.AlreadyAdded = existing[normHost(entry.BaseURL)]
			out = append(out, entry)
		}

		// The fetch worked — remember the connection so it survives restarts
		// and the section comes prefilled next time.
		if stored.ProwlarrURL != req.URL || stored.ProwlarrAPIKey != req.APIKey {
			s := d.Cfg.Settings()
			s.ProwlarrURL, s.ProwlarrAPIKey = req.URL, req.APIKey
			if err := d.Cfg.UpdateSettings(s); err != nil {
				d.logWarnf("prowlarr: could not save connection settings: %v", err)
			}
		}
		jsonOK(w, out)
	}
}

// normHost lowercases and strips scheme/trailing slash for URL comparison.
func normHost(u string) string {
	u = strings.ToLower(strings.TrimRight(strings.TrimSpace(u), "/"))
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return strings.TrimPrefix(u, "www.")
}
