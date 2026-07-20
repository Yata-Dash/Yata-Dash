package notify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// A destination that 429s once then succeeds should be retried and delivered.
func TestSendRetriesOn429(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "0.05")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"retry_after":0.05}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dest := models.NotifyDestination{Type: "generic", URL: srv.URL, Enabled: true}
	if err := Send(dest, "title", "msg"); err != nil {
		t.Fatalf("expected delivery after retry, got error: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected 2 attempts (429 then 200), got %d", got)
	}
}

// A destination that always 429s should give up after maxSendAttempts.
func TestSendGivesUpOnPersistent429(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Retry-After", "0.01")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"retry_after":0.01}`))
	}))
	defer srv.Close()

	dest := models.NotifyDestination{Type: "generic", URL: srv.URL, Enabled: true}
	if err := Send(dest, "title", "msg"); err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if got := atomic.LoadInt32(&hits); got != maxSendAttempts {
		t.Fatalf("expected %d attempts, got %d", maxSendAttempts, got)
	}
}

// captureRequest is a test double for a webhook endpoint: it records every
// delivered title (parsed back out of the generic-type "title" field) so
// SendChunked's numbering and ordering can be asserted without a real
// destination.
func captureRequest(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var titles []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title string `json:"title"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		titles = append(titles, body.Title)
		w.WriteHeader(http.StatusOK)
	}))
	return srv, &titles
}

// TestSendChunkedUnderLimitIsOneSend: text under the chunk limit is delivered
// as a single message with the title unchanged (no "(1/1)" suffix).
func TestSendChunkedUnderLimitIsOneSend(t *testing.T) {
	srv, titles := captureRequest(t)
	defer srv.Close()

	dest := models.NotifyDestination{Type: "generic", URL: srv.URL, Enabled: true}
	if err := SendChunked(dest, "Yata weekly digest", "short digest body"); err != nil {
		t.Fatalf("SendChunked: %v", err)
	}
	if len(*titles) != 1 {
		t.Fatalf("sent %d messages, want 1", len(*titles))
	}
	if (*titles)[0] != "Yata weekly digest" {
		t.Errorf("title = %q, want unchanged \"Yata weekly digest\"", (*titles)[0])
	}
}

// TestSendChunkedSplitsOnLineBoundaries: text over the chunk limit splits
// into multiple sends, never mid-line, with numbered titles.
func TestSendChunkedSplitsOnLineBoundaries(t *testing.T) {
	srv, titles := captureRequest(t)
	defer srv.Close()

	// Build ~4000 chars of distinct lines so it must split into at least 3
	// chunks under the 1800-char limit.
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("line-%02d-%s", i, strings.Repeat("x", 60)))
	}
	text := strings.Join(lines, "\n")

	dest := models.NotifyDestination{Type: "generic", URL: srv.URL, Enabled: true}
	if err := SendChunked(dest, "Yata weekly digest", text); err != nil {
		t.Fatalf("SendChunked: %v", err)
	}

	chunks := chunkLines(text, chunkSendLimit)
	if len(chunks) < 2 {
		t.Fatalf("test fixture didn't produce multiple chunks (got %d) — enlarge it", len(chunks))
	}
	if len(*titles) != len(chunks) {
		t.Fatalf("sent %d messages, want %d (one per chunk)", len(*titles), len(chunks))
	}
	for i, title := range *titles {
		want := fmt.Sprintf("Yata weekly digest (%d/%d)", i+1, len(chunks))
		if title != want {
			t.Errorf("title[%d] = %q, want %q", i, title, want)
		}
	}
	// No chunk may end mid-line: every chunk boundary falls exactly on a "\n".
	rebuilt := strings.Join(chunks, "\n")
	if rebuilt != text {
		t.Errorf("chunks don't losslessly rejoin into the original text on line boundaries")
	}
}
