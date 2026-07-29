package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Yata-Dash/Yata-Dash/internal/netguard"
)

// QUI is a qBittorrent management UI (github.com/autobrr/qui). Yata can
// display its live torrent stats bars above the tracker table.

func registerQUI(r chi.Router, d *Deps) {
	// POST, not GET, because this endpoint takes a destination from the caller
	// and attaches a stored credential to it. blockCrossSite exempts safe
	// methods, and SameSite=Lax sends the session cookie on a top-level
	// navigation — so as a GET, any page the user visited could open
	// /api/qui/instances?url=http://attacker/ and collect the QUI API key from
	// its own logs. As a POST it is covered by the cross-site check.
	r.Post("/qui/instances", quiInstances(d))
	r.Get("/qui/stats", quiStats(d))
}

// quiPolicy: QUI is a companion app, normally on localhost or the LAN, so
// private destinations have to be allowed. Redirects are pinned to the
// configured origin — a qui instance has no reason to bounce Yata elsewhere,
// and without the pin an attacker-controlled host could answer 302 to
// somewhere internal and the LAN allowance would let it through.
var quiPolicy = netguard.Policy{AllowPrivate: true, PinOrigin: true}

var quiClient = netguard.Client(10*time.Second, quiPolicy)

type quiRequest struct {
	URL string `json:"url"`
	Key string `json:"key"`
}

// POST /api/qui/instances {url, key}
// The optional url/key let the settings form TEST credentials that haven't
// been saved yet ("reload instances" before hitting Save). An empty key, or
// the mask sentinel, means "use the stored key" — but only for the stored
// destination; see quiCredentials.
func quiInstances(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req quiRequest
		if r.Body != nil {
			// An empty body is a valid request meaning "use everything stored".
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
				jsonError(w, "invalid JSON", http.StatusBadRequest)
				return
			}
		}
		set := d.Cfg.Settings()
		target, key, errMsg := quiCredentials(set.QUIURL, set.QUIAPIKey, req)
		if errMsg != "" {
			jsonError(w, errMsg, http.StatusBadRequest)
			return
		}
		if _, err := netguard.Validate(target, quiPolicy); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, status, err := quiFetch(strings.TrimRight(target, "/")+"/api/instances", key)
		if err != nil {
			jsonError(w, err.Error(), upstreamStatus(status))
			return
		}
		var instances []map[string]any
		if err := json.Unmarshal(body, &instances); err != nil {
			jsonError(w, "parse error", http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(instances))
		for _, inst := range instances {
			out = append(out, map[string]any{
				"id":        inst["id"],
				"name":      inst["name"],
				"connected": inst["connected"],
				"host":      inst["host"],
			})
		}
		jsonOK(w, out)
	}
}

// GET /api/qui/stats?id=N — proxies torrent/server stats for one instance.
func quiStats(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		set := d.Cfg.Settings()
		instID := r.URL.Query().Get("id")
		if instID == "" {
			if len(set.QUIEnabledInstances) > 0 {
				instID = fmt.Sprintf("%d", set.QUIEnabledInstances[0])
			} else {
				instID = "1"
			}
		}
		// Instance ids are integers in qui. Enforcing that stops the id from
		// steering the request path — the destination host is fixed here, but
		// a value carrying "../" or "?" still rewrites which endpoint is
		// called and what it is asked for.
		if _, err := strconv.Atoi(instID); err != nil {
			jsonError(w, "invalid instance id", http.StatusBadRequest)
			return
		}
		url := fmt.Sprintf("%s/api/instances/%s/torrents?page=1&limit=1",
			strings.TrimRight(set.QUIURL, "/"), instID)
		body, _, err := quiFetch(url, set.QUIAPIKey)
		if err != nil {
			jsonOK(w, map[string]any{"error": err.Error(), "instance_id": instID})
			return
		}
		var data map[string]any
		if err := json.Unmarshal(body, &data); err != nil {
			jsonOK(w, map[string]any{"error": "parse_error", "instance_id": instID})
			return
		}
		ss, _ := data["serverState"].(map[string]any)
		ts, _ := data["stats"].(map[string]any)
		if ss == nil {
			ss = map[string]any{}
		}
		if ts == nil {
			ts = map[string]any{}
		}
		// Unregistered count (torrents the tracker no longer knows) lives in
		// counts.status, not the stats block. Absent on older qui versions —
		// the response then simply omits the key and the bar hides the pill.
		var unregistered any
		if counts, ok := data["counts"].(map[string]any); ok {
			if status, ok := counts["status"].(map[string]any); ok {
				if v, ok := status["unregistered"]; ok {
					unregistered = v
				}
			}
		}
		jsonOK(w, map[string]any{
			"instance_id":          instID,
			"connection_status":    ss["connection_status"],
			"dl_info_speed":        ss["dl_info_speed"],
			"up_info_speed":        ss["up_info_speed"],
			"dl_rate_limit":        ss["dl_rate_limit"],
			"up_rate_limit":        ss["up_rate_limit"],
			"use_alt_speed_limits": ss["use_alt_speed_limits"],
			"free_space_on_disk":   ss["free_space_on_disk"],
			"global_ratio":         ss["global_ratio"],
			"seeding":              ts["seeding"],
			"downloading":          ts["downloading"],
			"paused":               ts["paused"],
			"errors":               ts["error"],
			"unregistered":         unregistered,
			"checking":             ts["checking"],
			"total_torrents":       ts["total"],
			"total_size":           ts["totalSize"],
		})
	}
}

// quiCredentials decides which destination and which key a request should
// use, and refuses the combination that leaks.
//
// The rule is that a STORED credential only ever travels to the STORED
// origin. Falling back to the saved key for a caller-supplied destination is
// what turns "let the settings form test an unsaved URL" into "hand the key
// to any host someone names". Testing a different QUI still works — the
// caller just has to supply the key for it, which someone configuring their
// own instance has and an attacker forging a request does not.
func quiCredentials(storedURL, storedKey string, req quiRequest) (target, key, errMsg string) {
	target = strings.TrimSpace(req.URL)
	if target == "" {
		target = strings.TrimSpace(storedURL)
	}
	if target == "" {
		return "", "", "QUI not configured"
	}
	key = strings.TrimSpace(req.Key)
	if key == "" || key == maskedKey {
		if !netguard.SameOriginStr(target, storedURL) {
			return "", "", "an API key is required when testing a different QUI address"
		}
		key = storedKey
	}
	return target, key, ""
}

func quiFetch(url, apiKey string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	resp, err := quiClient.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("connection_error")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("http_%d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return body, http.StatusOK, nil
}
