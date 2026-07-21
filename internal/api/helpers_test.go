package api

import (
	"net/http"
	"testing"
)

// TestUpstreamStatus: a 401/403 from a tracker or integration must never be
// forwarded to the browser as-is — the SPA reads a 401 as "Yata session
// expired" and re-shows the login gate (the bug where an expired TRACKER
// cookie kept logging users out of Yata). Other statuses pass through.
func TestUpstreamStatus(t *testing.T) {
	cases := []struct{ in, want int }{
		{http.StatusUnauthorized, http.StatusBadGateway},
		{http.StatusForbidden, http.StatusBadGateway},
		{http.StatusNotFound, http.StatusNotFound},
		{http.StatusTooManyRequests, http.StatusTooManyRequests},
		{http.StatusBadGateway, http.StatusBadGateway},
		{http.StatusGatewayTimeout, http.StatusGatewayTimeout},
		{http.StatusInternalServerError, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		if got := upstreamStatus(tc.in); got != tc.want {
			t.Errorf("upstreamStatus(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
