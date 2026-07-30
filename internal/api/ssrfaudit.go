package api

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/netguard"
)

// The integration endpoints accept a destination from the caller and are
// allowed to reach the LAN, because Prowlarr, Jackett and qui normally live
// there. That allowance is deliberate and cannot be removed without breaking
// the ordinary deployment — but it does mean a session can be used to probe
// internal services, and until now that left no trace at all.
//
// This records it. It changes no behaviour: nothing is refused here, and a
// legitimate install produces two or three lines total, one per integration
// host, on the first request to each. What it makes visible is the shape of
// the abuse — one line per address probed — which is the difference between
// noticing a scan and never knowing it happened.

// auditSeen keeps this to one line per endpoint+host pair. A poller hits the
// same integration every refresh cycle, and a scan is recognisable from the
// number of DISTINCT hosts rather than from repetition.
var auditSeen sync.Map

// auditPrivateDestination logs a caller-supplied destination that resolves to
// a private address. kind names the endpoint ("prowlarr", "qui", …) so the
// line says which feature was used.
//
// Resolution is capped and best-effort: this is bookkeeping, and an audit note
// must never be the reason a legitimate request is slow or fails.
func auditPrivateDestination(d *Deps, kind, rawURL string) {
	if d == nil || rawURL == "" {
		return
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return
	}
	host := u.Hostname()
	if host == "" {
		return
	}
	key := kind + "|" + strings.ToLower(host)
	if _, seen := auditSeen.LoadOrStore(key, true); seen {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if netguard.IsPublicHost(ctx, host) {
		return
	}
	d.logInfof("security: the %s endpoint reached %q, a private address — expected "+
		"if that is where the service lives. Several different addresses here, "+
		"especially ones you do not recognise, mean this endpoint is being used "+
		"to probe your network.", kind, host)
}
