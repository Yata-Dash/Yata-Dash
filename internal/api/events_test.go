package api

import (
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/notify"
)

// TestRecordGroupChange checks the transition rules: initial group (empty old)
// and no-op/case-only changes record nothing; a real promotion records once;
// an exact repeat is de-duped.
func TestRecordGroupChange(t *testing.T) {
	d := testDeps(t)
	tr := models.Tracker{ID: "tr1", Name: "Test"}
	m := models.MergedStats{}

	recordGroupChange(d, tr, m, "", "Seeker", notify.TrendContext{})             // first sighting — no event
	recordGroupChange(d, tr, m, "Seeker", "Seeker", notify.TrendContext{})       // unchanged — no event
	recordGroupChange(d, tr, m, "PowerPool", "powerpool", notify.TrendContext{}) // case-only — no event
	recordGroupChange(d, tr, m, "Seeker", "PowerPool", notify.TrendContext{})    // real promotion — 1 event
	recordGroupChange(d, tr, m, "Seeker", "PowerPool", notify.TrendContext{})    // exact repeat — de-duped

	evs, err := d.DB.EventsSince(nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1: %+v", len(evs), evs)
	}
	if evs[0].Detail != "Seeker→PowerPool" || evs[0].Kind != "group_change" {
		t.Errorf("event = %+v", evs[0])
	}

	// A later demotion is a distinct transition and records.
	recordGroupChange(d, tr, m, "PowerPool", "Seeker", notify.TrendContext{})
	evs, _ = d.DB.EventsSince(nil, time.Unix(0, 0))
	if len(evs) != 2 {
		t.Fatalf("after demotion = %d, want 2", len(evs))
	}
}

func TestMergedFieldString(t *testing.T) {
	m := models.MergedStats{"group": models.StatField{Value: "  PowerPool  "}}
	if got := mergedFieldString(m, "group"); got != "PowerPool" {
		t.Errorf("group = %q, want PowerPool", got)
	}
	if got := mergedFieldString(m, "missing"); got != "" {
		t.Errorf("missing = %q, want empty", got)
	}
}
