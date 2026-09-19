package api

import (
	"testing"
	"time"

	"github.com/Yata-Dash/Yata-Dash/internal/store"
)

// One notice per version. The daily check runs every day; the store's unread
// guard only holds until the notice is read, so a read notice must still stop
// the same version being raised again tomorrow — and a newer one must get
// through.
func TestAppUpdateIsAnnouncedOncePerVersion(t *testing.T) {
	d := testDeps(t)
	status := func(latest string) updateStatus {
		return updateStatus{App: updateComponent{Current: "Beta-20260901", Latest: latest, Available: latest != ""}}
	}
	count := func() int {
		_, n, err := d.DB.Alerts(store.AlertQuery{TrackerID: "app"})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	announceAppUpdate(d, status(""))
	if count() != 0 {
		t.Fatal("nothing available must raise nothing")
	}
	announceAppUpdate(d, status("Beta-20260910"))
	announceAppUpdate(d, status("Beta-20260910"))
	if count() != 1 {
		t.Fatalf("same version twice → %d alerts, want 1", count())
	}
	// Read it, then "the next day" the check finds the same version.
	first, _, _ := d.DB.Alerts(store.AlertQuery{TrackerID: "app"})
	if err := d.DB.MarkAlertsRead([]int64{first[0].ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	announceAppUpdate(d, status("Beta-20260910"))
	if count() != 1 {
		t.Fatalf("same version after reading → %d alerts, want 1", count())
	}
	announceAppUpdate(d, status("Beta-20260915"))
	if count() != 2 {
		t.Fatalf("newer version → %d alerts, want 2", count())
	}
	alerts, _, _ := d.DB.Alerts(store.AlertQuery{TrackerID: "app"})
	if alerts[0].URL != releasesURL || alerts[0].Title != "Yata Beta-20260915 is available" {
		t.Errorf("newest = %+v", alerts[0])
	}

	// The 0915 notice is still unread when 0920 arrives. The store allows one
	// unread per origin, so without superseding, the newer one would be
	// silently dropped and the panel would go on pointing at the old release.
	announceAppUpdate(d, status("Beta-20260920"))
	alerts, _, _ = d.DB.Alerts(store.AlertQuery{TrackerID: "app"})
	if len(alerts) != 3 || alerts[0].Title != "Yata Beta-20260920 is available" {
		t.Fatalf("newer version behind an unread notice: %+v", alerts)
	}
	if alerts[0].ReadAt != 0 || alerts[1].ReadAt == 0 {
		t.Errorf("the newest should be the unread one and the superseded one read: %+v", alerts[:2])
	}
}
