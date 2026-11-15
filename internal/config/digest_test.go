package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// Each out-of-range field resets to its default; a valid neighbour is kept.
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

// UpdateNotifications keeps the stored send history over a stale or forged one.
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

// An import without send history keeps the live one; one with history restores it.
func TestImportKeepsLiveDigestHistoryWhenMissing(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateDigestState(1700000000, []string{"Aither"}); err != nil {
		t.Fatal(err)
	}
	importDigest := func(digest map[string]any) {
		t.Helper()
		data, err := json.Marshal(map[string]any{
			"settings":      map[string]any{},
			"notifications": map[string]any{"digest": digest},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Import(data); err != nil {
			t.Fatalf("Import: %v", err)
		}
	}

	importDigest(map[string]any{"enabled": true, "weekday": 1, "hour": 9})
	if got := m.Notifications().Digest.LastSentAt; got != 1700000000 {
		t.Fatalf("LastSentAt after import without history = %d, want the live 1700000000", got)
	}

	importDigest(map[string]any{"enabled": true, "weekday": 1, "hour": 9, "last_sent_at": 1600000000})
	if got := m.Notifications().Digest.LastSentAt; got != 1600000000 {
		t.Fatalf("LastSentAt after import with history = %d, want the restored 1600000000", got)
	}
}
