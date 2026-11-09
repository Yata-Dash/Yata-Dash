package netguard

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// JoinPath appends a definition-supplied path to a base URL and confirms the
// result still points at the base's scheme and host.
//
// Building a request URL by string concatenation lets the PATH decide the
// host. "https://tracker.example" + "@evil.example/api/user" parses as user
// "tracker.example" at host evil.example, and ".evil.example/x" turns the host
// into tracker.example.evil.example — either way the request, with the
// tracker's API key or session cookie attached, goes somewhere the user never
// typed. Definition validation rejects such paths at load time; this is the
// check at the point of use, so a path assembled at run time (a username
// substituted in, a def loaded by some route that skipped validation) cannot
// do it either.
//
// The error wraps ErrBlocked and names neither URL: the base may carry
// credentials, and the path is the def's to report, not the log's.
func JoinPath(base, path string) (string, error) {
	b, err := url.Parse(strings.TrimSpace(base))
	if err != nil || b.Host == "" {
		return "", fmt.Errorf("%w: base URL is not a valid absolute URL", ErrBlocked)
	}
	joined := strings.TrimRight(strings.TrimSpace(base), "/") + path
	j, err := url.Parse(joined)
	if err != nil {
		return "", fmt.Errorf("%w: path does not form a valid URL", ErrBlocked)
	}
	if !strings.EqualFold(j.Scheme, b.Scheme) || !strings.EqualFold(j.Host, b.Host) || j.User.String() != b.User.String() {
		return "", fmt.Errorf("%w: path would change the request's host", ErrBlocked)
	}
	return joined, nil
}

// MaxResponseBytes caps a response body read from a tracker or an indexer
// manager. A profile page is tens to a few hundred kilobytes and an API reply
// a few; the largest legitimate body is a Prowlarr or Jackett indexer list,
// which carries every configured indexer's settings and categories and runs
// to a few megabytes for a large setup. Eight MiB leaves headroom for that
// while bounding what a hostile or broken destination can make Yata hold in
// memory — without a cap, one endless response is an out-of-memory crash.
const MaxResponseBytes = 8 << 20

// ErrResponseTooLarge reports a body that exceeded its ReadLimited cap.
var ErrResponseTooLarge = errors.New("response too large")

// ReadLimited reads r to the end, failing once more than max bytes arrive.
//
// It fails rather than truncating because every caller parses what it reads:
// a cut-off JSON document or HTML page would surface as a misleading parse
// error, or worse parse cleanly and lose data. The error carries the limit and
// nothing of the body — a destination's content never reaches an error string.
func ReadLimited(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", ErrResponseTooLarge, max)
	}
	return b, nil
}
