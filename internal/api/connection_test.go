package api

import (
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

func day(today int64, back int) int64 { return today - int64(back)*86400 }

// TestBuildUptimeFixedWidth: the strip is always connectionDays wide and ends
// today, whatever subset of days actually has rows — the UI renders it as
// blocks, so every tracker's strip has to line up.
func TestBuildUptimeFixedWidth(t *testing.T) {
	today := int64(1_800_000_000) / 86400 * 86400
	got := buildUptime(nil, today).Uptime
	if len(got) != connectionDays {
		t.Fatalf("width = %d, want %d", len(got), connectionDays)
	}
	for i, v := range got {
		if v != -1 {
			t.Errorf("day %d = %v with no rows, want -1 (no data)", i, v)
		}
	}
}

// TestBuildUptimeNoDataIsNotDown is the guarantee that matters most: gaps must
// never be reported as outages. A tracker added yesterday, or one paused for a
// week, has no failures to show — only missing days.
func TestBuildUptimeNoDataIsNotDown(t *testing.T) {
	today := int64(1_800_000_000) / 86400 * 86400
	rows := []store.ConnectionDay{
		{Day: day(today, 0), OKCount: 4},
	}
	v := buildUptime(rows, today)
	got, unreachable, kind := v.Uptime, v.Unreachable, v.LastKind
	if unreachable {
		t.Error("a tracker with one good day and six empty ones must not be unreachable")
	}
	if kind != "" {
		t.Errorf("last down kind = %q, want empty", kind)
	}
	if got[connectionDays-1] != 1 {
		t.Errorf("today = %v, want 1", got[connectionDays-1])
	}
	for i := range connectionDays - 1 {
		if got[i] != -1 {
			t.Errorf("day %d = %v, want -1", i, got[i])
		}
	}
}

// TestBuildUptimeVerdictFollowsLastContact: the current verdict comes from the
// most recent day that actually had contact, so a tracker that broke two days
// ago and has not been tried since still reads as down rather than being
// silently forgiven by today's empty row.
func TestBuildUptimeVerdictFollowsLastContact(t *testing.T) {
	today := int64(1_800_000_000) / 86400 * 86400
	rows := []store.ConnectionDay{
		{Day: day(today, 4), OKCount: 6},
		{Day: day(today, 2), FailCount: 3, LastKind: "http_500"},
	}
	v := buildUptime(rows, today)
	got, unreachable, kind := v.Uptime, v.Unreachable, v.LastKind
	if !unreachable {
		t.Error("want unreachable — the last day with contact failed outright")
	}
	if kind != "http_500" {
		t.Errorf("kind = %q, want http_500", kind)
	}
	if got[connectionDays-3] != 0 {
		t.Errorf("outage day = %v, want 0", got[connectionDays-3])
	}
	if got[connectionDays-1] != -1 {
		t.Errorf("today (no contact) = %v, want -1", got[connectionDays-1])
	}
}

// TestBuildUptimePartialDay: a day that mixed successes and failures reads as
// a fraction, and is NOT counted as unreachable — something got through.
func TestBuildUptimePartialDay(t *testing.T) {
	today := int64(1_800_000_000) / 86400 * 86400
	rows := []store.ConnectionDay{
		{Day: today, OKCount: 3, FailCount: 1, LastKind: "timeout"},
	}
	v := buildUptime(rows, today)
	got, unreachable := v.Uptime, v.Unreachable
	if unreachable {
		t.Error("a partially-successful day is not an outage")
	}
	if got[connectionDays-1] != 0.75 {
		t.Errorf("uptime = %v, want 0.75", got[connectionDays-1])
	}
}

// TestBuildUptimeIgnoresRowsOutsideWindow: rollups older than the strip must
// not shift the verdict, or a months-old outage would pin the card red.
func TestBuildUptimeIgnoresRowsOutsideWindow(t *testing.T) {
	today := int64(1_800_000_000) / 86400 * 86400
	rows := []store.ConnectionDay{
		{Day: day(today, 30), FailCount: 9, LastKind: "http_500"},
		{Day: today, OKCount: 2},
	}
	unreachable := buildUptime(rows, today).Unreachable
	if unreachable {
		t.Error("an out-of-window outage must not affect the current verdict")
	}
}

// TestBuildUptimeAPIDownBehindWorkingScrape is the case a combined success
// count cannot see: the API 500s on every refresh, the scrape fallback that
// runs straight after it succeeds, so the day is half-successful and the
// tracker reports as perfectly reachable while half of how Yata reaches it
// has been broken for days. The API channel is tallied separately so the
// dashboard can say so.
func TestBuildUptimeAPIDownBehindWorkingScrape(t *testing.T) {
	today := int64(1_800_000_000) / 86400 * 86400
	rows := []store.ConnectionDay{
		{Day: today, OKCount: 6, FailCount: 6, LastKind: "http_500",
			APIOKCount: 0, APIFailCount: 6},
	}
	v := buildUptime(rows, today)
	if v.Unreachable {
		t.Error("the scrape fallback worked — the tracker is not unreachable")
	}
	if !v.APIDown {
		t.Fatal("every API attempt failed; want APIDown so the card can report it")
	}
	if v.APIDownKind != "http_500" {
		t.Errorf("APIDownKind = %q, want http_500", v.APIDownKind)
	}
}

// TestBuildUptimeAPIDownQuietWhenNeverTried: a scrape-only tracker (no API
// key, so nothing is ever sent) must not be reported as having a dead API.
func TestBuildUptimeAPIDownQuietWhenNeverTried(t *testing.T) {
	today := int64(1_800_000_000) / 86400 * 86400
	rows := []store.ConnectionDay{{Day: today, OKCount: 5}}
	if v := buildUptime(rows, today); v.APIDown {
		t.Error("a tracker whose API was never tried must not read as API-down")
	}
}

// TestBuildUptimeFullyDarkSuppressesAPIDown: when nothing got through at all,
// "unreachable" already says it — reporting the API half too would double-count
// one tracker as two separate problems on the card.
func TestBuildUptimeFullyDarkSuppressesAPIDown(t *testing.T) {
	today := int64(1_800_000_000) / 86400 * 86400
	rows := []store.ConnectionDay{
		{Day: today, FailCount: 4, LastKind: "timeout", APIFailCount: 4},
	}
	v := buildUptime(rows, today)
	if !v.Unreachable {
		t.Fatal("want unreachable")
	}
	if v.APIDown {
		t.Error("fully-dark tracker must not also report APIDown")
	}
}

// TestRecordConnectionSeparatesChannels: the source argument must land in the
// per-channel tally, not just the combined one.
func TestRecordConnectionSeparatesChannels(t *testing.T) {
	d := testDeps(t)
	now := time.Now().UTC()
	_ = d.DB.RecordConnection("t1", now, false, "http_500", "api")
	_ = d.DB.RecordConnection("t1", now, true, "", "scrape")

	rows, err := d.DB.ConnectionDaily([]string{"t1"}, now.Add(-24*time.Hour))
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v err = %v", rows, err)
	}
	c := rows[0]
	if c.OKCount != 1 || c.FailCount != 1 {
		t.Errorf("combined counts = %d/%d, want 1/1", c.OKCount, c.FailCount)
	}
	if c.APIOKCount != 0 || c.APIFailCount != 1 {
		t.Errorf("api counts = %d/%d, want 0/1 (the scrape must not land here)",
			c.APIOKCount, c.APIFailCount)
	}
	if !c.APIDown() {
		t.Error("APIDown() should be true — the only API attempt failed")
	}
}

// TestRecordConnectionEvents covers the timeline half: only state CHANGES
// become events, a run of identical failures is de-duped, and the timeline
// never opens with a recovery there was no outage to precede.
func TestRecordConnectionEvents(t *testing.T) {
	d := testDeps(t)
	tr := models.Tracker{ID: "tr1", Name: "Test"}

	recordConnection(d, tr, "api", "")         // first ever contact, fine — no event
	recordConnection(d, tr, "api", "")         // still fine — no event
	recordConnection(d, tr, "api", "timeout")  // went down — 1 event
	recordConnection(d, tr, "api", "timeout")  // still down, same kind — de-duped
	recordConnection(d, tr, "api", "http_500") // still down, new kind — 1 event
	recordConnection(d, tr, "api", "")         // recovered — 1 event

	evs, err := d.DB.EventsSince(nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var conn []string
	for _, e := range evs {
		if e.Kind == "connection" {
			conn = append(conn, e.Detail)
		}
	}
	want := []string{"down:timeout", "down:http_500", "up"}
	if len(conn) != len(want) {
		t.Fatalf("connection events = %v, want %v", conn, want)
	}
	for i := range want {
		if conn[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, conn[i], want[i])
		}
	}
}

// TestRecordConnectionIgnoresPreflight: a tracker with no API key configured
// (scrape-only by design) must not be recorded as unreachable — nothing was
// ever sent, so there is no connection outcome to report. Without this the
// Health card sits permanently red on a perfectly healthy setup.
func TestRecordConnectionIgnoresPreflight(t *testing.T) {
	d := testDeps(t)
	tr := models.Tracker{ID: "tr1", Name: "Test"}

	for _, kind := range []string{"no_key", "no_username", "no_cookie", "no_def"} {
		recordConnection(d, tr, "api", kind)
	}
	rows, err := d.DB.ConnectionDaily([]string{"tr1"}, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("pre-flight failures recorded as connection outcomes: %+v", rows)
	}
	evs, _ := d.DB.EventsSince(nil, time.Unix(0, 0))
	for _, e := range evs {
		if e.Kind == "connection" {
			t.Errorf("pre-flight failure produced a timeline event: %+v", e)
		}
	}

	// A real network failure on the same tracker still records.
	recordConnection(d, tr, "api", "timeout")
	rows, _ = d.DB.ConnectionDaily([]string{"tr1"}, time.Now().UTC().Add(-24*time.Hour))
	if len(rows) != 1 || rows[0].FailCount != 1 {
		t.Errorf("real failure not recorded after pre-flight ones: %+v", rows)
	}
}

// TestRecordConnectionSurvivesRestart: de-duping reads the last PERSISTED
// event, not in-process state, so a restart while a tracker is already down
// must not record a second "went down".
func TestRecordConnectionSurvivesRestart(t *testing.T) {
	d := testDeps(t)
	tr := models.Tracker{ID: "tr1", Name: "Test"}

	recordConnection(d, tr, "api", "")
	recordConnection(d, tr, "api", "timeout")
	// Simulated restart: nothing in memory, tracker still failing the same way.
	recordConnection(d, tr, "api", "timeout")

	evs, _ := d.DB.EventsSince(nil, time.Unix(0, 0))
	n := 0
	for _, e := range evs {
		if e.Kind == "connection" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("connection events = %d, want 1 (restart must not re-record)", n)
	}
}

// TestScrapeStatusCarriesUptime wires the store through the endpoint's shape:
// a recorded failure must surface as both the strip and the verdict.
func TestScrapeStatusCarriesUptime(t *testing.T) {
	d := testDeps(t)
	if err := d.DB.RecordConnection("t1", time.Now().UTC(), false, "timeout", "api"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	rows, err := d.DB.ConnectionDaily([]string{"t1"}, now.AddDate(0, 0, -(connectionDays-1)))
	if err != nil {
		t.Fatal(err)
	}
	v := buildUptime(rows, today)
	strip, unreachable, kind := v.Uptime, v.Unreachable, v.LastKind
	if !unreachable || kind != "timeout" {
		t.Errorf("unreachable = %v kind = %q, want true/timeout", unreachable, kind)
	}
	if strip[connectionDays-1] != 0 {
		t.Errorf("today = %v, want 0", strip[connectionDays-1])
	}
}
