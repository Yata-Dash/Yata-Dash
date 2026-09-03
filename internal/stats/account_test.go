package stats

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

func newEngine(t *testing.T) *Engine {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

// TestDeriveAccountFields covers the timestamp formats these fields actually
// arrive in, and the arithmetic in both directions.
func TestDeriveAccountFields(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	e := &Engine{}

	cases := []struct {
		name    string
		field   string
		value   any
		want    string // derived field name
		wantVal int
	}{
		// HHD's /api/user — RFC3339 with an explicit offset.
		{"rfc3339 login", FieldLastLogin, "2026-08-30T01:12:50+00:00", FieldDaysSinceLogin, 3},
		// Anthelion's LastAccess — no timezone at all, read as UTC.
		{"bare login", FieldLastLogin, "2026-09-01 03:42:44", FieldDaysSinceLogin, 1},
		// A few hours ago is "today", not one day.
		{"login today", FieldLastLogin, "2026-09-02 03:00:00", FieldDaysSinceLogin, 0},
		{"date only", FieldLastLogin, "2026-08-03", FieldDaysSinceLogin, 30},
		{"epoch seconds", FieldLastLogin, float64(now.Add(-72 * time.Hour).Unix()), FieldDaysSinceLogin, 3},
		// LST's api_key.expires_at, promoted to a top-level field by the fetcher.
		{"key expiry", FieldAPIKeyExpiresAt, "2027-01-01T00:00:00+00:00", FieldAPIKeyExpiryDays, 120},
		{"key expired", FieldAPIKeyExpiresAt, "2026-08-01T00:00:00+00:00", FieldAPIKeyExpiryDays, -32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := models.MergedStats{tc.field: {Value: tc.value, Source: models.SourceAPI, UpdatedAt: 99}}
			e.deriveAccountFields("t1", out, now)
			got, ok := out[tc.want]
			if !ok {
				t.Fatalf("%s not derived from %s = %v", tc.want, tc.field, tc.value)
			}
			if got.Value != tc.wantVal {
				t.Errorf("%s = %v, want %d", tc.want, got.Value, tc.wantVal)
			}
			// Provenance follows the timestamp the value was computed from,
			// so the UI's source dot points at where the date came from.
			if got.Source != models.SourceAPI || got.UpdatedAt != 99 {
				t.Errorf("%s provenance = %s/%d, want api/99", tc.want, got.Source, got.UpdatedAt)
			}
		})
	}
}

// TestDeriveAccountFieldsOmitsUnknowns is the point of the whole feature: a
// field with no input must be ABSENT, never a helpful-looking zero. An alert
// condition can't match an absent field, so silence on the trackers that
// report nothing is automatic — whereas days_since_login: 0 would read as
// "logged in today" on every one of them.
func TestDeriveAccountFieldsOmitsUnknowns(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	e := &Engine{}

	for _, tc := range []struct {
		name string
		in   models.MergedStats
	}{
		{"no fields at all", models.MergedStats{}},
		{"empty login", models.MergedStats{FieldLastLogin: {Value: ""}}},
		{"unparseable login", models.MergedStats{FieldLastLogin: {Value: "last week"}}},
		{"zero epoch", models.MergedStats{FieldLastLogin: {Value: float64(0)}}},
		{"unparseable expiry", models.MergedStats{FieldAPIKeyExpiresAt: {Value: "never"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e.deriveAccountFields("t1", tc.in, now)
			for _, f := range []string{FieldDaysSinceLogin, FieldLoginDaysRemaining, FieldAPIKeyExpiryDays} {
				if _, ok := tc.in[f]; ok {
					t.Errorf("%s emitted with no usable input: %+v", f, tc.in[f])
				}
			}
		})
	}
}

// TestLoginDaysRemainingNeedsAPolicy: the countdown exists only where a def
// declares max_login_gap_days. Without one there is nothing to count down to,
// and saying so by omission is right — Yata not knowing a tracker's policy is
// not evidence that the tracker has none.
func TestLoginDaysRemainingNeedsAPolicy(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	login := models.MergedStats{FieldLastLogin: {Value: "2026-08-03T12:00:00+00:00", Source: models.SourceAPI}}

	// No callback at all.
	e := &Engine{}
	e.deriveAccountFields("t1", login, now)
	if _, ok := login[FieldLoginDaysRemaining]; ok {
		t.Fatal("login_days_remaining emitted with no policy source")
	}
	if got := login[FieldDaysSinceLogin].Value; got != 30 {
		t.Fatalf("days_since_login = %v, want 30", got)
	}

	// A callback that knows nothing about this tracker.
	e.AccountPolicy = func(string) AccountPolicy { return AccountPolicy{} }
	e.deriveAccountFields("t1", login, now)
	if _, ok := login[FieldLoginDaysRemaining]; ok {
		t.Fatal("login_days_remaining emitted for a tracker with no declared policy")
	}

	// With a policy: 30 days in, 90-day gap, 60 left.
	e.AccountPolicy = func(string) AccountPolicy { return AccountPolicy{MaxLoginGapDays: 90} }
	e.deriveAccountFields("t1", login, now)
	if got := login[FieldLoginDaysRemaining].Value; got != 60 {
		t.Fatalf("login_days_remaining = %v, want 60", got)
	}

	// Past the deadline it goes negative rather than clamping — "12 days
	// overdue" is worth saying, and a clamp at 0 would look like "due today"
	// forever.
	overdue := models.MergedStats{FieldLastLogin: {Value: "2026-05-03T12:00:00+00:00", Source: models.SourceAPI}}
	e.deriveAccountFields("t1", overdue, now)
	if got := overdue[FieldLoginDaysRemaining].Value.(int); got >= 0 {
		t.Fatalf("login_days_remaining = %d for a long-lapsed account, want negative", got)
	}
}

// TestMergedDerivesFromAnyLayer: the derived fields read the MERGED login
// time, not one layer's. That is what lets phase 2's manual "I logged in just
// now" reset drive them without touching anything downstream.
func TestMergedDerivesFromAnyLayer(t *testing.T) {
	e := newEngine(t)
	e.AccountPolicy = func(string) AccountPolicy { return AccountPolicy{MaxLoginGapDays: 30} }

	// Manual layer only — no API, no scrape.
	yesterday := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	if err := e.SaveManual("t1", map[string]any{FieldLastLogin: yesterday}); err != nil {
		t.Fatal(err)
	}
	m, err := e.Merged("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got := m[FieldDaysSinceLogin].Value; got != 1 {
		t.Fatalf("days_since_login = %v, want 1", got)
	}
	if got := m[FieldLoginDaysRemaining].Value; got != 28 && got != 29 {
		t.Fatalf("login_days_remaining = %v, want 28 or 29", got)
	}
	if src := m[FieldDaysSinceLogin].Source; src != models.SourceManual {
		t.Errorf("derived source = %s, want manual", src)
	}
}

// TestAPILoginAlwaysBeatsTheRecordedOne: last_login follows the ordinary merge
// priority with no special case. The tracker's API is the source of truth
// about the tracker's own account, so it wins even when the user's record is
// newer — and even when the API layer has gone stale.
//
// This is deliberate rather than incidental. A special case that let a manual
// tap outrank a fresher API value would mean the two trackers that DO report a
// login time answer from Yata's own bookkeeping instead of from the tracker,
// which is the one place the tracker cannot be wrong.
func TestAPILoginAlwaysBeatsTheRecordedOne(t *testing.T) {
	e := newEngine(t)
	e.AccountPolicy = func(string) AccountPolicy { return AccountPolicy{MaxLoginGapDays: 90} }

	// The user taps "I've logged in" well after the API's value was written.
	if err := e.SaveAPI("t1", map[string]any{FieldLastLogin: "2026-08-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := e.SaveManual("t1", map[string]any{FieldLastLogin: "2026-08-30T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	m, err := e.Merged("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got := m[FieldLastLogin].Value; got != "2026-08-01T00:00:00Z" {
		t.Errorf("last_login = %v, want the API value even though it is older", got)
	}
	if src := m[FieldLastLogin].Source; src != models.SourceAPI {
		t.Errorf("source = %s, want api", src)
	}
	// The derived countdown follows the winning value, not the tap.
	if _, ok := m[FieldDaysSinceLogin]; !ok {
		t.Error("days_since_login missing")
	}
}

// TestRecordedLoginFillsWhereTheAPIIsSilent is the case that carries the
// feature: a tracker whose API reports no login time at all. Of 22 configured
// trackers probed on 2026-09-02, exactly one reported one.
func TestRecordedLoginFillsWhereTheAPIIsSilent(t *testing.T) {
	e := newEngine(t)
	e.AccountPolicy = func(string) AccountPolicy { return AccountPolicy{MaxLoginGapDays: 90} }

	if err := e.SaveAPI("t1", map[string]any{"ratio": "1.20"}); err != nil {
		t.Fatal(err)
	}
	if err := e.SaveManual("t1", map[string]any{FieldLastLogin: "2026-08-30T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	m, err := e.Merged("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got := m[FieldLastLogin].Value; got != "2026-08-30T00:00:00Z" {
		t.Errorf("last_login = %v, want the recorded login", got)
	}
	if src := m[FieldLastLogin].Source; src != models.SourceManual {
		t.Errorf("source = %s, want manual", src)
	}
}

// TestManualLoginDrivesTheCountdownAlone is the phase-2 case that matters: a
// tracker whose API reports no login time at all. Of 22 configured trackers on
// 2026-09-02, exactly one reported one, so this is the normal path.
func TestManualLoginDrivesTheCountdownAlone(t *testing.T) {
	e := newEngine(t)
	e.AccountPolicy = func(string) AccountPolicy { return AccountPolicy{MaxLoginGapDays: 60} }

	if err := e.SaveAPI("t1", map[string]any{"ratio": "1.20"}); err != nil {
		t.Fatal(err)
	}
	m, err := e.Merged("t1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m[FieldLoginDaysRemaining]; ok {
		t.Fatal("countdown present before the user recorded anything")
	}

	// The user taps "I've logged in".
	now := time.Now().UTC().Format(time.RFC3339)
	if err := e.SaveManual("t1", map[string]any{FieldLastLogin: now}); err != nil {
		t.Fatal(err)
	}
	m, err = e.Merged("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got := m[FieldDaysSinceLogin].Value; got != 0 {
		t.Errorf("days_since_login = %v, want 0", got)
	}
	if got := m[FieldLoginDaysRemaining].Value; got != 59 && got != 60 {
		t.Errorf("login_days_remaining = %v, want 59 or 60", got)
	}
}
