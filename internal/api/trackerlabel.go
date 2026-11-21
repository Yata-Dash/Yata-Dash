package api

import (
	"strings"
	"unicode"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// trackerLabel is the name a tracker goes by in logs, alerts and the digest:
// its custom name, else its definition's name, else its URL's host, else its
// ID. A tracker added without a name stores "" and leaves the def to name it
// (toView does the same), so printing t.Name alone gave "fetch:  (id) failed".
// Control characters become spaces so a name can't start a new log line.
func trackerLabel(d *Deps, t models.Tracker) string {
	// Creating a tracker with no name and no matching def stores its URL as
	// the name; the host reads better and carries no path.
	if n := cleanLabel(t.Name); n != "" && !sameURL(n, t.URL) {
		return n
	}
	if d != nil && d.Reg != nil {
		if td, ok := d.Reg.TrackerByURL(t.URL); ok {
			if n := cleanLabel(td.Name); n != "" {
				return n
			}
		}
	}
	if h := hostOfURL(strings.TrimSpace(t.URL)); h != "" {
		return h
	}
	if id := cleanLabel(t.ID); id != "" {
		return id
	}
	return "unknown tracker"
}

func sameURL(name, rawURL string) bool {
	u := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	return u != "" && strings.EqualFold(strings.TrimRight(name, "/"), u)
}

// labelled returns a copy of t whose Name is its trackerLabel, for handing to
// code that prints t.Name (the alert engine). Never save the copy.
func labelled(d *Deps, t models.Tracker) models.Tracker {
	t.Name = trackerLabel(d, t)
	return t
}

// labelledAll is labelled over a list.
func labelledAll(d *Deps, ts []models.Tracker) []models.Tracker {
	out := make([]models.Tracker, len(ts))
	for i, t := range ts {
		out[i] = labelled(d, t)
	}
	return out
}

func cleanLabel(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
}
