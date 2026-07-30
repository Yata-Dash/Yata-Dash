// Package redact strips credentials out of text on its way somewhere a person
// other than the account holder might read it: the log file, the Logs settings
// tab, or an API error body.
//
// This exists because the leak is invisible at the call site. A handler that
// writes `d.logWarnf("test: %v", err)` looks harmless, but Go's *url.Error
// renders the FULL request URL in its message — so a tracker whose def
// authenticates with api_key_query puts the API key into the text of every
// timeout and connection error it produces. Telegram does the same with the
// bot token, which lives in the URL path. No caller can reasonably be expected
// to remember that, and Yata's log is explicitly meant to be attached to a
// GitHub issue (see package logging), so redaction is applied centrally at the
// point every line passes through rather than asked of each author.
package redact

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Mask replaces anything removed. Deliberately free of punctuation that URL
// encoding would mangle, so it survives a round trip through url.URL.String().
const Mask = "REDACTED"

// urlRe finds absolute URLs in free text. It stops at whitespace and at the
// quote characters Go's error formatting wraps URLs in (`Get "https://…":`).
var urlRe = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.\-]*://[^\s"'<>\\]+`)

// pathSecretRes match the secrets Yata handles that live in a URL PATH rather
// than in a query string. Dropping the query covers most credentials; these
// three services put theirs where that does nothing.
//
// Each replacement keeps the identifying prefix and masks only the secret, so
// a log line still says which service and (for Discord) which webhook failed.
var pathSecretRes = []struct {
	re   *regexp.Regexp
	repl string
}{
	// Telegram: /bot<id>:<secret>/sendMessage
	{regexp.MustCompile(`/bot[0-9]+:[A-Za-z0-9_-]+`), "/bot" + Mask},
	// Discord: /api/webhooks/<id>/<token>. The token is the whole credential —
	// anyone holding it can post to that channel — and it reaches an error
	// message the same way Telegram's does, through *url.Error.
	{regexp.MustCompile(`(/webhooks/[0-9]+/)[A-Za-z0-9_.-]+`), "${1}" + Mask},
	// Slack: /services/T…/B…/<secret>
	{regexp.MustCompile(`(/services/T[A-Za-z0-9]+/B[A-Za-z0-9]+/)[A-Za-z0-9]+`), "${1}" + Mask},
}

func redactPath(p string) string {
	for _, s := range pathSecretRes {
		p = s.re.ReplaceAllString(p, s.repl)
	}
	return p
}

// trailing punctuation that belongs to the surrounding sentence, not the URL.
const trailingPunct = `.,;:!?)]}`

// String returns s with credentials removed: every URL loses its query string
// and any userinfo, and known secret-bearing path segments are masked.
//
// The whole query is dropped rather than just recognised key parameters. The
// parameter name is set by each tracker's def (api_token, apikey, api_key,
// passkey, …) and a def added tomorrow can name it anything, so an allow-list
// of "known secret parameters" would be wrong by default. Nothing Yata puts in
// a query string is worth more than the risk of missing one.
func String(s string) string {
	if s == "" || !strings.Contains(s, "://") {
		return s
	}
	return urlRe.ReplaceAllStringFunc(s, redactURL)
}

// Error renders err with String applied. Returns "" for a nil error.
func Error(err error) string {
	if err == nil {
		return ""
	}
	return String(err.Error())
}

func redactURL(raw string) string {
	trail := ""
	for len(raw) > 0 && strings.ContainsRune(trailingPunct, rune(raw[len(raw)-1])) {
		trail = raw[len(raw)-1:] + trail
		raw = raw[:len(raw)-1]
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Unparseable — cut everything from the first '?' rather than give up,
		// since the query is the part that matters. Path secrets still get
		// masked: a URL malformed enough to fail Parse is exactly when the
		// structured pass below is unavailable to do it.
		if i := strings.IndexByte(raw, '?'); i >= 0 {
			return redactPath(raw[:i]) + "?" + Mask + trail
		}
		return redactPath(raw) + trail
	}
	if u.User != nil {
		u.User = url.User(Mask)
	}
	hadQuery := u.RawQuery != ""
	u.RawQuery = ""
	u.Path = redactPath(u.Path)
	out := u.String()
	if hadQuery {
		out += "?" + Mask
	}
	return out + trail
}

// JSONShape describes a JSON document's STRUCTURE — every key kept, every
// value replaced by its type — for reporting a parse failure.
//
// Diagnosing "the API changed and no longer parses" needs the shape ("is
// uploaded a string this time?") and never the contents. The contents are the
// problem: a tracker's user endpoint carries the account's email, IRC key and
// inviter, none of which Yata stores anywhere else, and a parse error is
// exactly the moment someone reaches for the log to ask for help.
//
// A non-JSON body (an HTML login page, typically) reports only its size, since
// there is no structure to describe and the markup may embed a username.
func JSONShape(b []byte) string {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Sprintf("non-JSON body (%d bytes)", len(b))
	}
	out, err := json.Marshal(shape(v))
	if err != nil {
		return fmt.Sprintf("JSON body (%d bytes)", len(b))
	}
	return string(out)
}

func shape(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = shape(val)
		}
		return m
	case []any:
		// One representative element: a 500-entry array says nothing more about
		// the shape than its first entry does.
		if len(t) == 0 {
			return []any{}
		}
		return []any{shape(t[0])}
	case string:
		return "<string>"
	case float64:
		return "<number>"
	case bool:
		return "<bool>"
	case nil:
		return nil
	default:
		return "<unknown>"
	}
}
