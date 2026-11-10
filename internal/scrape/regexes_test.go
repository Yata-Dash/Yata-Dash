package scrape

import (
	"strconv"
	"testing"
	"time"
)

// TestParseUSDateTimeZoneSuffix: a known zone suffix shifts the result and is
// stripped.
func TestParseUSDateTimeZoneSuffix(t *testing.T) {
	utc := parseUSDateTime("01/02/2026 15:04")
	if want := time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC).Unix(); utc != want {
		t.Fatalf("parseUSDateTime = %d, want %d", utc, want)
	}
	if got := parseUSDateTime("01/02/2026 15:04 EST"); got != utc+5*3600 {
		t.Errorf("EST: got %d, want %d", got, utc+5*3600)
	}
	if got := parseUSDateTime("01/02/2026 15:04 UTC"); got != utc {
		t.Errorf("UTC: got %d, want %d", got, utc)
	}
}

// TestExtractStatsHoistedPatterns covers the registration <time> and
// promoTime fallback paths.
func TestExtractStatsHoistedPatterns(t *testing.T) {
	raw := `<html><body>
<time class="profile__registration" datetime="2024-05-06 12:00:00">6 May 2024</time>
<div class="special-event-alert"><strong>Freeleech</strong></div>
<script>var x = { promoTime: new Date('2026-01-02T15:04:05Z') };</script>
</body></html>`
	got := extractStats(raw, map[string]string{}, "")
	if got["join_date"] != "2024-05-06" {
		t.Errorf("join_date = %q, want 2024-05-06", got["join_date"])
	}
	want := strconv.FormatInt(time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC).Unix(), 10)
	if got["active_event_ends_at"] != want {
		t.Errorf("active_event_ends_at = %q, want %s", got["active_event_ends_at"], want)
	}

	// A datetime that is not an ISO date must not be taken as the join date.
	bad := extractStats(`<time class="profile__registration" datetime="yesterday">x</time>`, map[string]string{}, "")
	if bad["join_date"] != "" {
		t.Errorf("join_date from non-ISO datetime = %q, want empty", bad["join_date"])
	}
}

func BenchmarkParseUSDateTime(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		parseUSDateTime("01/02/2026 03:04 PM EST")
	}
}
