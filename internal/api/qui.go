package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// QUI is a qBittorrent management UI (github.com/autobrr/qui). Yata can
// display its live torrent stats bars above the tracker table.

func registerQUI(r chi.Router, d *Deps) {
	r.Get("/qui/instances", quiInstances(d))
	r.Get("/qui/stats", quiStats(d))
}

var quiClient = &http.Client{Timeout: 10 * time.Second}

// GET /api/qui/instances?url=…&key=…
// The optional url/key query params let the settings form TEST credentials
// that haven't been saved yet ("reload instances" before hitting Save).
// A key equal to the mask sentinel means "use the stored key".
func quiInstances(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		set := d.Cfg.Settings()
		url, key := set.QUIURL, set.QUIAPIKey
		if q := r.URL.Query().Get("url"); q != "" {
			url = q
		}
		if q := r.URL.Query().Get("key"); q != "" && q != maskedKey {
			key = q
		}
		if url == "" {
			jsonError(w, "QUI not configured", http.StatusBadRequest)
			return
		}
		body, status, err := quiFetch(url+"/api/instances", key)
		if err != nil {
			jsonError(w, err.Error(), status)
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
		url := fmt.Sprintf("%s/api/instances/%s/torrents?page=1&limit=1", set.QUIURL, instID)
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
