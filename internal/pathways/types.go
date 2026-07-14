// Package pathways computes invite paths toward a target tracker using the
// community tracker-pathways dataset (defs/pathways/routes.json, converted
// from github.com/handokota/trackerpathways by tools/pathsync).
//
// The data is community-driven and may be wrong or out of date. Every API
// response carries the source attribution and a disclaimer; the UI must show
// them. Meeting listed requirements never guarantees an invite.
package pathways

import (
	"encoding/json"
	"os"
)

// Route is one invite edge: being on From (meeting Reqs, with Days account
// age) can lead to an invite to To.
type Route struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Days    int    `json:"days"` // min account age on From in days (-1 unknown)
	Reqs    string `json:"reqs"` // free-text requirements (community data)
	Active  bool   `json:"active"`
	Updated string `json:"updated,omitempty"`
}

// UnlockClass describes when a tracker's own invite privileges unlock.
type UnlockClass struct {
	Days int    `json:"days"` // typical days until invites unlock (-1 unknown)
	Text string `json:"text"` // "Class: reqs; Class2: reqs" free text
}

// SourceInfo is the dataset attribution shown in the UI disclosure.
type SourceInfo struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	License string `json:"license"`
	// Fetched is when Yata last pulled the snapshot (metadata only).
	Fetched string `json:"fetched"`
	// Updated is the actual freshness of the upstream DATA (YYYY-MM, the newest
	// route date). This — not Fetched — is the pathways "version" the update
	// check reports, so users see the real data date, not just when we grabbed it.
	Updated string `json:"updated,omitempty"`
}

// Data is the parsed routes.json file.
type Data struct {
	SchemaVersion int                    `json:"schema_version"`
	Source        SourceInfo             `json:"source"`
	Abbr          map[string]string      `json:"abbr,omitempty"`
	Routes        []Route                `json:"routes"`
	Unlocks       map[string]UnlockClass `json:"unlocks"`

	// byFrom / byTo are built on load.
	byFrom map[string][]Route
	byTo   map[string][]Route
	names  []string
}

// Load reads and indexes a routes.json file. A missing file is not an error
// for the caller to treat as fatal — the feature simply stays hidden.
func Load(path string) (*Data, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d Data
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	d.index()
	return &d, nil
}

func (d *Data) index() {
	d.byFrom = map[string][]Route{}
	d.byTo = map[string][]Route{}
	seen := map[string]bool{}
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			d.names = append(d.names, n)
		}
	}
	for _, r := range d.Routes {
		d.byFrom[r.From] = append(d.byFrom[r.From], r)
		d.byTo[r.To] = append(d.byTo[r.To], r)
		add(r.From)
		add(r.To)
	}
}

// Names returns every tracker name in the dataset (stable order: first seen).
func (d *Data) Names() []string { return d.names }

// From returns the outgoing routes of a tracker.
func (d *Data) From(name string) []Route { return d.byFrom[name] }

// To returns the incoming routes of a tracker.
func (d *Data) To(name string) []Route { return d.byTo[name] }
