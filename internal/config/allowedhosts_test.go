package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// The check is an allowlist, not a list of banned characters. The version it
// replaced named the handful a URL would contain, which let everything else
// through — so a semicolon, a quote or a tag was stored as a "hostname" that
// could never match a real request.
func TestCleanAllowedHostsRejectsNonHostnames(t *testing.T) {
	bad := []string{
		"tracker;rm -rf",
		`<script>alert(1)</script>`,
		"name'--",
		`say"hi"`,
		"has space",
		"https://yata.example.com",
		"yata.example.com/path",
		"*",
		strings.Repeat("a", maxHostLen+1),
	}
	for _, h := range bad {
		if _, err := CleanAllowedHosts([]string{h}); err == nil {
			t.Errorf("CleanAllowedHosts(%q) was accepted, want an error", h)
		}
	}
}

func TestCleanAllowedHostsAcceptsRealNames(t *testing.T) {
	// Ports and IPv6 literals are legitimate: hostAllowed compares through
	// hostOnly, which strips both before matching.
	good := []string{"yata.example.com", "box.tailnet-name.ts.net", "localhost",
		"my_box", "A-B.example", "box.example.com:8420", "[::1]"}
	got, err := CleanAllowedHosts(good)
	if err != nil {
		t.Fatalf("CleanAllowedHosts(%v) = %v", good, err)
	}
	if len(got) != len(good) {
		t.Fatalf("kept %d of %d: %v", len(got), len(good), got)
	}
}

// A value stored before the check tightened must not lock the user out of
// their own settings: UpdateSettings validates the whole settings object, so
// one bad entry would refuse every unrelated change — including the change
// that removes it.
func TestLegacyBadHostIsDroppedOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	m, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Write one past the validator, the way an older build could have.
	m.mu.Lock()
	m.cfg.Settings.AllowedHosts = []string{"good.example.com", "mash;withsemicolon"}
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	hosts := reopened.Settings().AllowedHosts
	if len(hosts) != 1 || hosts[0] != "good.example.com" {
		t.Fatalf("hosts after reload = %v, want just the valid one", hosts)
	}

	// And an unrelated settings change now saves, which is the whole point.
	s := reopened.Settings()
	s.ShowFavicons = !s.ShowFavicons
	if err := reopened.UpdateSettings(s); err != nil {
		t.Errorf("an unrelated settings change was refused: %v", err)
	}
}

func TestPruneKeepsAValidList(t *testing.T) {
	s := models.Settings{AllowedHosts: []string{"a.example.com", "b.example.com"}}
	if pruneAllowedHosts(&s) {
		t.Error("a valid list was reported as changed")
	}
	if len(s.AllowedHosts) != 2 {
		t.Errorf("hosts = %v", s.AllowedHosts)
	}
}
