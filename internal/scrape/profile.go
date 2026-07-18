// Package scrape contains the multi-strategy HTML profile scraper and the
// rate-limit policy engine.
//
// The scraper itself is tracker-agnostic: every tracker-specific detail
// (extra labels, profile path, event title class, stat card classes) arrives
// via the Spec, which is resolved from the external JSON defs. The base
// label vocabulary below is generic English label variants shared by many
// tracker layouts — it contains no tracker-specific strings.
//
// All strategies were battle-tested in v1 — keep semantics when refactoring.
package scrape

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"

	"github.com/Yata-Dash/Yata-Dash/internal/defs"
	"github.com/Yata-Dash/Yata-Dash/internal/ident"
	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
)

// Spec is everything the scraper needs to know about a tracker, resolved
// from the defs registry by the caller.
type Spec struct {
	// Labels merged over the base vocabulary (type + tracker extra labels).
	ExtraLabels map[string]string
	// ProfilePath with "{username}" placeholder; "" = "/users/{username}".
	ProfilePath string
	// EventTitleClass extracts the event banner title by CSS class.
	EventTitleClass string
	// StatCardClasses enables the label/value class-pair strategy.
	StatCardClasses *defs.StatCardClasses
	// Gazelle enables Gazelle-specific behaviour (ID-based profile URL via
	// API lookup, id-keyed <li> extraction). Keyed off the type's api.kind.
	Gazelle bool
	// KnownUserID fills "{id}" profile paths on NON-gazelle trackers (from a
	// previous scrape's user_id stat). Empty → the id is discovered from the
	// site header (the logged-in user's own profile link).
	KnownUserID string
	// PresenceFlags detect boolean header states (unread mail/notifications)
	// by element presence — zero extra requests, the header is in the page.
	PresenceFlags map[string]defs.PresenceFlag
	// Identify is the def-resolved traffic-identification mode ("ua" default,
	// "header", "none") — see internal/ident.
	Identify string
}

// Error is a scrape failure with an HTTP-ish status for the API layer.
type Error struct {
	Status int
	Kind   string
}

func (e *Error) Error() string { return e.Kind }

func serr(status int, kind string) *Error { return &Error{Status: status, Kind: kind} }

// ── Base label vocabulary (generic, NOT tracker-specific) ────────────────────

var labelMap = map[string]string{
	// Main stats (used when the profile is the only available source).
	"uploaded":           "uploaded",
	"total uploaded":     "uploaded",
	"data uploaded":      "uploaded",
	"downloaded":         "downloaded",
	"total downloaded":   "downloaded",
	"data downloaded":    "downloaded",
	"ratio":              "ratio",
	"upload ratio":       "ratio",
	"seeding":            "seeding",
	"seeding torrents":   "seeding",
	"currently seeding":  "seeding",
	"leeching":           "leeching",
	"leeching torrents":  "leeching",
	"currently leeching": "leeching",
	"hit and runs":       "hit_and_runs",
	"hit & runs":         "hit_and_runs",
	"h&rs":               "hit_and_runs",
	"hnrs":               "hit_and_runs",
	"bonus points":       "bonus_points",
	// Title-attribute labels (Unit3D top-nav ratio bar, e.g. <li title="Upload">).
	"upload":          "uploaded",
	"download":        "downloaded",
	"my bonus points": "bonus_points",
	"my fl tokens":    "fl_tokens",
	"my buffer":       "buffer",
	// Extended stats.
	"buffer":                        "buffer",
	"total seeding":                 "seeding",
	"total leeching":                "leeching",
	"seeding size":                  "seed_size",
	"total seeding size":            "seed_size",
	"seed size":                     "seed_size",
	"average seedtime":              "avg_seed_time",
	"avg seedtime":                  "avg_seed_time",
	"avg seed time":                 "avg_seed_time",
	"average seed time":             "avg_seed_time",
	"total seedtime":                "total_seedtime",
	"total seed time":               "total_seedtime",
	"warnings":                      "warnings",
	"active warnings":               "warnings",
	"invites":                       "invites",
	"freeleech tokens":              "fl_tokens",
	"fl tokens":                     "fl_tokens",
	"freeleech token":               "fl_tokens",
	"tokens":                        "fl_tokens",
	"registration date":             "join_date",
	"member since":                  "join_date",
	"registered":                    "join_date",
	"joined":                        "join_date",
	"join date":                     "join_date",
	"account created":               "join_date",
	"real ratio":                    "real_ratio",
	"uploads":                       "uploads_approved",
	"approved uploads":              "uploads_approved",
	"uploaded torrents":             "uploads_approved",
	"uploaded (non-anonymous)":      "uploads_approved",
	"non-anonymous uploads":         "uploads_approved",
	"non anonymous uploads":         "uploads_approved",
	"total uploads":                 "uploads_approved",
	"requests filled":               "requests_filled",
	"requests filled by user":       "requests_filled",
	"filled requests":               "requests_filled",
	"filled members requests":       "requests_filled",
	"filled member requests":        "requests_filled",
	"member requests filled":        "requests_filled",
	"requests you have filled":      "requests_filled",
	"total requests filled":         "requests_filled",
	"upload snatches":               "upload_snatches",
	"total uploads (non-anonymous)": "uploads_approved",
	"total uploads(non-anonymous)":  "uploads_approved",
	"uploads (non-anonymous)":       "uploads_approved",
	"total non-anonymous uploads":   "uploads_approved",
}

// Field validators — protect single-word labels from matching nav-link text.
var intFields = map[string]bool{
	"warnings": true, "invites": true, "fl_tokens": true,
	"uploads_approved": true, "requests_filled": true,
	"seeding": true, "leeching": true, "hit_and_runs": true, "bonus_points": true,
}

var sizeFields = map[string]bool{
	"uploaded": true, "downloaded": true, "buffer": true, "seed_size": true,
}

var floatFields = map[string]bool{
	"ratio": true, "real_ratio": true,
}

var sizeUnitRe = regexp.MustCompile(`(?i)\d\s*(TiB|GiB|MiB|KiB|TB|GB|MB|KB|\bB\b)`)

func validForField(key, v string) bool {
	if intFields[key] && !containsDigit(v) {
		return false
	}
	if sizeFields[key] && !sizeUnitRe.MatchString(v) {
		// "∞" is a legitimate buffer/size display on some trackers.
		if strings.TrimSpace(v) != "∞" {
			return false
		}
	}
	if floatFields[key] {
		if _, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err != nil {
			return false
		}
	}
	return true
}

// ─────────────────────────────────────────────────────────────────────────────
// Entry point
// ─────────────────────────────────────────────────────────────────────────────

var httpClient = &http.Client{Timeout: 30 * time.Second}

// mergedLabels combines the base vocabulary with tracker/type extra labels.
func mergedLabels(extra map[string]string) map[string]string {
	labels := make(map[string]string, len(labelMap)+len(extra))
	for k, v := range labelMap {
		labels[k] = v
	}
	for k, v := range extra {
		labels[k] = v
	}
	return labels
}

// ExtractFromHTML runs the full extraction pipeline on already-fetched HTML.
// Used by Profile after its HTTP fetch, and directly by tests/dev tools that
// feed saved reference pages (reference/ folder) — never by live code paths
// against the reference files.
func ExtractFromHTML(rawHTML string, spec Spec) map[string]string {
	labels := mergedLabels(spec.ExtraLabels)
	result := extractStats(rawHTML, labels, spec.EventTitleClass)
	if scc := spec.StatCardClasses; scc != nil {
		extractStatCardPairs(rawHTML, labels, scc.Label, scc.Value, result)
	}
	if spec.Gazelle {
		extractGazelleIDStats(rawHTML, result)
	}
	postProcess(result)
	if v, ok := result["seed_size"]; ok && v != "" {
		result["seed_size"] = parse.NormalizeSeedSize(v)
	}
	extractPresenceFlags(rawHTML, spec.PresenceFlags, result)
	return result
}

// extractPresenceFlags detects boolean page states by element presence: for
// each flag, find an <a> whose href (query/fragment stripped) ends with the
// flag's LinkSuffix; the field becomes "true" when the anchor contains the
// Marker element, "false" when the anchor exists without one. Anchor absent
// → the field is NOT set — an unrecognised layout must never fake a "false"
// ("you read everything"). Pages often render the header twice (desktop +
// mobile nav) — "true" from any copy wins.
func extractPresenceFlags(rawHTML string, flags map[string]defs.PresenceFlag, result map[string]string) {
	if len(flags) == 0 {
		return
	}
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return
	}
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "a" {
			return
		}
		href := attr(n, "href")
		if href == "" {
			return
		}
		if i := strings.IndexAny(href, "?#"); i >= 0 {
			href = href[:i]
		}
		href = strings.TrimRight(href, "/")
		for field, f := range flags {
			if f.LinkSuffix == "" || f.Marker == "" || !strings.HasSuffix(href, f.LinkSuffix) {
				continue
			}
			if hasDescendantElement(n, f.Marker) {
				result[field] = "true"
			} else if result[field] != "true" {
				result[field] = "false"
			}
		}
	})
}

// hasDescendantElement reports whether n contains an element named `name`.
func hasDescendantElement(n *html.Node, name string) bool {
	found := false
	walkNodes(n, func(c *html.Node) {
		if c != n && c.Type == html.ElementNode && c.Data == name {
			found = true
		}
	})
	return found
}

// Profile scrapes the tracker profile page and returns a flat canonical-field
// map. The caller is responsible for policy checks (rate limits, disable
// flags) — this function only does the HTTP + extraction work.
func Profile(t models.Tracker, spec Spec) (map[string]string, *Error) {
	if strings.TrimSpace(t.Username) == "" {
		return nil, serr(http.StatusBadRequest, "no_username")
	}
	// Defence in depth: the policy layer blocks cookie-less scrapes, but
	// guard here too — an unauthenticated request gets the login page and
	// the extractor would pull garbage from it.
	if strings.TrimSpace(t.SessionCookie) == "" {
		return nil, serr(http.StatusBadRequest, "no_cookie")
	}

	baseURL := strings.TrimRight(t.URL, "/")
	path := spec.ProfilePath
	if path == "" {
		path = "/users/{username}"
	}
	profileURL := baseURL + strings.ReplaceAll(path, "{username}", t.Username)

	// Gazelle: resolve the ID-based profile URL and capture authoritative API
	// values (invites, join_date, snatched) that override scraped values.
	var gz *gazelleProfileData
	if spec.Gazelle {
		if strings.TrimSpace(t.APIKey) == "" {
			return nil, serr(http.StatusBadRequest, "no_key")
		}
		gd, gerr := fetchGazelleProfileData(t, spec.Identify)
		if gerr != nil {
			return nil, gerr
		}
		gz = gd
		profileURL = baseURL + "/user.php?id=" + strconv.Itoa(gd.UserID)
	}

	// Non-gazelle "{id}" profile paths (TBDev-family /userdetails.php?id={id}):
	// use the id from a previous scrape when known, otherwise discover it from
	// the site header — every page links the logged-in user's own profile.
	// The resolved id is stored as user_id so future scrapes (and the UI's
	// profile link) skip discovery.
	userID := strings.TrimSpace(spec.KnownUserID)
	if !spec.Gazelle && strings.Contains(path, "{id}") {
		if userID == "" {
			uid, derr := discoverUserID(baseURL, path, t, spec.Identify)
			if derr != nil {
				return nil, derr
			}
			userID = uid
		}
		profileURL = baseURL + strings.ReplaceAll(path, "{id}", userID)
	}

	req, _ := http.NewRequest(http.MethodGet, profileURL, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	ident.Apply(req, spec.Identify)
	if cookie := strings.TrimSpace(t.SessionCookie); cookie != "" {
		req.Header.Set("Cookie", cookie)
		req.Header.Set("Referer", baseURL+"/")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			return nil, serr(http.StatusGatewayTimeout, "timeout")
		}
		return nil, serr(http.StatusBadGateway, "connection_error")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, serr(401, "session_expired")
	case http.StatusForbidden:
		return nil, serr(403, "forbidden")
	case http.StatusNotFound:
		return nil, serr(404, "user_not_found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, serr(resp.StatusCode, fmt.Sprintf("http_%d", resp.StatusCode))
	}
	// An invalid/expired cookie doesn't 401 on most trackers — they redirect
	// the profile URL to the login page, which the client follows to a clean
	// 200. Catch it by the FINAL URL (Unit3D /login, TBDev/Gazelle /login.php)
	// so it reports session_expired instead of silently scraping 0 fields.
	if finalPath := strings.ToLower(resp.Request.URL.Path); strings.Contains(finalPath, "login") {
		return nil, serr(401, "session_expired")
	}

	rawHTML, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, serr(http.StatusInternalServerError, "read_error")
	}

	result := ExtractFromHTML(string(rawHTML), spec)
	// A real profile page always yields SOMETHING. Zero extracted fields on a
	// 200 means we got an interstitial (anti-bot page, maintenance page, a
	// login form that didn't redirect) — an error the user should see, not an
	// "ok — 0 fields" that quietly never updates their stats.
	if len(result) == 0 {
		return nil, serr(http.StatusBadGateway, "empty_scrape")
	}

	// Gazelle: API values are authoritative for these fields.
	if spec.Gazelle && gz != nil {
		result["invites"] = fmt.Sprintf("%d", gz.Invites)
		result["join_date"] = gz.JoinDate
		result["snatched"] = fmt.Sprintf("%d", gz.Snatched)
	}
	// Persist the resolved user id (SaveScrape replaces the whole layer, so
	// it must be re-included every time or the next scrape re-discovers it).
	if userID != "" {
		result["user_id"] = userID
	}
	return result, nil
}

// discoverUserID fetches the tracker's base page with the user's session
// cookie and finds the logged-in user's own profile link: an anchor whose
// href matches the "{id}" profile path and whose text equals the configured
// username. This resolves the numeric user id on TBDev-family sites whose
// APIs don't expose it. Tracker-agnostic — driven purely by profile_path.
func discoverUserID(baseURL, path string, t models.Tracker, identify string) (string, *Error) {
	// "/userdetails.php?id={id}" → match hrefs containing "userdetails.php?id=".
	prefix := strings.TrimPrefix(path[:strings.Index(path, "{id}")], "/")
	if prefix == "" {
		return "", serr(http.StatusBadRequest, "bad_profile_path")
	}

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	ident.Apply(req, identify)
	req.Header.Set("Cookie", strings.TrimSpace(t.SessionCookie))

	resp, err := httpClient.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			return "", serr(http.StatusGatewayTimeout, "timeout")
		}
		return "", serr(http.StatusBadGateway, "connection_error")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", serr(resp.StatusCode, fmt.Sprintf("http_%d", resp.StatusCode))
	}
	rawHTML, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", serr(http.StatusInternalServerError, "read_error")
	}
	doc, err := html.Parse(strings.NewReader(string(rawHTML)))
	if err != nil {
		return "", serr(http.StatusInternalServerError, "parse_error")
	}

	wantName := strings.TrimSpace(strings.ToLower(t.Username))
	found := ""
	walkNodes(doc, func(n *html.Node) {
		if found != "" || n.Type != html.ElementNode || n.Data != "a" {
			return
		}
		href := attr(n, "href")
		idx := strings.Index(href, prefix)
		if idx < 0 {
			return
		}
		id := digitRe.FindString(href[idx+len(prefix):])
		if id == "" {
			return
		}
		if strings.TrimSpace(strings.ToLower(nodeText(n))) == wantName {
			found = id
		}
	})
	if found == "" {
		// Logged-in header link not found — most likely an expired cookie
		// serving the login page (same signal as a 401 profile fetch).
		return "", serr(http.StatusUnauthorized, "user_id_not_found")
	}
	return found, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Extraction strategies
// ─────────────────────────────────────────────────────────────────────────────

func extractStats(rawHTML string, labels map[string]string, eventTitleClass string) map[string]string {
	// Longest-first label slice so tracker extra labels participate in
	// prefix-collision prevention.
	sorted := make([]string, 0, len(labels))
	for l := range labels {
		sorted = append(sorted, l)
	}
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })

	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return map[string]string{}
	}

	result := map[string]string{}

	// Strategy 0a: <time class="profile__registration" datetime="...">
	walkNodes(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "time" && hasClass(n, "profile__registration") {
			if dt := attr(n, "datetime"); dt != "" {
				datePart := strings.SplitN(dt, " ", 2)[0]
				if ok, _ := regexp.MatchString(`\d{4}-\d{2}-\d{2}`, datePart); ok {
					if _, exists := result["join_date"]; !exists {
						result["join_date"] = datePart
					}
				}
			}
		}
	})

	// Strategy 0e: title-attribute labels — <el title="Upload">…value…</el>.
	// Used by the Unit3D top-nav ratio bar (uploaded/downloaded/ratio/buffer/
	// bonus points/FL tokens) and metadata spans like title="Registration date".
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || countDescendants(n) > 10 {
			return
		}
		key, ok := labels[normText(attr(n, "title"))]
		if !ok || result[key] != "" {
			return
		}
		v := acceptVal(nodeText(n))
		if v != "" && labels[normText(v)] == "" && validForField(key, v) {
			result[key] = v
		}
	})

	// Strategy 0b: data-table__* adjacent label/value pattern.
	walkNodes(doc, func(n *html.Node) {
		if !strings.Contains(attr(n, "class"), "data-table__") {
			return
		}
		key, ok := labels[normText(nodeText(n))]
		if !ok || result[key] != "" {
			return
		}
		if sib := nextSibling(n); sib != nil {
			v := acceptVal(nodeText(sib))
			if v != "" && labels[normText(v)] == "" && validForField(key, v) {
				result[key] = v
			}
		}
	})

	// Strategy 0c: inline label with <strong> child.
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		tag := n.Data
		if tag != "span" && tag != "div" && tag != "p" && tag != "li" {
			return
		}
		if countDescendants(n) > 20 {
			return
		}
		// Key-value structures (<dt>/<dd> children) belong to strategy 2 —
		// prefix-matching their combined text grabs wrong values (e.g. a
		// "BON Upload: 0 B" group polluting the "bon" bonus-points label).
		if findChild(n, "dt", "dd") != nil {
			return
		}
		elText := normText(nodeText(n))
		for _, label := range sorted {
			if !strings.HasPrefix(elText, label) {
				continue
			}
			after := elText[len(label):]
			if after != "" && (after[0] == '_' || unicode.IsLetter(rune(after[0])) || unicode.IsDigit(rune(after[0]))) {
				continue
			}
			key := labels[label]
			if result[key] != "" {
				return
			}
			if strong := findChild(n, "strong", "b"); strong != nil {
				v := acceptVal(nodeText(strong))
				if v != "" && labels[normText(v)] == "" {
					if !validForField(key, v) {
						return
					}
					result[key] = v
					return
				}
			}
			origText := strings.TrimSpace(nodeText(n))
			idx := strings.Index(strings.ToLower(origText), label)
			if idx == -1 {
				return
			}
			remainder := strings.TrimLeft(origText[idx+len(label):], ": ")
			if remainder == "" || strings.HasPrefix(remainder, "(") {
				return
			}
			if v := acceptVal(remainder); v != "" && labels[normText(v)] == "" && validForField(key, v) {
				result[key] = v
			}
			return
		}
	})

	// Strategy 0d: <parent><label-child>LABEL</label-child>VALUE_TEXT</parent>
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || countDescendants(n) > 10 {
			return
		}
		var matchedKey string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if k, ok := labels[normText(nodeText(c))]; ok && result[k] == "" {
				matchedKey = k
				break
			}
		}
		if matchedKey == "" {
			return
		}
		var texts []string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				if t := strings.TrimSpace(c.Data); t != "" {
					texts = append(texts, t)
				}
			}
		}
		if v := acceptVal(strings.Join(texts, " ")); v != "" && labels[normText(v)] == "" {
			if !validForField(matchedKey, v) {
				return
			}
			result[matchedKey] = v
		}
	})

	// Strategy 1: <tr> with exactly two cells.
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "tr" {
			return
		}
		cells := childrenByTag(n, "td", "th")
		if len(cells) != 2 {
			return
		}
		key, ok := labels[normText(nodeText(cells[0]))]
		if !ok || result[key] != "" {
			return
		}
		if v := acceptVal(nodeText(cells[1])); v != "" && validForField(key, v) {
			result[key] = v
			return
		}
		// Text-less value cell holding an image (e.g. TBDev class badges:
		// <td><img alt="Cinema Addicted"></td>) — use the image's alt/title.
		if img := findChild(cells[1], "img"); img != nil {
			alt := attr(img, "alt")
			if alt == "" {
				alt = attr(img, "title")
			}
			if v := acceptVal(alt); v != "" && validForField(key, v) {
				result[key] = v
			}
		}
	})

	// Strategy 2: <dt>/<dd> definition lists.
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "dt" {
			return
		}
		key, ok := labels[normText(nodeText(n))]
		if !ok || result[key] != "" {
			return
		}
		if dd := nextSiblingByTag(n, "dd"); dd != nil {
			if v := acceptVal(nodeText(dd)); v != "" && validForField(key, v) {
				result[key] = v
			}
		}
	})

	// Strategy 3: <li> with bold/strong label.
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "li" {
			return
		}
		strong := findChild(n, "strong", "b", "span")
		if strong == nil {
			return
		}
		key, ok := labels[normText(nodeText(strong))]
		if !ok || result[key] != "" {
			return
		}
		valText := strings.TrimLeft(strings.Replace(nodeText(n), nodeText(strong), "", 1), ": ")
		if v := acceptVal(valText); v != "" {
			if !validForField(key, v) {
				return
			}
			result[key] = v
		}
	})

	// Strategy 3b: profile-stat-card layout.
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || !hasClass(n, "profile-stat-card__label") {
			return
		}
		key, ok := labels[normText(nodeText(n))]
		if !ok || result[key] != "" {
			return
		}
		if n.Parent == nil {
			return
		}
		valEl := findDescendantByClass(n.Parent, "profile-stat-card__value")
		if valEl == nil {
			return
		}
		if v := acceptVal(nodeText(valEl)); v != "" && labels[normText(v)] == "" {
			if !validForField(key, v) {
				return
			}
			result[key] = v
		}
	})

	// Strategy 4: adjacent sibling text nodes (broad fallback).
	for _, label := range sorted {
		key := labels[label]
		if result[key] != "" {
			continue
		}
		found := false
		walkNodes(doc, func(n *html.Node) {
			if found || n.Type != html.TextNode || normText(n.Data) != label {
				return
			}
			ancestor := n.Parent
			for range [2]struct{}{} {
				if ancestor == nil {
					break
				}
				if sib := nextSibling(ancestor); sib != nil {
					v := acceptVal(nodeText(sib))
					if v != "" && labels[normText(v)] == "" {
						if !validForField(key, v) {
							ancestor = ancestor.Parent
							continue
						}
						result[key] = v
						found = true
						return
					}
				}
				ancestor = ancestor.Parent
			}
		})
	}

	extractEventBanner(rawHTML, doc, result, eventTitleClass)
	return result
}

// extractEventBanner finds active_event and active_event_ends_at.
// Primary: Alpine.js countdown spans inside .special-event-alert.
// Fallback: promoTime JS variable in an inline <script>.
// Title: tracker-specified CSS class (event_title_class) or default <strong>.
func extractEventBanner(rawHTML string, doc *html.Node, result map[string]string, eventTitleClass string) {
	var eventEl *html.Node
	walkNodes(doc, func(n *html.Node) {
		if eventEl == nil && hasClass(n, "special-event-alert") {
			eventEl = n
		}
	})
	if eventEl == nil {
		return
	}

	// Strategy A: tracker-specified title class (e.g. <span class="badge">).
	if eventTitleClass != "" {
		var titleEl *html.Node
		walkNodes(eventEl, func(n *html.Node) {
			if titleEl == nil && n.Type == html.ElementNode && hasClass(n, eventTitleClass) {
				titleEl = n
			}
		})
		if titleEl != nil {
			txt := collapseSpace(strings.TrimSpace(nodeText(titleEl)))
			if txt != "" && len(txt) <= 200 {
				result["active_event"] = txt
			}
		}
	}

	// Strategy B: default <strong>, skipping the countdown-timer span.
	if _, set := result["active_event"]; !set {
		if strong := findDescendantByTag(eventEl, "strong"); strong != nil {
			var parts []string
			for c := strong.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && hasClass(c, "countdown-timer") {
					continue
				}
				part := strings.TrimRight(strings.TrimSpace(nodeText(c)), ": \t")
				if part != "" {
					parts = append(parts, part)
				}
			}
			if len(parts) > 0 {
				txt := collapseSpace(strings.Join(parts, " + "))
				if len(txt) <= 200 {
					result["active_event"] = txt
				}
			}
		}
	}

	// Countdown spans (only trusted when remaining > 0 — they are usually
	// populated client-side and read as empty/zero in raw HTML).
	counts := map[string]int64{}
	walkNodes(eventEl, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "span" {
			return
		}
		key := strings.ToLower(strings.TrimSpace(attr(n, "x-text")))
		switch key {
		case "days", "hours", "minutes", "seconds":
			var v int64
			fmt.Sscanf(strings.TrimSpace(nodeText(n)), "%d", &v)
			counts[key] = v
		}
	})
	remaining := counts["days"]*86400 + counts["hours"]*3600 + counts["minutes"]*60 + counts["seconds"]
	if remaining > 0 {
		result["active_event_ends_at"] = fmt.Sprintf("%d", time.Now().Unix()+remaining)
		return
	}

	// promoTime fallback.
	rePromo := regexp.MustCompile("promoTime\\s*:\\s*new Date\\([`'\"]([^`'\"]+)[`'\"]\\)")
	m := rePromo.FindStringSubmatch(rawHTML)
	if m == nil {
		return
	}
	dateStr := strings.TrimSpace(m[1])
	var ts int64
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(layout, dateStr); err == nil {
			ts = t.Unix()
			break
		}
	}
	if ts == 0 {
		ts = parseUSDateTime(dateStr)
	}
	if ts > 0 {
		result["active_event_ends_at"] = fmt.Sprintf("%d", ts)
	}
}

// extractStatCardPairs handles layouts where each stat card holds a value
// element and a label element as siblings (either order) under one parent.
// Opt-in via the tracker def's scrape.stat_card_classes.
func extractStatCardPairs(rawHTML string, labels map[string]string, labelClass, valueClass string, result map[string]string) {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return
	}
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || !hasClass(n, labelClass) {
			return
		}
		key, ok := labels[normText(nodeText(n))]
		if !ok || result[key] != "" {
			return
		}
		parent := n.Parent
		if parent == nil {
			return
		}
		for sib := parent.FirstChild; sib != nil; sib = sib.NextSibling {
			if sib == n || sib.Type != html.ElementNode || !hasClass(sib, valueClass) {
				continue
			}
			v := acceptVal(nodeText(sib))
			if v == "" || labels[normText(v)] != "" || !validForField(key, v) {
				continue
			}
			result[key] = v
			return
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Post-processing
// ─────────────────────────────────────────────────────────────────────────────

var dateFormats = []string{
	"2006-01-02", "02/01/2006", "01/02/2006",
	"January 02, 2006", "Jan 02, 2006",
	"January 2, 2006", "Jan 2, 2006",
	"02 January 2006", "02 Jan 2006", "2006/01/02",
}

var digitRe = regexp.MustCompile(`\d+`)

// nonDigitSepRe matches every kind of thousands separator: commas, regular
// spaces, and non-breaking spaces ("1 966 075" / "1,966,075" → "1966075").
var nonDigitSepRe = regexp.MustCompile(`[,\s\x{00a0}\x{202f}]`)

// parenRe strips trailing parentheticals from dates, e.g.
// "Dec 26, 2025 (5 months ago)" → "Dec 26, 2025".
var parenRe = regexp.MustCompile(`\s*\(.*\)\s*$`)

func postProcess(r map[string]string) {
	if raw, ok := r["join_date"]; ok {
		raw = strings.TrimSpace(parenRe.ReplaceAllString(raw, ""))
		r["join_date"] = raw
		for _, f := range dateFormats {
			if t, err := time.Parse(f, raw); err == nil {
				r["join_date"] = t.Format("2006-01-02")
				break
			}
		}
	}
	// Integer fields: strip separators (commas, narrow/regular NBSPs) then
	// keep the first digit run ("157 697" → "157697").
	// adoptions: keeps only the locked-in count — trailing decorations like
	// "(+1 Fostered) View" are dropped (fostering is a temporary ≤2-week
	// state before a reservation converts into an adoption).
	for _, fld := range []string{
		"warnings", "invites", "fl_tokens", "uploads_approved", "requests_filled",
		"bonus_points", "seeding", "leeching", "hit_and_runs", "upload_snatches",
		"adoptions",
	} {
		if raw, ok := r[fld]; ok {
			clean := nonDigitSepRe.ReplaceAllString(raw, "")
			if m := digitRe.FindString(strings.TrimSpace(clean)); m != "" {
				r[fld] = m
			} else {
				delete(r, fld)
			}
		}
	}
	for _, fld := range []string{"ratio", "real_ratio"} {
		if raw, ok := r[fld]; ok {
			clean := strings.TrimSpace(strings.ReplaceAll(raw, ",", ""))
			if _, err := strconv.ParseFloat(clean, 64); err == nil {
				r[fld] = clean
			}
		}
	}
	// Size fields: drop trailing annotations like "2.25 TiB (3247)" — some
	// ratio bars append a count in parentheses next to the size.
	for _, fld := range []string{"uploaded", "downloaded", "buffer", "seed_size"} {
		if raw, ok := r[fld]; ok {
			r[fld] = strings.TrimSpace(parenRe.ReplaceAllString(raw, ""))
		}
	}
}

var tzOffsets = map[string]int{
	"EST": -5, "EDT": -4, "CST": -6, "CDT": -5,
	"MST": -7, "MDT": -6, "PST": -8, "PDT": -7,
	"UTC": 0, "GMT": 0,
}

func parseUSDateTime(s string) int64 {
	tzRe := regexp.MustCompile(`\b([A-Z]{2,4})\s*$`)
	offset := 0
	if m := tzRe.FindStringSubmatch(s); m != nil {
		if h, ok := tzOffsets[m[1]]; ok {
			offset = h
		}
		s = strings.TrimSpace(tzRe.ReplaceAllString(s, ""))
	}
	for _, layout := range []string{
		"01/02/2006 15:04", "01/02/2006 15:04:05",
		"02/01/2006 15:04", "02/01/2006 15:04:05",
		"01/02/2006 03:04 PM", "01/02/2006 03:04:05 PM",
		"02/01/2006 03:04 PM", "2006-01-02 15:04:05",
		"1/2/2006 3:04 PM", "1/2/2006 3:04:05 PM",
		"1/2/2006 15:04", "1/2/2006 15:04:05",
		"1/02/2006 3:04 PM", "01/2/2006 3:04 PM",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Add(time.Duration(-offset) * time.Hour).Unix()
		}
	}
	return 0
}

// ─────────────────────────────────────────────────────────────────────────────
// Gazelle-specific helpers (keyed off api.kind, not any specific tracker)
// ─────────────────────────────────────────────────────────────────────────────

type gazelleProfileData struct {
	UserID   int
	Invites  int
	Snatched int
	JoinDate string
}

func fetchGazelleProfileData(t models.Tracker, identify string) (*gazelleProfileData, *Error) {
	apiURL := fmt.Sprintf(
		"%s/api.php?action=user&apikey=%s&method=getuserinfo&type=username&user=%s",
		strings.TrimRight(t.URL, "/"), t.APIKey, t.Username)
	req, _ := http.NewRequest(http.MethodGet, apiURL, nil)
	ident.Apply(req, identify)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, serr(http.StatusBadGateway, "connection_error")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, serr(http.StatusBadGateway, fmt.Sprintf("http_%d", resp.StatusCode))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, serr(http.StatusBadGateway, "read_error")
	}
	var raw struct {
		Status   string `json:"status"`
		Response struct {
			ID       int    `json:"ID"`
			Invites  int    `json:"Invites"`
			Snatched int    `json:"Snatched"`
			JoinDate string `json:"JoinDate"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, serr(http.StatusBadGateway, "parse_error")
	}
	if raw.Status != "success" || raw.Response.ID == 0 {
		return nil, serr(http.StatusBadGateway, "api_error")
	}
	jd := raw.Response.JoinDate
	if len(jd) >= 10 {
		jd = jd[:10]
	}
	return &gazelleProfileData{
		UserID:   raw.Response.ID,
		Invites:  raw.Response.Invites,
		Snatched: raw.Response.Snatched,
		JoinDate: jd,
	}, nil
}

// extractGazelleIDStats scrapes id-keyed <li> fields (bonus_points from
// <li id="bonus_points">, uploads_approved from <li id="comm_upload">).
func extractGazelleIDStats(rawHTML string, result map[string]string) {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return
	}
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "li" {
			return
		}
		switch attr(n, "id") {
		case "bonus_points":
			if result["bonus_points"] != "" {
				return
			}
			statSpan := findDescendantByClass(n, "stat")
			if statSpan == nil {
				return
			}
			v := strings.ReplaceAll(strings.TrimSpace(nodeText(statSpan)), ",", "")
			if v != "" && digitRe.MatchString(v) {
				result["bonus_points"] = v
			}
		case "comm_upload":
			if result["uploads_approved"] != "" {
				return
			}
			var parts []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode {
					if t := strings.TrimSpace(c.Data); t != "" {
						parts = append(parts, t)
					}
				}
			}
			raw := strings.ToLower(strings.Join(parts, " "))
			if idx := strings.Index(raw, "uploaded:"); idx != -1 {
				if m := digitRe.FindString(raw[idx+len("uploaded:"):]); m != "" {
					result["uploads_approved"] = m
				}
			}
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// DOM helpers
// ─────────────────────────────────────────────────────────────────────────────

func walkNodes(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNodes(c, fn)
	}
}

func nodeText(n *html.Node) string {
	var sb strings.Builder
	walkNodes(n, func(c *html.Node) {
		if c.Type == html.TextNode {
			sb.WriteString(c.Data)
		}
	})
	return sb.String()
}

var spaceRe = regexp.MustCompile(`\s+`)

func normText(s string) string {
	s = spaceRe.ReplaceAllString(strings.ToLower(s), " ")
	return strings.TrimRight(strings.TrimSpace(s), ":")
}

func collapseSpace(s string) string { return spaceRe.ReplaceAllString(s, " ") }

// nbspRe matches non-breaking spaces (regular + narrow), which Go's \s does
// NOT cover — Unit3D renders sizes as "17.31&nbsp;TiB".
var nbspRe = regexp.MustCompile(`[\x{00a0}\x{202f}]`)

func acceptVal(s string) string {
	s = nbspRe.ReplaceAllString(s, " ")
	s = spaceRe.ReplaceAllString(strings.TrimSpace(s), " ")
	// TBDev-family layouts render "<strong>Label</strong>:  value" — the
	// colon lands in the value's text node. No real value starts with ":".
	s = strings.TrimLeft(s, ": ")
	if s == "" || len(s) > 100 || strings.HasPrefix(s, "http") {
		return ""
	}
	return s
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, cls string) bool {
	return strings.Contains(" "+attr(n, "class")+" ", " "+cls+" ")
}

func nextSibling(n *html.Node) *html.Node {
	for s := n.NextSibling; s != nil; s = s.NextSibling {
		if s.Type == html.ElementNode {
			return s
		}
	}
	return nil
}

func nextSiblingByTag(n *html.Node, tag string) *html.Node {
	for s := n.NextSibling; s != nil; s = s.NextSibling {
		if s.Type == html.ElementNode && s.Data == tag {
			return s
		}
	}
	return nil
}

func childrenByTag(n *html.Node, tags ...string) []*html.Node {
	set := map[string]bool{}
	for _, t := range tags {
		set[t] = true
	}
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && set[c.Data] {
			out = append(out, c)
		}
	}
	return out
}

func findChild(n *html.Node, tags ...string) *html.Node {
	set := map[string]bool{}
	for _, t := range tags {
		set[t] = true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && set[c.Data] {
			return c
		}
	}
	return nil
}

func findDescendantByTag(n *html.Node, tag string) *html.Node {
	var found *html.Node
	walkNodes(n, func(c *html.Node) {
		if found == nil && c.Type == html.ElementNode && c.Data == tag {
			found = c
		}
	})
	return found
}

func findDescendantByClass(n *html.Node, cls string) *html.Node {
	var found *html.Node
	walkNodes(n, func(c *html.Node) {
		if found == nil && c.Type == html.ElementNode && hasClass(c, cls) {
			found = c
		}
	})
	return found
}

func countDescendants(n *html.Node) int {
	count := 0
	walkNodes(n, func(_ *html.Node) { count++ })
	return count - 1
}

func containsDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
