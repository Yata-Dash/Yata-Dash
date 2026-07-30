package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Setting allowed_hosts to "*" switches the Host-header check off, which is
// what stands between a Yata behind a domain and a DNS-rebinding attack. That
// is refused from the dashboard on purpose, so that turning it off needs
// access to the machine rather than a browser session.
//
// Import is the second way in. It replaces the entire settings object, so
// without the same check a crafted config file is simply a longer route to the
// setting the dashboard refuses.

func importSettings(t *testing.T, settings map[string]any) (*Manager, error) {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, err := json.Marshal(map[string]any{"settings": settings})
	if err != nil {
		t.Fatal(err)
	}
	return m, m.Import(data)
}

func TestImportRefusesTheAllowedHostsWildcard(t *testing.T) {
	m, err := importSettings(t, map[string]any{"allowed_hosts": []string{"*"}})
	if err == nil {
		t.Fatal("import accepted a \"*\" wildcard, disabling the host guard")
	}
	if !strings.Contains(err.Error(), "allowed_hosts") {
		t.Errorf("error should name the field, got %q", err)
	}
	// A refused import must not have applied any of it.
	if got := m.Settings().AllowedHosts; len(got) != 0 {
		t.Errorf("refused import still stored %v", got)
	}
}

func TestImportRefusesHostsThatAreNotHostnames(t *testing.T) {
	for _, bad := range []string{"https://yata.example.com", "yata.example.com/path", "a b"} {
		if _, err := importSettings(t, map[string]any{"allowed_hosts": []string{bad}}); err == nil {
			t.Errorf("import accepted %q as a hostname", bad)
		}
	}
}

func TestImportAcceptsRealHostnames(t *testing.T) {
	m, err := importSettings(t, map[string]any{
		"allowed_hosts": []string{" yata.example.com ", "", "box.tailnet.ts.net:8420"},
	})
	if err != nil {
		t.Fatalf("import rejected a legitimate host list: %v", err)
	}
	got := m.Settings().AllowedHosts
	if len(got) != 2 || got[0] != "yata.example.com" || got[1] != "box.tailnet.ts.net:8420" {
		t.Errorf("AllowedHosts = %v, want the two real entries trimmed", got)
	}
}

// UpdateSettings is the other write path, and the check belongs to the config
// layer rather than to either caller.
func TestUpdateSettingsRefusesTheWildcard(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s := m.Settings()
	s.AllowedHosts = []string{"*"}
	if err := m.UpdateSettings(s); err == nil {
		t.Fatal("UpdateSettings accepted a \"*\" wildcard")
	}
	if got := m.Settings().AllowedHosts; len(got) != 0 {
		t.Errorf("refused update still stored %v", got)
	}
}
