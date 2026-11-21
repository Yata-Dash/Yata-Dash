package config

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

func openTemp(t *testing.T) *Manager {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Settings() used to copy only one slice, so editing a returned copy (or
// decoding a patch onto it, which writes through *bool pointers) changed the
// live settings without the lock.
func TestSettingsCopySharesNoMemory(t *testing.T) {
	m := openTemp(t)
	on := true
	if _, _, err := m.PatchSettings(func(s *models.Settings) error {
		s.AllowedHosts = []string{"yata.example"}
		s.PathwayFavorites = []string{"A"}
		s.PathwayNotInterested = []string{"B"}
		s.PathwayPins = []models.PathwayPin{{Hops: []string{"A", "B"}}}
		s.QUIEnabledInstances = []int{1}
		s.HighlightHnR = &on
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	c := m.Settings()
	c.AllowedHosts[0] = "changed.example"
	c.PathwayFavorites[0] = "X"
	c.PathwayNotInterested[0] = "X"
	c.PathwayPins[0].Hops[0] = "X"
	c.QUIEnabledInstances[0] = 9
	*c.HighlightHnR = false
	snap := m.Snapshot()
	snap.Settings.AllowedHosts[0] = "changed.example"

	got := m.Settings()
	if got.AllowedHosts[0] != "yata.example" || got.PathwayFavorites[0] != "A" ||
		got.PathwayNotInterested[0] != "B" || got.PathwayPins[0].Hops[0] != "A" ||
		got.QUIEnabledInstances[0] != 1 || !*got.HighlightHnR {
		t.Errorf("editing a copy changed the live settings: %+v", got)
	}
}

func TestPatchSettingsLeavesSettingsOnError(t *testing.T) {
	m := openTemp(t)
	if _, _, err := m.PatchSettings(func(s *models.Settings) error { s.Theme = "dark"; return nil }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	_, _, err := m.PatchSettings(func(s *models.Settings) error { s.Theme = "light"; return boom })
	if !errors.Is(err, ErrInvalidSettings) || !errors.Is(err, boom) {
		t.Fatalf("err = %v, want ErrInvalidSettings wrapping the mutate error", err)
	}
	_, _, err = m.PatchSettings(func(s *models.Settings) error { s.Theme = "light"; s.AllowedHosts = []string{"*"}; return nil })
	if !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("a refused hostname gave %v, want ErrInvalidSettings", err)
	}
	if got := m.Settings().Theme; got != "dark" {
		t.Errorf("theme = %q after failed patches, want dark", got)
	}
}

func TestPatchSettingsReturnsBothSides(t *testing.T) {
	m := openTemp(t)
	before, after, err := m.PatchSettings(func(s *models.Settings) error {
		s.ScrapeIntervalMinutes = 5 // below the floor
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.ScrapeIntervalMinutes != 60 {
		t.Errorf("after = %d, want the normalised 60", after.ScrapeIntervalMinutes)
	}
	if before.ScrapeIntervalMinutes == 5 {
		t.Error("before shows the patch, not the prior state")
	}
}
