package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// An out-of-range digest schedule in config.json (hand-edited, or written by
// an older build that did not check) used to survive load untouched, and a
// weekday of 20 made the digest due on every 5-minute tick. Each bad field
// falls back to its default; a valid neighbour is left alone.
func TestDigestOutOfRangeResetOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{
		"server": {"host": "0.0.0.0", "port": 8420},
		"trackers": [],
		"settings": {},
		"notifications": {
			"destinations": [], "rules": [], "seeded_default_rules": true,
			"digest": {"enabled": true, "weekday": 20, "hour": 14, "destinations": []}
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dig := m.Notifications().Digest
	if dig.Weekday != 1 || dig.Hour != 14 || !dig.Enabled {
		t.Fatalf("digest after load = %+v, want weekday reset to 1, hour 14 kept, still enabled", dig)
	}
}

// Import replaces the whole config, so it needs the same reset as load —
// before this it applied none of the digest normalisation at all.
func TestDigestOutOfRangeResetOnImport(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"settings": map[string]any{},
		"notifications": map[string]any{
			"digest": map[string]any{"enabled": true, "weekday": -3, "hour": 200},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Import(data); err != nil {
		t.Fatalf("Import: %v", err)
	}
	dig := m.Notifications().Digest
	if dig.Weekday != 1 || dig.Hour != 9 {
		t.Fatalf("digest after import = weekday %d hour %d, want the 1/9 defaults", dig.Weekday, dig.Hour)
	}
}

// A save built from an earlier read must not roll back a digest sent in the
// meantime. The send history is the server's, so UpdateNotifications keeps the
// stored value whatever the caller carries — stale or forged.
func TestUpdateNotificationsKeepsDigestSendHistory(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	stale := m.Notifications() // read before the send, as putNotifications does
	if err := m.UpdateDigestState(1700000000, []string{"Aither"}); err != nil {
		t.Fatal(err)
	}
	stale.Digest.Enabled = true
	if err := m.UpdateNotifications(stale); err != nil {
		t.Fatal(err)
	}
	dig := m.Notifications().Digest
	if dig.LastSentAt != 1700000000 {
		t.Errorf("LastSentAt = %d, want the send's 1700000000 kept over the stale 0", dig.LastSentAt)
	}
	if len(dig.LastReadyTargets) != 1 || dig.LastReadyTargets[0] != "Aither" {
		t.Errorf("LastReadyTargets = %v, want [Aither]", dig.LastReadyTargets)
	}
	if !dig.Enabled {
		t.Error("the caller's schedule change was lost")
	}

	forged := m.Notifications()
	forged.Digest = models.DigestConfig{Enabled: true, Weekday: 2, Hour: 8, LastSentAt: 5}
	if err := m.UpdateNotifications(forged); err != nil {
		t.Fatal(err)
	}
	if got := m.Notifications().Digest.LastSentAt; got != 1700000000 {
		t.Errorf("LastSentAt = %d after a forged save, want 1700000000", got)
	}
}
