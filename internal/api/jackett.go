package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// Jackett import: like the Prowlarr import, but Jackett's admin API is
// cookie-authenticated (its dashboard login), not API-keyed. Flow per
// Jackett's source (Server/Controllers/UIController.cs):
//
//  1. GET  /UI/Login          — sets the test cookie; when NO admin password
//     is configured Jackett auto-authenticates this session.
//  2. POST /UI/Dashboard      — form field "password"; only needed when an
//     admin password is set.
//  3. GET  /api/v2.0/indexers?configured=true       — the indexer list.
//  4. GET  /api/v2.0/indexers/{id}/Config           — per-indexer stored
//     settings; credentials (apikey / cookie) live here, not in the list.
//
// Nothing is persisted by this endpoint except the connection settings (URL +
// admin password, on success) — the frontend creates trackers via the normal
// POST /api/trackers for each selected entry.

func registerJackett(r chi.Router, d *Deps) {
	r.Post("/jackett/indexers", jackettIndexers(d))
}

type jackettRequest struct {
	URL           string `json:"url"`
	AdminPassword string `json:"admin_password"`
}

// jackettListEntry is Jackett's own indexer DTO (the fields we use).
type jackettListEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"` // private | semi-private | public
	Configured bool   `json:"configured"`
	SiteLink   string `json:"site_link"`
}

// jackettConfigItem is one entry of GET /indexers/{id}/Config.
type jackettConfigItem struct {
	ID    string `json:"id"`
	Value any    `json:"value"`
}

func jackettIndexers(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req jackettRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		req.URL = strings.TrimRight(strings.TrimSpace(req.URL), "/")
		req.AdminPassword = strings.TrimSpace(req.AdminPassword)
		// Empty field or mask sentinel = "use what's stored".
		stored := d.Cfg.Settings()
		if req.URL == "" {
			req.URL = strings.TrimRight(strings.TrimSpace(stored.JackettURL), "/")
		}
		if req.AdminPassword == "" || req.AdminPassword == maskedKey {
			req.AdminPassword = stored.JackettAdminPassword
		}
		if req.URL == "" {
			jsonError(w, "url is required", http.StatusBadRequest)
			return
		}

		client, err := jackettClient()
		if err != nil {
			jsonError(w, "client_error", http.StatusInternalServerError)
			return
		}
		if errMsg, status := jackettLogin(client, req.URL, req.AdminPassword); errMsg != "" {
			jsonError(w, errMsg, upstreamStatus(status))
			return
		}

		list, errMsg, status := jackettList(client, req.URL)
		if errMsg != "" {
			jsonError(w, errMsg, upstreamStatus(status))
			return
		}

		existing := map[string]bool{}
		for _, t := range d.Cfg.Trackers() {
			existing[normHost(t.URL)] = true
		}

		out := make([]prowlarrIndexer, 0, len(list))
		for _, ix := range list {
			if !ix.Configured {
				continue
			}
			entry := prowlarrIndexer{
				Name:    ix.Name,
				Privacy: ix.Type,
				Enabled: true, // configured = usable in Jackett
				BaseURL: strings.TrimRight(ix.SiteLink, "/"),
			}
			// Credentials live in the per-indexer config, not the list. A
			// config fetch failing just means we import without credentials.
			for _, item := range jackettConfig(client, req.URL, ix.ID) {
				s, ok := item.Value.(string)
				if !ok || s == "" {
					continue
				}
				switch strings.ToLower(item.ID) {
				case "apikey", "api_key":
					entry.HasAPIKey = true
					entry.APIKey = s
				case "cookie", "cookieheader":
					entry.SessionCookie = s
				case "sitelink", "site_link":
					// The user-selected mirror — more accurate than site_link.
					entry.BaseURL = strings.TrimRight(s, "/")
				}
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

		// The fetch worked — remember the connection like the Prowlarr import.
		if stored.JackettURL != req.URL || stored.JackettAdminPassword != req.AdminPassword {
			s := d.Cfg.Settings()
			s.JackettURL, s.JackettAdminPassword = req.URL, req.AdminPassword
			if err := d.Cfg.UpdateSettings(s); err != nil {
				d.logWarnf("jackett: could not save connection settings: %v", err)
			}
		}
		jsonOK(w, out)
	}
}

// jackettClient builds an HTTP client with a cookie jar (Jackett auth is a
// session cookie issued by the login flow).
func jackettClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &http.Client{Timeout: 15 * time.Second, Jar: jar}, nil
}

// jackettLogin authenticates the client's session. Returns ("", 0) on
// success, or an error message + HTTP status for the caller to relay.
func jackettLogin(client *http.Client, base, password string) (string, int) {
	// Step 1: GET /UI/Login — sets Jackett's test cookie and, when no admin
	// password is configured, auto-authenticates this session.
	resp, err := client.Get(base + "/UI/Login")
	if err != nil {
		return "connection_error", http.StatusBadGateway
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()

	// Step 2: POST the admin password if one was provided.
	if password != "" {
		form := url.Values{"password": {password}}
		resp, err := client.PostForm(base+"/UI/Dashboard", form)
		if err != nil {
			return "connection_error", http.StatusBadGateway
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
	return "", 0
}

// jackettList fetches the configured-indexer list. A non-JSON response means
// the session isn't authenticated (Jackett redirects to the HTML login page).
func jackettList(client *http.Client, base string) ([]jackettListEntry, string, int) {
	resp, err := client.Get(base + "/api/v2.0/indexers?configured=true")
	if err != nil {
		return nil, "connection_error", http.StatusBadGateway
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "read_error", http.StatusBadGateway
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Sprintf("jackett http_%d", resp.StatusCode), http.StatusBadGateway
	}
	var list []jackettListEntry
	if err := json.Unmarshal(body, &list); err != nil {
		// HTML instead of JSON = we were bounced to the login page.
		if strings.Contains(strings.ToLower(string(body[:min(512, len(body))])), "<!doctype") ||
			strings.HasPrefix(strings.TrimSpace(string(body)), "<") {
			return nil, "invalid Jackett admin password", http.StatusUnauthorized
		}
		return nil, "parse_error", http.StatusBadGateway
	}
	return list, "", 0
}

// jackettConfig fetches one indexer's stored settings. Errors return nil —
// the import then simply carries no credentials for that indexer.
func jackettConfig(client *http.Client, base, id string) []jackettConfigItem {
	resp, err := client.Get(base + "/api/v2.0/indexers/" + url.PathEscape(id) + "/Config")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var items []jackettConfigItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil
	}
	return items
}
