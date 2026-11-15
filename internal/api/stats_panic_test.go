package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// panicTransport panics for panicHost, answers other hosts with a 500 and
// records which hosts were asked.
type panicTransport struct {
	panicHost string
	mu        sync.Mutex
	seen      map[string]bool
}

func (p *panicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p.mu.Lock()
	p.seen[req.URL.Hostname()] = true
	p.mu.Unlock()
	if req.URL.Hostname() == p.panicHost {
		panic("simulated scraper bug")
	}
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

func (p *panicTransport) asked(host string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen[host]
}

// panicDeps configures two unit3d trackers; the first panics and has stored stats.
func panicDeps(t *testing.T, prefix string) (*Deps, *panicTransport) {
	t.Helper()
	d := testDeps(t)
	pt := &panicTransport{panicHost: "boom.invalid", seen: map[string]bool{}}
	d.Fetch.HTTP = &http.Client{Transport: pt}
	for _, tr := range []models.Tracker{
		{ID: prefix + "-boom", Name: "Boom", URL: "http://boom.invalid", Type: "unit3d", APIKey: "k", Enabled: true},
		{ID: prefix + "-ok", Name: "Fine", URL: "http://ok.invalid", Type: "unit3d", APIKey: "k", Enabled: true},
	} {
		if err := d.Cfg.AddTracker(tr); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Stats.SaveAPI(prefix+"-boom", map[string]any{"uploaded": "5.00 TiB"}); err != nil {
		t.Fatal(err)
	}
	return d, pt
}

func TestRefreshCyclePanicIsContainedPerTracker(t *testing.T) {
	d, pt := panicDeps(t, "cycle")
	RunRefreshCycle(d) // must not panic
	if !pt.asked("boom.invalid") {
		t.Fatal("the panicking tracker was never refreshed — the test proves nothing")
	}
	if !pt.asked("ok.invalid") {
		t.Fatal("the tracker after the panicking one was not refreshed")
	}
}

func TestBulkStatsPanicIsContainedPerTracker(t *testing.T) {
	d, pt := panicDeps(t, "bulk")
	rec := httptest.NewRecorder()
	bulkStats(d)(rec, httptest.NewRequest(http.MethodGet, "/api/stats?force=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var got map[string]models.TrackerStatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	boom, ok := got["bulk-boom"]
	if !ok || boom.OK || boom.ErrorKind != "internal_error" {
		t.Fatalf("panicking tracker = %+v, want a failed internal_error result", boom)
	}
	// The last stored stats are still served.
	if f, ok := boom.Fields["uploaded"]; !ok || f.Value != "5.00 TiB" {
		t.Errorf("stored stats lost on panic: %+v", boom.Fields)
	}
	fine, ok := got["bulk-ok"]
	if !ok || fine.ErrorKind != "http_500" {
		t.Fatalf("other tracker = %+v, want its own http_500 result", fine)
	}
	if !pt.asked("ok.invalid") {
		t.Fatal("the other tracker was not refreshed")
	}
}
