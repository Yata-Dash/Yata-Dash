// Package fetch retrieves tracker stats from APIs. The fetcher used for a
// tracker is selected by its type def's api.kind — all tracker-specific
// details (endpoints, auth, field mappings) come from the defs registry.
//
// Every fetcher returns a flat map of canonical field names → values. Field
// name normalisation (api_field_map) is applied here so downstream code only
// ever sees canonical names.
package fetch

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/ident"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
)

// Error classifies a fetch failure for the UI.
type Error struct {
	Kind string // no_key | no_username | no_def | timeout | connection_error | http_NNN | parse_error | api_error
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Kind, e.Err)
	}
	return e.Kind
}

func errf(kind string, err error) *Error { return &Error{Kind: kind, Err: err} }

// Client fetches stats using definitions from the registry.
type Client struct {
	Registry     *defs.Registry
	HTTP         *http.Client
	TestDataPath string // path to test_data.json for demo trackers
}

// NewClient builds a Client with a sane default HTTP timeout.
func NewClient(reg *defs.Registry, testDataPath string) *Client {
	return &Client{
		Registry:     reg,
		HTTP:         &http.Client{Timeout: 30 * time.Second},
		TestDataPath: testDataPath,
	}
}

// Fetch retrieves stats for one tracker, dispatching on the type's api.kind.
// The returned map uses canonical field names.
func (c *Client) Fetch(t models.Tracker) (map[string]any, *Error) {
	kind := c.Registry.APIKind(t.URL, t.Type)

	var data map[string]any
	var ferr *Error
	switch kind {
	case "demo":
		data, ferr = c.fetchDemo(t)
	case "gazelle":
		data, ferr = c.fetchGazelle(t)
	case "custom":
		data, ferr = c.fetchCustom(t)
	case "none":
		return map[string]any{}, nil // scrape-only type: empty API layer
	default: // "unit3d"
		data, ferr = c.fetchUnit3D(t)
	}
	if ferr != nil {
		return nil, ferr
	}
	fieldMap := c.Registry.ResolveAPIFieldMap(t.URL, t.Type)
	return defs.NormalizeAPIFields(fieldMap, data), nil
}

// ── Unit3D ───────────────────────────────────────────────────────────────────

func (c *Client) fetchUnit3D(t models.Tracker) (map[string]any, *Error) {
	if strings.TrimSpace(t.APIKey) == "" {
		return nil, errf("no_key", nil)
	}
	base := strings.TrimRight(t.URL, "/")
	id := c.identify(t)
	data, ferr := c.getUnit3D(base+"/api/user", t.APIKey, id)
	if ferr != nil {
		return nil, ferr
	}
	// Supplementary extended-stats endpoint (opt-in per def). Newer UNIT3D
	// trackers expose formerly scrape-only stats (seed size, seed times, unread
	// flags, …) here, letting them turn scraping off entirely. Best-effort: a
	// failure here never fails the whole fetch — the core stats still return.
	if ext := c.Registry.ExtendedStats(t.URL, t.Type); ext != nil && ext.Path != "" {
		if extData, eErr := c.getUnit3D(base+ext.Path, t.APIKey, id); eErr == nil {
			mergeExtended(data, extData, ext.ByteFields)
		}
	}
	return data, nil
}

// getUnit3D fetches a UNIT3D JSON endpoint using Bearer auth, which keeps the
// API token out of the request URL — and therefore out of the tracker's access
// logs and any intermediary proxy/CDN. If a tracker rejects the header (401/403,
// e.g. an older UNIT3D instance), it transparently falls back to the classic
// ?api_token= query form so no setup can regress. Every current UNIT3D tracker
// we've probed accepts Bearer, so the fallback effectively never fires.
func (c *Client) getUnit3D(url, key, identify string) (map[string]any, *Error) {
	data, ferr := c.getJSON(url, map[string]string{"Authorization": "Bearer " + key}, identify)
	if ferr == nil {
		return data, nil
	}
	if ferr.Kind == "http_401" || ferr.Kind == "http_403" {
		return c.getJSON(url+"?api_token="+key, nil, identify)
	}
	return nil, ferr
}

// mergeExtended folds an extended-stats response into the core /api/user map.
// Core values are authoritative (never overwritten — e.g. username appears in
// both); fields named in byteFields are converted from raw bytes to size
// strings; everything else (seconds, counts, ratios, bools) passes through.
func mergeExtended(core, ext map[string]any, byteFields []string) {
	isByte := make(map[string]bool, len(byteFields))
	for _, f := range byteFields {
		isByte[f] = true
	}
	for k, v := range ext {
		if _, exists := core[k]; exists {
			continue // core /api/user wins on any overlap
		}
		switch {
		case isByte[k]:
			core[k] = parse.BytesToSize(int64(parse.AnyFloat(v)))
		default:
			if b, ok := v.(bool); ok {
				// Present booleans (e.g. unread_mail) as the "true"/"false"
				// strings the scrape presence-flag path already produces, so the
				// UI's unread-flag rows render identically whatever the source.
				core[k] = fmt.Sprintf("%t", b)
			} else {
				core[k] = v
			}
		}
	}
}

// identify resolves the def-level traffic-identification mode for a tracker
// ("ua" default / "header" / "none") — API requests identify themselves the
// same way scrapes do, so staff can monitor ALL of Yata's traffic.
func (c *Client) identify(t models.Tracker) string {
	return c.Registry.ResolveScrape(t.URL, t.Type).Identify
}

// ── Gazelle ──────────────────────────────────────────────────────────────────

type gazelleResponse struct {
	Status   string `json:"status"`
	Response struct {
		ID         int    `json:"ID"`
		Username   string `json:"Username"`
		Class      string `json:"Class"`
		Uploaded   int64  `json:"Uploaded"`
		Downloaded int64  `json:"Downloaded"`
		SeedCount  int    `json:"SeedCount"`
		Invites    int    `json:"Invites"`
		JoinDate   string `json:"JoinDate"`
		Snatched   int    `json:"Snatched"`
	} `json:"response"`
	Error string `json:"error,omitempty"`
}

func (c *Client) fetchGazelle(t models.Tracker) (map[string]any, *Error) {
	if strings.TrimSpace(t.APIKey) == "" {
		return nil, errf("no_key", nil)
	}
	if strings.TrimSpace(t.Username) == "" {
		return nil, errf("no_username", nil)
	}
	apiURL := fmt.Sprintf(
		"%s/api.php?action=user&apikey=%s&method=getuserinfo&type=username&user=%s",
		strings.TrimRight(t.URL, "/"), t.APIKey, t.Username)

	body, ferr := c.getBody(apiURL, nil, c.identify(t))
	if ferr != nil {
		return nil, ferr
	}
	var raw gazelleResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errf("parse_error", err)
	}
	if raw.Status != "success" {
		msg := raw.Error
		if msg == "" {
			msg = "api_error"
		}
		return nil, errf("api_error", fmt.Errorf("%s", msg))
	}
	r := raw.Response

	var ratio float64
	if r.Downloaded > 0 {
		ratio = float64(r.Uploaded) / float64(r.Downloaded)
	}
	bufBytes := max(r.Uploaded-r.Downloaded, 0)
	joinDate := r.JoinDate
	if len(joinDate) >= 10 {
		joinDate = joinDate[:10] // "2026-03-05 01:18:59" → date only
	}
	out := map[string]any{
		"username":   r.Username,
		"group":      r.Class,
		"uploaded":   parse.BytesToSize(r.Uploaded),
		"downloaded": parse.BytesToSize(r.Downloaded),
		"buffer":     parse.BytesToSize(bufBytes),
		"ratio":      ratio,
		"seeding":    r.SeedCount,
		"invites":    fmt.Sprintf("%d", r.Invites),
		"snatched":   fmt.Sprintf("%d", r.Snatched),
		"join_date":  joinDate,
	}
	// user_id drives the ID-based profile URL (/user.php?id=N). Not rendered as
	// a stat row (frontend NON_ROW_FIELDS) — consumed by profileURL().
	if r.ID > 0 {
		out["user_id"] = fmt.Sprintf("%d", r.ID)
	}
	return out, nil
}

// ── Custom (fully data-driven) ───────────────────────────────────────────────

func (c *Client) fetchCustom(t models.Tracker) (map[string]any, *Error) {
	td, found := c.Registry.TrackerByURL(t.URL)
	if !found || td.API == nil {
		return nil, errf("no_def", fmt.Errorf("no custom API def for %s", t.URL))
	}
	api := td.API

	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(t.URL, "/")+api.Path, nil)
	if err != nil {
		return nil, errf("request_error", err)
	}
	req.Header.Set("Accept", "application/json")
	ident.Apply(req, c.identify(t))

	switch api.AuthMethod {
	case "session_cookie":
		if strings.TrimSpace(t.SessionCookie) == "" {
			return nil, errf("no_key", nil)
		}
		req.Header.Set("Cookie", api.CookieName+"="+strings.TrimSpace(t.SessionCookie))
	case "api_key_query":
		if strings.TrimSpace(t.APIKey) == "" {
			return nil, errf("no_key", nil)
		}
		q := req.URL.Query()
		param := api.APIKeyParam
		if param == "" {
			param = "api_token"
		}
		q.Set(param, t.APIKey)
		req.URL.RawQuery = q.Encode()
	case "api_key_header":
		if strings.TrimSpace(t.APIKey) == "" {
			return nil, errf("no_key", nil)
		}
		req.Header.Set("Authorization", "Bearer "+t.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, errf(classifyNetErr(err), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errf(fmt.Sprintf("http_%d", resp.StatusCode), fmt.Errorf("http %d", resp.StatusCode))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errf("read_error", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errf("parse_error", err)
	}

	out := map[string]any{}

	// Direct field mappings (dot-notation paths).
	for jsonPath, canonical := range api.FieldMap {
		v := nested(raw, jsonPath)
		if v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			out[canonical] = normalizeCustomString(canonical, val)
		case float64:
			// Ratio/points fields stay float; other numerics are counts.
			if canonical == "ratio" || canonical == "bonus_points" || canonical == "fl_tokens" {
				out[canonical] = val
			} else {
				out[canonical] = int(val)
			}
		}
	}

	// Summed count fields.
	for canonical, paths := range api.SumFields {
		var total float64
		for _, p := range paths {
			if v := nested(raw, p); v != nil {
				total += parse.AnyFloat(v)
			}
		}
		out[canonical] = int(total)
	}

	// Byte fields → size strings (raw values kept for buffer calc).
	rawBytes := map[string]int64{}
	for jsonPath, canonical := range api.ByteFields {
		v := nested(raw, jsonPath)
		if v == nil {
			continue
		}
		b := int64(parse.AnyFloat(v))
		rawBytes[canonical] = b
		out[canonical] = parse.BytesToSize(b)
	}

	// Summed byte fields → size strings.
	for canonical, paths := range api.SumBytesFields {
		var total int64
		for _, p := range paths {
			if v := nested(raw, p); v != nil {
				total += int64(parse.AnyFloat(v))
			}
		}
		out[canonical] = parse.BytesToSize(total)
	}

	if api.BufferFromBytes {
		out["buffer"] = parse.BytesToSize(max(rawBytes["uploaded"]-rawBytes["downloaded"], 0))
	}

	if api.RatioFromBytes {
		if _, mapped := out["ratio"]; !mapped {
			up, down := rawBytes["uploaded"], rawBytes["downloaded"]
			switch {
			case down > 0:
				out["ratio"] = float64(up) / float64(down)
			case up > 0:
				out["ratio"] = "Infinity" // nothing downloaded yet → ∞
			}
			// both zero: no ratio row — a 0/0 account has no meaningful ratio
		}
	}

	// Bool flags: a truthy value (a non-zero count, JSON true, or a non-empty
	// string) → "true". Lets an unread-COUNT drive the unread_mail flag.
	for jsonPath, canonical := range api.BoolFields {
		if v := nested(raw, jsonPath); v != nil {
			out[canonical] = strconv.FormatBool(anyTruthy(v))
		}
	}

	// Membership class id/name → a group NAME from the def's groups. Some APIs
	// report the rank as a numeric class ("class": 3) rather than its name.
	if api.ClassField != "" && len(api.ClassMap) > 0 {
		if v := nested(raw, api.ClassField); v != nil {
			if name, ok := api.ClassMap[classKey(v)]; ok {
				out["group"] = name
			}
		}
	}
	return out, nil
}

// classKey renders a class value as a ClassMap lookup key: a JSON number
// becomes its integer form (3.0 → "3"), a string is used as-is.
func classKey(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.Itoa(int(n))
	case string:
		return n
	default:
		return fmt.Sprintf("%v", v)
	}
}

// anyTruthy reports whether a JSON value is "set": a non-zero number, a true
// boolean, or a string that isn't empty/"false"/"0".
func anyTruthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != "" && t != "0" && !strings.EqualFold(t, "false")
	default:
		return false
	}
}

// normalizeCustomString cleans up string values from custom APIs per
// canonical field, so defs don't each need conversion machinery:
//   - join_date: ISO datetimes ("2022-01-01T00:00:00+00:00") → date only.
//   - ratio/real_ratio: "Inf"/"∞" (downloaded = 0) → "Infinity", which the
//     frontend parses to a real Infinity and renders as ∞ (green).
func normalizeCustomString(canonical, v string) string {
	switch canonical {
	case "join_date":
		if len(v) > 10 && v[10] == 'T' {
			return v[:10]
		}
	case "ratio", "real_ratio":
		if strings.EqualFold(v, "inf") || v == "∞" {
			return "Infinity"
		}
	}
	return v
}

// nested traverses a map using a dot-notation path, e.g. "leeching.count".
func nested(m map[string]any, path string) any {
	parts := strings.SplitN(path, ".", 2)
	val, ok := m[parts[0]]
	if !ok {
		return nil
	}
	if len(parts) == 1 {
		return val
	}
	sub, ok := val.(map[string]any)
	if !ok {
		return nil
	}
	return nested(sub, parts[1])
}

// ── Shared HTTP helpers ──────────────────────────────────────────────────────

func (c *Client) getBody(url string, headers map[string]string, identify string) ([]byte, *Error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, errf("request_error", err)
	}
	ident.Apply(req, identify)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, errf(classifyNetErr(err), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errf(fmt.Sprintf("http_%d", resp.StatusCode), fmt.Errorf("http %d", resp.StatusCode))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errf("read_error", err)
	}
	return body, nil
}

func (c *Client) getJSON(url string, headers map[string]string, identify string) (map[string]any, *Error) {
	body, ferr := c.getBody(url, headers, identify)
	if ferr != nil {
		return nil, ferr
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, errf("parse_error", err)
	}
	return data, nil
}

func classifyNetErr(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") {
		return "timeout"
	}
	return "connection_error"
}
