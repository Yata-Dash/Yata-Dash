package stats

import (
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
	"github.com/Yata-Dash/Yata-Dash/internal/parse"
)

// EventGrace is how long a finished event stays in the merged stats (shown
// as "Ended") before it is dropped. The same 48h the fetcher applies when it
// reads a tracker's event list (fetch.eventGrace); this is the read-side
// copy, for the layers the fetcher never rewrites.
//
// A layer only changes when its source runs. Aither's scrape layer still
// held "Global freeleech mode activated, until Sep 5" three weeks later,
// because scraping had been switched off in the meantime and nothing ever
// replaced it — the API layer was fresh and had (correctly) dropped the
// event, and the merge then filled the gap from the stale scrape. Time-bound
// fields are the one kind of stat where "last known" is wrong once the time
// has passed, so they expire here, whatever layer they came from.
const EventGrace = 48 * time.Hour

// expireStaleEvents removes the flat banner fields and any structured event
// whose end is more than EventGrace ago.
func expireStaleEvents(out models.MergedStats, now time.Time) {
	staleBefore := now.Add(-EventGrace).Unix()
	if f, ok := out["active_event_ends_at"]; ok {
		if end := int64(parse.AnyFloat(f.Value)); end > 0 && end < staleBefore {
			delete(out, "active_event")
			delete(out, "active_event_ends_at")
		}
	}
	f, ok := out["active_events"]
	if !ok {
		return
	}
	list, ok := f.Value.([]any)
	if !ok {
		return
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		ev, _ := item.(map[string]any)
		if end := int64(parse.AnyFloat(ev["ends_at"])); end > 0 && end < staleBefore {
			continue
		}
		kept = append(kept, item)
	}
	switch {
	case len(kept) == 0:
		delete(out, "active_events")
	case len(kept) < len(list):
		f.Value = kept
		out["active_events"] = f
	}
}
