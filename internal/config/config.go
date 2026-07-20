// Package config owns config.json: loading, atomic saving, and concurrency.
//
// Concurrency contract (lesson from v1): the Manager holds the only in-memory
// copy of the config behind a mutex. Callers get deep-ish snapshots and apply
// mutations through Manager methods. Nothing ever re-reads the file from disk
// during normal operation, so concurrent goroutines can never race a reload.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// Manager provides synchronised access to the application config.
type Manager struct {
	mu        sync.RWMutex
	path      string
	backupDir string
	cfg       models.Config
}

// Open loads (or creates) the config file at path.
func Open(path string) (*Manager, error) {
	m := &Manager{path: path, backupDir: filepath.Join(filepath.Dir(path), "backups")}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		m.cfg = defaultConfig()
		seedDefaultAlertRules(&m.cfg.Notifications)
		normalizeDigest(&m.cfg.Notifications.Digest)
		if err := m.saveLocked(); err != nil {
			return nil, fmt.Errorf("create config: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("read config: %w", err)
	default:
		if err := json.Unmarshal(data, &m.cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		m.applyDefaults()
		// Runs once per install: seedDefaultAlertRules is a no-op after the
		// flag is set, so this costs an extra write only on the very first
		// load of a pre-existing config.json that predates event alerts.
		if !m.cfg.Notifications.SeededDefaultRules {
			seedDefaultAlertRules(&m.cfg.Notifications)
			if err := m.saveLocked(); err != nil {
				return nil, fmt.Errorf("seed default alert rules: %w", err)
			}
		}
	}
	return m, nil
}

// seedDefaultAlertRules gives a brand-new install (no destinations AND no
// rules — nobody has touched Alerts yet) three starter rules so promotion/
// demotion/target-met/standing-guard notifications aren't invisible until
// someone discovers the Alerts tab: "Promotions & demotions" (promoted OR
// demoted), "Target met", and "Ratio approaching minimum" (the
// ratio_min_eta_days standing guard). All three are enabled, unscoped (all
// trackers), destination-less (all enabled destinations). The ratio guard
// gets a day-long cooldown — its ETA jitters with rate noise, so a shorter
// cooldown would flap; the other two have none. A user who already has ANY
// destination or rule is left alone — that setup was deliberate — only the
// flag is set so this never runs again. Idempotent: called on every load, a
// no-op once SeededDefaultRules is true.
func seedDefaultAlertRules(n *models.NotificationConfig) {
	if n.SeededDefaultRules {
		return
	}
	if len(n.Destinations) == 0 && len(n.Rules) == 0 {
		n.Rules = append(n.Rules,
			models.AlertRule{
				ID:      newRuleID(),
				Name:    "Promotions & demotions",
				Enabled: true,
				Match:   "any",
				Conditions: []models.Condition{
					{Field: "promoted", Op: "is_true"},
					{Field: "demoted", Op: "is_true"},
				},
			},
			models.AlertRule{
				ID:         newRuleID(),
				Name:       "Target met",
				Enabled:    true,
				Match:      "all",
				Conditions: []models.Condition{{Field: "target_met", Op: "is_true"}},
			},
			models.AlertRule{
				ID:           newRuleID(),
				Name:         "Ratio approaching minimum",
				Enabled:      true,
				Match:        "all",
				Conditions:   []models.Condition{{Field: "ratio_min_eta_days", Op: "lte", Value: "14"}},
				CooldownMins: 1440,
			},
		)
	}
	n.SeededDefaultRules = true
}

// newRuleID mints a random ID for a seeded rule — same shape as the IDs the
// /api/notifications handler assigns to rules saved without one.
func newRuleID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func defaultConfig() models.Config {
	return models.Config{
		Server:   models.ServerConfig{Host: "0.0.0.0", Port: 8420},
		Trackers: []models.Tracker{},
		Settings: models.DefaultSettings(),
	}
}

// applyDefaults fills zero values that must never be zero.
func (m *Manager) applyDefaults() {
	if m.cfg.Server.Host == "" {
		m.cfg.Server.Host = "0.0.0.0"
	}
	if m.cfg.Server.Port == 0 {
		m.cfg.Server.Port = 8420
	}
	if m.cfg.Settings.ScrapeIntervalMinutes < 60 {
		m.cfg.Settings.ScrapeIntervalMinutes = 60
	}
	if m.cfg.Settings.QUIEnabledInstances == nil {
		m.cfg.Settings.QUIEnabledInstances = []int{}
	}
	if m.cfg.Trackers == nil {
		m.cfg.Trackers = []models.Tracker{}
	}
	normalizeBackup(&m.cfg.Settings)
	normalizeDigest(&m.cfg.Notifications.Digest)
}

// normalizeDigest defaults the weekly digest schedule to Monday 09:00 the
// first time an install loads it — detected by every field still being at
// its Go zero value (slices can't join a == struct comparison, so this is
// checked field-by-field rather than against a literal models.DigestConfig{}).
// Once the user has touched Alerts at all (even just to enable it, or pick a
// destination) at least one field is non-zero and this never fires again.
func normalizeDigest(dig *models.DigestConfig) {
	if !dig.Enabled && dig.Weekday == 0 && dig.Hour == 0 &&
		len(dig.Destinations) == 0 && dig.LastSentAt == 0 && len(dig.LastReadyTargets) == 0 {
		dig.Weekday = 1
		dig.Hour = 9
	}
}

// normalizeBackup applies safe defaults/bounds to the backup settings.
func normalizeBackup(s *models.Settings) {
	switch s.BackupFrequency {
	case "daily", "weekly", "monthly":
		// ok
	default:
		s.BackupFrequency = "weekly"
	}
	if s.BackupKeep <= 0 {
		s.BackupKeep = 5
	}
	if s.BackupKeep > 99 {
		s.BackupKeep = 99
	}
}

// Snapshot returns a copy of the full config.
func (m *Manager) Snapshot() models.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.copyLocked()
}

// Server returns the server config.
func (m *Manager) Server() models.ServerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Server
}

// Settings returns a copy of the settings.
func (m *Manager) Settings() models.Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.cfg.Settings
	s.QUIEnabledInstances = append([]int(nil), s.QUIEnabledInstances...)
	return s
}

// Trackers returns a copy of all trackers.
func (m *Manager) Trackers() []models.Tracker {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return copyTrackers(m.cfg.Trackers)
}

// Tracker returns the tracker with the given id.
func (m *Manager) Tracker(id string) (models.Tracker, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.cfg.Trackers {
		if t.ID == id {
			return copyTracker(t), true
		}
	}
	return models.Tracker{}, false
}

// AddTracker appends a tracker and persists.
func (m *Manager) AddTracker(t models.Tracker) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.Trackers = append(m.cfg.Trackers, t)
	return m.saveLocked()
}

// UpdateTracker replaces the tracker with matching ID via the mutate callback.
func (m *Manager) UpdateTracker(id string, mutate func(*models.Tracker)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.cfg.Trackers {
		if m.cfg.Trackers[i].ID == id {
			mutate(&m.cfg.Trackers[i])
			return m.saveLocked()
		}
	}
	return fmt.Errorf("tracker %q not found", id)
}

// DeleteTracker removes a tracker by id.
func (m *Manager) DeleteTracker(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.cfg.Trackers {
		if m.cfg.Trackers[i].ID == id {
			m.cfg.Trackers = append(m.cfg.Trackers[:i], m.cfg.Trackers[i+1:]...)
			return m.saveLocked()
		}
	}
	return fmt.Errorf("tracker %q not found", id)
}

// Notifications returns a copy of the notification config (destinations + rules).
func (m *Manager) Notifications() models.NotificationConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return copyNotifications(m.cfg.Notifications)
}

// UpdateNotifications replaces the notification config and persists.
func (m *Manager) UpdateNotifications(n models.NotificationConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n.Destinations == nil {
		n.Destinations = []models.NotifyDestination{}
	}
	if n.Rules == nil {
		n.Rules = []models.AlertRule{}
	}
	if n.Digest.Destinations == nil {
		n.Digest.Destinations = []string{}
	}
	m.cfg.Notifications = n
	return m.saveLocked()
}

// UpdateDigestState persists only the weekly digest's server-maintained
// fields (LastSentAt/LastReadyTargets) after a send — used instead of
// UpdateNotifications so it never round-trips the whole client-shaped
// NotificationConfig (which would clobber destinations/rules edited
// concurrently in another tab).
func (m *Manager) UpdateDigestState(lastSentAt int64, lastReadyTargets []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.Notifications.Digest.LastSentAt = lastSentAt
	m.cfg.Notifications.Digest.LastReadyTargets = append([]string(nil), lastReadyTargets...)
	return m.saveLocked()
}

func copyNotifications(n models.NotificationConfig) models.NotificationConfig {
	// make (not append-to-nil) so empty marshals as [] rather than null.
	out := models.NotificationConfig{
		Destinations:       make([]models.NotifyDestination, len(n.Destinations)),
		Rules:              make([]models.AlertRule, len(n.Rules)),
		SeededDefaultRules: n.SeededDefaultRules,
		Digest:             n.Digest,
	}
	copy(out.Destinations, n.Destinations)
	for i, r := range n.Rules {
		r.Conditions = append([]models.Condition(nil), r.Conditions...)
		r.Destinations = append([]string(nil), r.Destinations...)
		out.Rules[i] = r
	}
	out.Digest.Destinations = append([]string(nil), n.Digest.Destinations...)
	out.Digest.LastReadyTargets = append([]string(nil), n.Digest.LastReadyTargets...)
	return out
}

// UpdateSettings replaces the settings (server config untouched) and persists.
func (m *Manager) UpdateSettings(s models.Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ScrapeIntervalMinutes < 60 {
		s.ScrapeIntervalMinutes = 60
	}
	if s.QUIEnabledInstances == nil {
		s.QUIEnabledInstances = []int{}
	}
	normalizeBackup(&s)
	m.cfg.Settings = s
	return m.saveLocked()
}

// Reset restores a fresh default config (no trackers, default settings, no
// notifications) while preserving the server host/port so the app stays
// reachable. Used by the login-reset recovery flow.
func (m *Manager) Reset() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	server := m.cfg.Server
	m.cfg = defaultConfig()
	m.cfg.Server = server
	return m.saveLocked()
}

// saveLocked writes the config atomically (tmp file + rename).
// Caller must hold the write lock (or be in Open before publishing).
func (m *Manager) saveLocked() error {
	data, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, m.path)
}

func (m *Manager) copyLocked() models.Config {
	c := m.cfg
	c.Trackers = copyTrackers(m.cfg.Trackers)
	c.Settings.QUIEnabledInstances = append([]int(nil), m.cfg.Settings.QUIEnabledInstances...)
	return c
}

func copyTrackers(in []models.Tracker) []models.Tracker {
	out := make([]models.Tracker, len(in))
	for i, t := range in {
		out[i] = copyTracker(t)
	}
	return out
}

func copyTracker(t models.Tracker) models.Tracker {
	if t.Targets != nil {
		targets := make(map[string]string, len(t.Targets))
		for k, v := range t.Targets {
			targets[k] = v
		}
		t.Targets = targets
	}
	return t
}
