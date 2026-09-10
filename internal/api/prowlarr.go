package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/netguard"
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

// Prowlarr and Jackett are companion apps, normally on localhost or the same
// LAN, so private destinations must stay reachable — the usual "reject private
// addresses" advice would break the ordinary deployment. Redirects are pinned
// instead: neither app has a reason to bounce Yata to another origin, and
// without the pin a host named by the caller could answer 302 to somewhere
// internal and the LAN allowance would carry it.
var indexerManagerPolicy = netguard.Policy{AllowPrivate: true, PinOrigin: true}

var prowlarrClient = netguard.Client(15*time.Second, indexerManagerPolicy)

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
			// A stored credential only travels to the stored origin. Without
			// this, "let the form test an unsaved URL" also means "send the
			// saved Prowlarr key to any host someone names".
			if !netguard.SameOriginStr(req.URL, stored.ProwlarrURL) {
				jsonError(w, "an API key is required when connecting to a different Prowlarr address",
					http.StatusBadRequest)
				return
			}
			req.APIKey = stored.ProwlarrAPIKey
		}
		if req.URL == "" || req.APIKey == "" {
			jsonError(w, "url and api_key are required", http.StatusBadRequest)
			return
		}
		if _, err := netguard.Validate(req.URL, indexerManagerPolicy); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		auditPrivateDestination(d, "prowlarr", req.URL)

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

		existing := d.indexExisting()

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
			entry.AlreadyAdded = existing.has(entry.BaseURL, entry.DefKey)
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

// existingTrackers indexes the configured trackers so an indexer coming back
// from Prowlarr or Jackett is recognised however its URL is spelled.
//
// Matching on the host alone was not enough. A tracker with more than one
// domain is one tracker: RetroFlix is retroflix.net, Prowlarr's stock
// definition ships retroflix.club, and the def already lists the second as an
// alias of the first. Comparing hosts made every import offer to add a
// duplicate of a tracker that was already there — and pre-ticked it, because
// "already added" is also what disables the checkbox.
type existingTrackers struct {
	hosts map[string]bool
	defs  map[string]bool
}

func (d *Deps) indexExisting() existingTrackers {
	e := existingTrackers{hosts: map[string]bool{}, defs: map[string]bool{}}
	for _, t := range d.Cfg.Trackers() {
		e.hosts[normHost(t.URL)] = true
		// The stored tracker carries no def key — that is resolved per request
		// — so resolve it the same way the incoming indexer is resolved, which
		// also means both sides honour the def's aliases.
		if td, ok := d.Reg.TrackerByURL(t.URL); ok {
			e.defs[td.Key] = true
		}
	}
	return e
}

// has reports whether a tracker is already configured, by URL or by def.
func (e existingTrackers) has(rawURL, defKey string) bool {
	if e.hosts[normHost(rawURL)] {
		return true
	}
	return defKey != "" && e.defs[defKey]
}

// normHost lowercases and strips scheme/trailing slash for URL comparison.
func normHost(u string) string {
	u = strings.ToLower(strings.TrimRight(strings.TrimSpace(u), "/"))
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return strings.TrimPrefix(u, "www.")
}
