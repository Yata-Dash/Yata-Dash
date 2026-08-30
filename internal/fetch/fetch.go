// Package fetch retrieves tracker stats from APIs. The fetcher used for a
// tracker is selected by its type def's api.kind — all tracker-specific
// details (endpoints, auth, field mappings) come from the defs registry.
//
// Every fetcher returns a flat map of canonical field names → values. Field
// name normalisation (api_field_map) is applied here so downstream code only
// ever sees canonical names.
package fetch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/ident"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/netguard"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
	"github.com/Yata-Dash/Yata-Dash/internal/redact"
)

// Error classifies a fetch failure for the UI.
type Error struct {
	Kind string // no_key | no_username | no_def | timeout | connection_error | http_NNN | parse_error | api_error
	Err  error
}

// Error renders the failure with credentials removed. The underlying error is
// very often a *url.Error, whose message carries the full request URL — and
// for a def using api_key_query that URL contains the tracker's API key. This
// string reaches both the log and the tracker-test modal, so it is redacted
// here rather than trusting every consumer to remember.
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s", e.Kind, redact.Error(e.Err))
	}
	return e.Kind
}

func errf(kind string, err error) *Error { return &Error{Kind: kind, Err: err} }

// Client fetches stats using definitions from the registry.
type Client struct {
	Registry *defs.Registry
	HTTP     *http.Client
	// HTTPDefBase is used only when a DEFINITION chose the destination rather
	// than the user — see DefBaseURLPolicy.
	HTTPDefBase  *http.Client
	TestDataPath string // path to test_data.json for demo trackers
}

// TrackerPolicy constrains where a tracker fetch may connect.
//
// The destination comes from a definition rather than from a request, so this
// guards a narrower surface than the integration endpoints do — but a
// definition is data, and community-contributed definitions are the direction
// of travel. What it enforces is the scheme (http/https only) and the
// addresses that are never a legitimate tracker: link-local, and so the cloud
// instance-metadata endpoint, plus multicast.
//
// Private addresses stay allowed HERE because this policy governs the URL the
// user typed when they added the tracker. Pointing Yata at an address on your
// own network is a decision about your own machine, and it is not hypothetical:
// a tracker reached over Tailscale resolves into 100.64.0.0/10, and one behind
// a personal domain with split-horizon DNS resolves to an RFC1918 address.
// Both are ordinary setups that a public-only policy would break, and neither
// instruction can arrive from a stranger.
//
// The half that CAN arrive from a stranger — a def's api.base_url — is held to
// DefBaseURLPolicy instead. That split is the point: the restriction lands on
// the input Yata does not control.
var TrackerPolicy = netguard.Policy{AllowPrivate: true}

// DefBaseURLPolicy constrains the one case where a DEFINITION, rather than the
// user, chooses the destination: `api.base_url`, which overrides the URL the
// user typed when they added the tracker.
//
// That override is legitimate — BTN's API is on a different hostname from its
// website — but it means a def file decides where Yata sends a request, and
// defs are data. Bundled ones are reviewed here; contributed ones are the
// direction of travel, and a `base_url` aimed at 192.168.1.1 in an otherwise
// correct-looking def is not something a reviewer checking field mappings
// would reliably catch.
//
// So this half gets no private allowance. A tracker's API is on the public
// internet by definition, which makes the restriction free: no legitimate def
// can want an address inside the user's house. The URL the USER typed keeps
// TrackerPolicy — pointing Yata at your own network is your decision to make,
// and unlike a def it cannot arrive from a stranger.
var DefBaseURLPolicy = netguard.Policy{}

// NewClient builds a Client with a sane default HTTP timeout.
func NewClient(reg *defs.Registry, testDataPath string) *Client {
	return &Client{
		Registry:     reg,
		HTTP:         netguard.Client(30*time.Second, TrackerPolicy),
		HTTPDefBase:  netguard.Client(30*time.Second, DefBaseURLPolicy),
		TestDataPath: testDataPath,
	}
}

// defBaseClient returns the client for a def-chosen destination, falling back
// to building one so a hand-constructed Client cannot silently lose the
// restriction by leaving the field nil.
func (c *Client) defBaseClient() *http.Client {
	if c.HTTPDefBase != nil {
		return c.HTTPDefBase
	}
	return netguard.Client(30*time.Second, DefBaseURLPolicy)
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
	case "gazelle_json":
		data, ferr = c.fetchGazelleJSON(t)
	case "gazelle_games":
		data, ferr = c.fetchGazelleGames(t)
	case "custom":
		data, ferr = c.fetchCustom(t)
	case "none":
		// Scrape-only, or a tracker with no definition and no type chosen yet:
		// an empty API layer either way, and no request made.
		return map[string]any{}, nil
	case "unit3d":
		data, ferr = c.fetchUnit3D(t)
	default:
		// A kind no fetcher handles is a definition-authoring mistake. This
		// used to fall through to UNIT3D, which hid the mistake behind
		// plausible-looking HTTP failures against the wrong endpoints.
		return nil, errf("unknown_api_kind_"+kind, nil)
	}
	if ferr != nil {
		return nil, ferr
	}
	fieldMap := c.Registry.ResolveAPIFieldMap(t.URL, t.Type)
	return normalizeStrings(defs.NormalizeAPIFields(fieldMap, data)), nil
}

// normalizeStrings applies the canonical-string cleanups to every fetcher's
// output. It runs HERE, after NormalizeAPIFields, because the cleanup is keyed
// by CANONICAL name and the field map has only just produced those — a def
// mapping joined_at → join_date still has the raw key inside its fetcher.
//
// join_date is what forced this. pathways parses it with a strict
// time.Parse("2006-01-02"), so an untrimmed "2026-08-21T03:58:59+00:00" fails
// and the account reads as brand new: every age requirement silently unmet,
// with no error anywhere. fetchCustom already normalised its own values (it
// resolves canonical names itself), so for that path this is a no-op — the
// cleanups are idempotent.
func normalizeStrings(data map[string]any) map[string]any {
	for k, v := range data {
		if str, ok := v.(string); ok {
			data[k] = normalizeCanonicalString(k, str)
		}
	}
	return data
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
	convertCoreBytes(data)
	normalizeActiveEvents(data)
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
	if ferr != nil && (ferr.Kind == "http_401" || ferr.Kind == "http_403") {
		data, ferr = c.getJSON(url+"?api_token="+key, nil, identify)
	}
	if ferr != nil {
		return nil, ferr
	}
	data = unwrapUnit3DEnvelope(data)
	// Both here, not in fetchUnit3D: mergeExtended copies every key the core
	// response doesn't already have, so an api_key object on the EXTENDED
	// endpoint would be merged into the stats and persisted. Sanitising only the
	// core response made that strictly more likely, because deleting the core
	// copy removes the overlap that would otherwise have blocked the merge.
	extractAPIKeyExpiry(data)
	return data, nil
}

// unit3dEnvelopeMarkers are field names used to recognise an envelope.
// Requiring one of them inside "data" means a response that happens to carry an
// unrelated top-level "data" object is left alone.
//
// It covers BOTH endpoints getUnit3D serves. A tracker that wraps /api/user
// almost certainly wraps its extended-stats endpoint too — it is a site-wide
// API convention, not a per-endpoint one — and an extended response carries
// supplementary fields (seed_size, avg_seed_time, …) rather than the core ones.
// With core-only markers its envelope would go unrecognised, and mergeExtended
// would then copy the literal "data" map into the stats as a field: every
// extended stat lost, and a nested object persisted to the stat layer.
//
// Raw names, since this runs before NormalizeAPIFields — hence seedbonus
// alongside bonus_points.
var unit3dEnvelopeMarkers = []string{
	// core /api/user
	"username", "group", "uploaded", "downloaded", "ratio", "buffer",
	"seeding", "leeching", "seedbonus", "bonus_points", "hit_and_runs",
	// extended stats (ExtendedStatsSpec)
	"seed_size", "avg_seed_time", "real_uploaded", "real_downloaded",
	"real_ratio", "fl_tokens", "unread_mail",
}

// unwrapUnit3DEnvelope flattens the {"data": {...}, "api_key": {...}} shape LST
// moved to in 2026-08 back into the flat map the rest of the pipeline expects.
//
// This matters more than it looks. NormalizeAPIFields, convertCoreBytes and
// every def's api_field_map look fields up at the TOP level, so an envelope
// makes all of them miss: the request succeeds, parses, and yields a tracker
// reporting nothing at all. There is no error to notice — which is exactly how
// LST went quiet.
//
// Unwrapping in getUnit3D covers /api/user and the extended-stats endpoint from
// one place, and leaves every downstream step and every existing def untouched.
//
// Siblings of "data" are kept so metadata outside the envelope (api_key today,
// whatever follows later) stays reachable. Inner keys win a collision: the
// envelope holds the stats, the outer level describes the request.
func unwrapUnit3DEnvelope(raw map[string]any) map[string]any {
	inner, ok := raw["data"].(map[string]any)
	if !ok {
		return raw
	}
	marked := false
	for _, m := range unit3dEnvelopeMarkers {
		if _, found := inner[m]; found {
			marked = true
			break
		}
	}
	if !marked {
		return raw
	}
	out := make(map[string]any, len(inner)+len(raw))
	for k, v := range inner {
		out[k] = v
	}
	for k, v := range raw {
		if k == "data" {
			continue
		}
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}

// APIKeyExpiryField carries the expiry of an expiring API token, for trackers
// that issue them (LST, 2026-08). The tracker's own RFC3339 string is kept
// as-is; turning it into "expires in N days" is a presentation decision and
// belongs above the fetcher, not here.
const APIKeyExpiryField = "api_key_expires_at"

// extractAPIKeyExpiry promotes api_key.expires_at to a canonical top-level
// field and removes the nested object.
//
// The removal is deliberate, not tidiness: whatever remains in this map is
// persisted to the API stat layer and its history. Today the object holds only
// an expiry, but it is the tracker's description of the CREDENTIAL, and a
// future field there could put the token itself into stored stats. Dropping it
// at the boundary means that can never happen by accident.
func extractAPIKeyExpiry(data map[string]any) {
	meta, ok := data["api_key"].(map[string]any)
	if !ok {
		return
	}
	if s, ok := meta["expires_at"].(string); ok && strings.TrimSpace(s) != "" {
		data[APIKeyExpiryField] = s
	}
	delete(data, "api_key")
}

// coreByteFields are the /api/user values that carry byte counts. Everything
// else UNIT3D returns is already a count, a ratio or a display string.
//
// The last three arrived with the expanded UNIT3D user stats (Zenith 2026-07,
// now proposed upstream): they used to exist only behind a supplementary
// endpoint, where ExtendedStatsSpec.ByteFields converted them, so a tracker
// serving them from /api/user instead sent bytes straight through — a seed size
// rendering as "3005578784855" rather than "2.73 TiB". Listing them here is
// safe for every other tracker: only NUMBERS are converted, so an install that
// doesn't send the field, or sends it pre-formatted, is untouched.
var coreByteFields = []string{
	"uploaded", "downloaded", "buffer",
	"seed_size", "real_uploaded", "real_downloaded",
}

// convertCoreBytes turns raw byte counts from /api/user into size strings.
//
// Two response shapes exist in the wild, and the Go type tells them apart:
// a stock UNIT3D install returns these fields as JSON NUMBERS of bytes, while
// some forks (ReelFliX, Upload.cx) pre-format them as "620.01 GiB" STRINGS.
// Only numbers are converted, so a fork's strings pass through untouched and
// nothing can be converted twice. Without this, a stock install's byte counts
// reached the UI as bare numbers and were read as though they were already
// GiB — the extended-stats path (mergeExtended) converts its own byte fields,
// but core values are authoritative there and so were never touched.
//
// The residual assumption is that a NUMBER always means bytes. No known fork
// returns a number in any other unit; one that did would be misread by a
// factor of ~1e9, so it would be obvious rather than subtly wrong.
//
// Negative buffers (downloaded above uploaded) fall out as "0.00 B" from
// BytesToSize, matching what the custom fetcher's buffer_from_bytes already
// does — worth keeping identical, not "fixing" here.
func convertCoreBytes(data map[string]any) {
	for _, f := range coreByteFields {
		if n, ok := data[f].(float64); ok {
			data[f] = parse.BytesToSize(int64(n))
		}
	}
}

// ── Active events ────────────────────────────────────────────────────────────

// activeEventsField is the structured event list newer UNIT3D installs return
// from /api/user: one object per running promotion, covering both global states
// (site-wide freeleech) and rows from the events table (upload contests), each
// with a name, status, window, and sometimes a URL, prize table and the user's
// own progress.
//
// Yata's canonical event fields predate it and are deliberately flat — a single
// banner string plus one countdown target — because that is all a grid card or
// a table row has room for, and alert rules are written against them. Rather
// than replace them, normalizeActiveEvents DERIVES them from the list and keeps
// the list itself, so every existing surface keeps working while the detail
// page can show the full picture.
const activeEventsField = "active_events"

// normalizeActiveEvents fills active_event / active_event_ends_at from the
// structured list, and rewrites the list into a form the frontend can render
// without date parsing.
//
// Only LIVE events feed the banner and the countdown: an event that hasn't
// started yet is real and worth listing, but announcing it as though freeleech
// were running right now would be wrong. Timestamps become unix seconds (the
// countdown ticker's unit) and null fields are dropped, so the UI can test for
// presence instead of null-checking every key. Everything else passes through
// untouched — a tracker adding a field to these objects tomorrow should reach
// the UI without a Yata release.
func normalizeActiveEvents(data map[string]any) {
	raw, ok := data[activeEventsField].([]any)
	if !ok {
		return
	}
	events := make([]any, 0, len(raw))
	var names []string
	var soonestEnd int64
	for _, item := range raw {
		ev, ok := item.(map[string]any)
		if !ok {
			continue
		}
		clean := make(map[string]any, len(ev))
		for k, v := range ev {
			if v == nil {
				continue
			}
			clean[k] = v
		}
		endsAt := eventUnix(clean["ends_at"])
		if endsAt > 0 {
			clean["ends_at"] = endsAt
		}
		if startsAt := eventUnix(clean["starts_at"]); startsAt > 0 {
			clean["starts_at"] = startsAt
		}
		events = append(events, clean)

		status, _ := clean["status"].(string)
		if status != "" && !strings.EqualFold(status, "live") {
			continue
		}
		if name := eventName(clean); name != "" {
			names = append(names, name)
		}
		if endsAt > 0 && (soonestEnd == 0 || endsAt < soonestEnd) {
			soonestEnd = endsAt
		}
	}
	if len(events) == 0 {
		// An empty list means "no events", not "unknown" — leaving the key in
		// would store an empty array that reads as a field the tracker reports.
		delete(data, activeEventsField)
		return
	}
	data[activeEventsField] = events
	if len(names) > 0 {
		data["active_event"] = strings.Join(names, " · ")
	}
	if soonestEnd > 0 {
		// The SOONEST end, so the countdown always names the next thing to
		// expire rather than an outer window that is still weeks away.
		data["active_event_ends_at"] = soonestEnd
	}
}

// eventName is an event's display name, falling back to a prettified type slug
// ("upload_contest" → "Upload Contest") when the tracker leaves name null.
func eventName(ev map[string]any) string {
	if s, _ := ev["name"].(string); strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	slug, _ := ev["type"].(string)
	if slug = strings.TrimSpace(slug); slug == "" {
		return ""
	}
	parts := strings.Split(strings.ReplaceAll(slug, "-", "_"), "_")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// eventUnix reads an event timestamp as unix seconds. Accepts the RFC3339 the
// UNIT3D schema uses ("2026-08-01T00:00:00+00:00") and a bare number, so a fork
// serialising these as epoch seconds needs no special case. 0 = unusable.
func eventUnix(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
			if parsed, err := time.Parse(layout, s); err == nil {
				return parsed.Unix()
			}
		}
	}
	return 0
}

// apiErrorMessage interprets a custom API's top-level "error" field. It
// reports whether the response is genuinely an error and, if so, the best
// message available.
//
// Only a TRUTHY value counts: a string with content, a non-empty error object,
// or boolean true. Empty strings, false, null, 0 and a missing field are all
// "no error" — the shapes APIs use when they include the field unconditionally.
func apiErrorMessage(v any) (string, bool) {
	const fallback = "API error"
	switch value := v.(type) {
	case nil:
		return "", false
	case string:
		if strings.TrimSpace(value) == "" {
			return "", false
		}
		return value, true
	case bool:
		return fallback, value
	case float64:
		return fallback, value != 0
	case map[string]any:
		if len(value) == 0 {
			return "", false
		}
		if text, ok := value["message"].(string); ok && strings.TrimSpace(text) != "" {
			return text, true
		}
		return fallback, true
	case []any:
		return fallback, len(value) > 0
	}
	return fallback, true // unknown non-nil shape — safer to surface it
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

// Gazelle JSON is the ajax.php API used by Redacted-style Gazelle sites.
type gazelleJSONEnvelope struct {
	Status   string          `json:"status"`
	Response json.RawMessage `json:"response"`
	Error    string          `json:"error,omitempty"`
}

type gazelleJSONIndex struct {
	Username    string  `json:"username"`
	ID          int     `json:"id"`
	GiftTokens  float64 `json:"giftTokens"`
	MeritTokens float64 `json:"meritTokens"`
}

type gazelleJSONUser struct {
	Username string `json:"username"`
	Stats    struct {
		JoinedDate string `json:"joinedDate"`
		Uploaded   int64  `json:"uploaded"`
		Downloaded int64  `json:"downloaded"`
		// Ratio and requiredRatio are numbers on Redacted and Orpheus, but some
		// forks send them as JSON strings ("38.17869") — accept either.
		Ratio         any   `json:"ratio"`
		Buffer        int64 `json:"buffer"`
		RequiredRatio any   `json:"requiredRatio"`
	} `json:"stats"`
	Personal struct {
		Class   string `json:"class"`
		Warned  bool   `json:"warned"`
		Enabled bool   `json:"enabled"`
	} `json:"personal"`
	Community struct {
		Posts          int `json:"posts"`
		RequestsFilled int `json:"requestsFilled"`
		PerfectFLACs   int `json:"perfectFlacs"`
		Uploaded       int `json:"uploaded"`
		Groups         int `json:"groups"`
		Seeding        int `json:"seeding"`
		Leeching       int `json:"leeching"`
		Snatched       int `json:"snatched"`
		Invited        int `json:"invited"`
	} `json:"community"`
}

type gazelleJSONCommunityStats struct {
	Leeching    any `json:"leeching"`
	Seeding     any `json:"seeding"`
	Snatched    any `json:"snatched"`
	SeedingSize any `json:"seedingsize"`
}

func (c *Client) getGazelleJSON(url string, headers map[string]string, identify string, out any) *Error {
	body, ferr := c.getBody(url, headers, identify)
	if ferr != nil {
		return ferr
	}
	var env gazelleJSONEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		// The SHAPE of the response, never its contents. Diagnosing a parse
		// failure needs to know which keys arrived and what types they held;
		// the values are the account's own details (a Gazelle user endpoint
		// carries email, IRC key and inviter) and this text is logged.
		return errf("parse_error", fmt.Errorf("%w — body shape: %s", err, redact.JSONShape(body)))
	}
	if env.Status != "success" {
		message := env.Error
		if message == "" {
			message = "api_error"
		}
		return errf("api_error", fmt.Errorf("%s", message))
	}
	if err := json.Unmarshal(env.Response, out); err != nil {
		return errf("parse_error", err)
	}
	return nil
}

func (c *Client) fetchGazelleJSON(t models.Tracker) (map[string]any, *Error) {
	if strings.TrimSpace(t.APIKey) == "" {
		return nil, errf("no_key", nil)
	}
	identify := c.identify(t)
	headers := map[string]string{
		"Accept":        "application/json",
		"Authorization": strings.TrimSpace(t.APIKey),
	}
	base := strings.TrimRight(t.URL, "/") + "/ajax.php?action="

	var index gazelleJSONIndex
	if ferr := c.getGazelleJSON(base+"index", headers, identify, &index); ferr != nil {
		return nil, ferr
	}
	if index.ID <= 0 {
		return nil, errf("api_error", fmt.Errorf("index response missing user id"))
	}

	var user gazelleJSONUser
	if ferr := c.getGazelleJSON(fmt.Sprintf("%suser&id=%d", base, index.ID), headers, identify, &user); ferr != nil {
		return nil, ferr
	}
	// community_stats is supplementary (its only live use below is seed_size —
	// leeching/seeding/snatched already come from `user` above), and not every
	// Gazelle fork supports it (verified failing on Orpheus). A failure here
	// must not discard the index+user data that already succeeded.
	var community gazelleJSONCommunityStats
	_ = c.getGazelleJSON(fmt.Sprintf("%scommunity_stats&userid=%d", base, index.ID), headers, identify, &community)

	joinDate := user.Stats.JoinedDate
	if len(joinDate) >= 10 {
		joinDate = joinDate[:10] // "2026-03-05 01:18:59" → date only
	}
	out := map[string]any{
		"username":         user.Username,
		"user_id":          fmt.Sprintf("%d", index.ID),
		"group":            user.Personal.Class,
		"uploaded":         parse.BytesToSize(user.Stats.Uploaded),
		"downloaded":       parse.BytesToSize(user.Stats.Downloaded),
		"buffer":           parse.BytesToSize(user.Stats.Buffer),
		"ratio":            parse.AnyFloat(user.Stats.Ratio),
		"required_ratio":   parse.AnyFloat(user.Stats.RequiredRatio),
		"join_date":        joinDate,
		"warnings":         0,
		"seeding":          user.Community.Seeding,
		"leeching":         user.Community.Leeching,
		"snatched":         user.Community.Snatched,
		"users_invited":    user.Community.Invited,
		"uploads_approved": user.Community.Uploaded,
		"requests_filled":  user.Community.RequestsFilled,
		"forum_posts":      user.Community.Posts,
		"groups_uploaded":  user.Community.Groups,
		"perfect_flacs":    user.Community.PerfectFLACs,
		"fl_tokens":        index.GiftTokens + index.MeritTokens,
	}
	if user.Personal.Warned {
		out["warnings"] = 1
	}
	if size, ok := community.SeedingSize.(string); ok && strings.TrimSpace(size) != "" {
		out["seed_size"] = size
	}
	return out, nil
}

// GazelleGames exposes its own Gazelle-derived API at api.php. The filename
// matches the fetchGazelle (Anthelion) endpoint and the envelope matches
// ajax.php, but that is where the resemblance stops: different query grammar,
// different response shapes, and the stats need three chained calls. Hence a
// third fetcher rather than a branch inside either of the others.
type gazelleGamesQuickUser struct {
	Username  string `json:"username"`
	ID        int    `json:"id"`
	UserStats struct {
		Class string `json:"class"`
	} `json:"userstats"`
}

type gazelleGamesRatio struct {
	Uploaded      any `json:"uploaded"`
	Downloaded    any `json:"downloaded"`
	Ratio         any `json:"ratio"`
	Buffer        any `json:"buffer"`
	Disposable    any `json:"disposable"`
	RequiredRatio any `json:"reqratio"`
}

type gazelleGamesUser struct {
	Stats struct {
		JoinedDate string  `json:"joinedDate"`
		ShareScore float64 `json:"shareScore"`
		Gold       float64 `json:"gold"`
	} `json:"stats"`
	Personal struct {
		HNRs    *int `json:"hnrs"`
		Warned  bool `json:"warned"`
		Invites *int `json:"invites"`
	} `json:"personal"`
	Community struct {
		HourlyGold     *float64 `json:"hourlyGold"`
		ActualPosts    *int     `json:"actualPosts"`
		IRCActualLines *int     `json:"ircActualLines"`
		Seeding        *int     `json:"seeding"`
		Leeching       *int     `json:"leeching"`
		Snatched       *int     `json:"snatched"`
		UniqueSnatched *int     `json:"uniqueSnatched"`
		SeedSize       *int64   `json:"seedSize"`
	} `json:"community"`
	Achievements struct {
		NextLevel       string `json:"nextLevel"`
		TotalPoints     int    `json:"totalPoints"`
		PointsToNextLvl int    `json:"pointsToNextLvl"`
	} `json:"achievements"`
}

func (c *Client) getGazelleGames(url, key, identify string, out any) *Error {
	body, ferr := c.getBody(url, map[string]string{
		"Accept":    "application/json",
		"X-API-Key": key,
	}, identify)
	if ferr != nil {
		return ferr
	}
	var env gazelleJSONEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return errf("parse_error", err)
	}
	if env.Status != "success" {
		message := env.Error
		if message == "" {
			message = "api_error"
		}
		return errf("api_error", fmt.Errorf("%s", message))
	}
	if err := json.Unmarshal(env.Response, out); err != nil {
		return errf("parse_error", err)
	}
	return nil
}

func (c *Client) fetchGazelleGames(t models.Tracker) (map[string]any, *Error) {
	key := strings.TrimSpace(t.APIKey)
	if key == "" {
		return nil, errf("no_key", nil)
	}
	base := strings.TrimRight(t.URL, "/") + "/api.php?request="
	identify := c.identify(t)

	var quick gazelleGamesQuickUser
	if ferr := c.getGazelleGames(base+"quick_user", key, identify, &quick); ferr != nil {
		return nil, ferr
	}
	if quick.ID <= 0 {
		return nil, errf("api_error", fmt.Errorf("quick_user response missing user id"))
	}
	var ratio gazelleGamesRatio
	if ferr := c.getGazelleGames(base+"user_stats_ratio", key, identify, &ratio); ferr != nil {
		return nil, ferr
	}
	// user is supplementary (join date, gold/share score, achievements,
	// HNRs/invites) — the core uploaded/downloaded/ratio/buffer numbers above
	// already succeeded, so a failure here must not discard them.
	var user gazelleGamesUser
	_ = c.getGazelleGames(fmt.Sprintf("%suser&id=%d", base, quick.ID), key, identify, &user)

	joinDate := user.Stats.JoinedDate
	if len(joinDate) >= 10 {
		joinDate = joinDate[:10]
	}
	out := map[string]any{
		"username":             quick.Username,
		"user_id":              fmt.Sprintf("%d", quick.ID),
		"group":                quick.UserStats.Class,
		"uploaded":             parse.BytesToSize(int64(parse.AnyFloat(ratio.Uploaded))),
		"downloaded":           parse.BytesToSize(int64(parse.AnyFloat(ratio.Downloaded))),
		"buffer":               parse.BytesToSize(int64(parse.AnyFloat(ratio.Buffer))),
		"disposable":           parse.BytesToSize(int64(parse.AnyFloat(ratio.Disposable))),
		"ratio":                parse.AnyFloat(ratio.Ratio),
		"required_ratio":       parse.AnyFloat(ratio.RequiredRatio),
		"join_date":            joinDate,
		"bonus_points":         user.Stats.Gold,
		"share_score":          user.Stats.ShareScore,
		"warnings":             0,
		"achievement_points":   user.Achievements.TotalPoints,
		"points_to_next_level": user.Achievements.PointsToNextLvl,
		"next_group":           user.Achievements.NextLevel,
	}
	if user.Personal.Warned {
		out["warnings"] = 1
	}
	if user.Personal.HNRs != nil {
		out["hit_and_runs"] = *user.Personal.HNRs
	}
	if user.Personal.Invites != nil {
		out["invites"] = *user.Personal.Invites
	}
	if user.Community.HourlyGold != nil {
		out["hourly_gold"] = *user.Community.HourlyGold
	}
	if user.Community.ActualPosts != nil {
		out["forum_posts"] = *user.Community.ActualPosts
	}
	if user.Community.IRCActualLines != nil {
		out["irc_lines"] = *user.Community.IRCActualLines
	}
	if user.Community.Seeding != nil {
		out["seeding"] = *user.Community.Seeding
	}
	if user.Community.Leeching != nil {
		out["leeching"] = *user.Community.Leeching
	}
	if user.Community.Snatched != nil {
		out["snatched"] = *user.Community.Snatched
	}
	if user.Community.UniqueSnatched != nil {
		out["unique_snatched"] = *user.Community.UniqueSnatched
	}
	if user.Community.SeedSize != nil {
		out["seed_size"] = parse.BytesToSize(*user.Community.SeedSize)
	}
	return out, nil
}

// ── Custom (fully data-driven) ───────────────────────────────────────────────

func (c *Client) fetchCustom(t models.Tracker) (map[string]any, *Error) {
	// The description may come from the tracker def, from its type (a family
	// sharing one endpoint), or from both merged together.
	api := c.Registry.ResolveCustomAPI(t.URL, t.Type)
	if api == nil || api.Path == "" {
		return nil, errf("no_def", fmt.Errorf("no custom API def for %s", t.URL))
	}
	path := api.Path
	if strings.Contains(path, "{username}") {
		if strings.TrimSpace(t.Username) == "" {
			return nil, errf("no_username", nil)
		}
		path = strings.ReplaceAll(path, "{username}", url.QueryEscape(strings.TrimSpace(t.Username)))
	}
	method := http.MethodGet
	var requestBody io.Reader
	if api.AuthMethod == "api_key_json_rpc" {
		if strings.TrimSpace(t.APIKey) == "" {
			return nil, errf("no_key", nil)
		}
		if strings.TrimSpace(api.JSONRPCMethod) == "" {
			return nil, errf("request_error", fmt.Errorf("missing JSON-RPC method"))
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  api.JSONRPCMethod,
			"params":  []string{t.APIKey},
			"id":      1,
		})
		if marshalErr != nil {
			return nil, errf("request_error", marshalErr)
		}
		method = http.MethodPost
		requestBody = bytes.NewReader(payload)
	}
	// Which client is used follows where the ADDRESS came from, not what is
	// being fetched: a def-chosen destination is held to DefBaseURLPolicy.
	// Enforcement is at connect time, in the dialer, so a hostname that
	// resolves inward is caught as well as a literal private address.
	baseURL, client := t.URL, c.HTTP
	if strings.TrimSpace(api.BaseURL) != "" {
		baseURL, client = api.BaseURL, c.defBaseClient()
	}
	req, err := http.NewRequest(method, strings.TrimRight(baseURL, "/")+path, requestBody)
	if err != nil {
		return nil, errf("request_error", err)
	}
	req.Header.Set("Accept", "application/json")
	if api.AuthMethod == "api_key_json_rpc" {
		req.Header.Set("Content-Type", "application/json")
	}
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
	case "api_key_json_rpc":
		// The key is already embedded as the first positional request param.
	}

	resp, err := client.Do(req)
	if err != nil {
		if netguard.IsBlocked(err) {
			// A def naming a destination Yata refuses is a def bug, not a
			// network failure, and reads very differently in the UI.
			return nil, errf("blocked_destination", err)
		}
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
	// An "error" key only means failure when it actually carries one. Plenty
	// of APIs include the field on EVERY response and leave it empty on
	// success ("", false, null, 0), so treating its mere presence as a failure
	// would reject perfectly good payloads — with a useless "API error" at
	// that, since there is no message to report.
	if msg, failed := apiErrorMessage(raw["error"]); failed {
		return nil, errf("api_error", fmt.Errorf("%s", msg))
	}
	if api.SuccessField != "" && fmt.Sprint(nested(raw, api.SuccessField)) != api.SuccessValue {
		return nil, errf("api_error", fmt.Errorf("unexpected %s value", api.SuccessField))
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
			out[canonical] = normalizeCanonicalString(canonical, val)
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

	// Unix-second fields → YYYY-MM-DD.
	for jsonPath, canonical := range api.UnixFields {
		v := nested(raw, jsonPath)
		if v == nil {
			continue
		}
		seconds := int64(parse.AnyFloat(v))
		if seconds > 0 {
			out[canonical] = time.Unix(seconds, 0).UTC().Format("2006-01-02")
		}
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

// normalizeCanonicalString cleans up string values per canonical field, so
// defs don't each need conversion machinery:
//   - join_date: ISO datetimes ("2022-01-01T00:00:00+00:00") → date only.
//   - ratio/real_ratio: "Inf"/"∞" (downloaded = 0) → "Infinity", which the
//     frontend parses to a real Infinity and renders as ∞ (green).
//
// Shared by the custom and UNIT3D fetchers. It was custom-only until a UNIT3D
// tracker (HHD) started returning a join date from /api/user: stock UNIT3D
// doesn't report one at all, so the UNIT3D path had never needed it.
func normalizeCanonicalString(canonical, v string) string {
	switch canonical {
	case "join_date":
		if len(v) > 10 && (v[10] == 'T' || v[10] == ' ') {
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
