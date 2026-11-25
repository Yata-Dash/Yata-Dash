package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// A patch that fails, or whose save fails, leaves the stored alerts as they were.
func TestPatchNotificationsKeepsNothingOnFailure(t *testing.T) {
	m := openTemp(t)
	if err := m.UpdateNotifications(models.NotificationConfig{
		Rules: []models.AlertRule{{ID: "r1", Name: "low ratio"}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.PatchNotifications(func(n *models.NotificationConfig) error {
		n.Rules[0].Name = "changed"
		return errors.New("refused")
	}); err == nil {
		t.Fatal("mutate error was not returned")
	}
	if got := m.Notifications().Rules[0].Name; got != "low ratio" {
		t.Errorf("after a refused patch the rule is %q", got)
	}

	// A file where the config's folder should be makes the save fail.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m.path = filepath.Join(blocker, "config.json")
	if _, err := m.PatchNotifications(func(n *models.NotificationConfig) error {
		n.Rules = append(n.Rules, models.AlertRule{ID: "r2"})
		return nil
	}); err == nil {
		t.Fatal("save error was not returned")
	}
	if got := m.Notifications().Rules; len(got) != 1 || got[0].ID != "r1" {
		t.Errorf("after a failed save the rules are %+v", got)
	}
}

// The copy handed to mutate shares no memory with the stored config, so an
// edit that is then refused can't leak through a shared slice.
func TestPatchNotificationsEditsACopy(t *testing.T) {
	m := openTemp(t)
	if err := m.UpdateNotifications(models.NotificationConfig{
		Rules: []models.AlertRule{{ID: "r1", TrackerIDs: []string{"t1"}, Destinations: []string{"d1"}}},
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = m.PatchNotifications(func(n *models.NotificationConfig) error {
		n.Rules[0].TrackerIDs[0] = "changed"
		n.Rules[0].Destinations[0] = "changed"
		return errors.New("refused")
	})
	r := m.Notifications().Rules[0]
	if r.TrackerIDs[0] != "t1" || r.Destinations[0] != "d1" {
		t.Errorf("a refused edit reached the stored rule: %+v", r)
	}
}
