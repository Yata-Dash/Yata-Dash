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
// Security: concatenation lets the path change the host ("@evil.example/x",
// ".evil.example/x"), taking the credential with it. This is the point-of-use
// check, covering run-time substitutions too. The error names neither URL:
// the base may carry credentials.
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

// MaxResponseBytes caps a response body from a tracker or indexer manager, so
// an endless response cannot exhaust memory. The largest legitimate body, a
// big Prowlarr/Jackett indexer list, runs to a few MiB.
const MaxResponseBytes = 8 << 20

// ErrResponseTooLarge reports a body that exceeded its ReadLimited cap.
var ErrResponseTooLarge = errors.New("response too large")

// ReadLimited reads r to the end, failing once more than max bytes arrive.
// It fails rather than truncates, since callers parse the result. The error
// never includes body content.
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
