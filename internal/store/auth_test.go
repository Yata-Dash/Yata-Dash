package store

import (
	"sync"
	"testing"
	"time"
)

// TestCreateFirstUserOnlyOnce: first-run setup must not be able to overwrite
// an account that already exists — that is what SetUser's upsert would do to
// the loser of two racing setups.
func TestCreateFirstUserOnlyOnce(t *testing.T) {
	db := testDB(t)

	created, err := db.CreateFirstUser("first", "hash-1")
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v, want true, nil", created, err)
	}
	created, err = db.CreateFirstUser("second", "hash-2")
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if created {
		t.Fatal("a second create reported success")
	}
	u, ok, err := db.GetUser()
	if err != nil || !ok {
		t.Fatalf("GetUser: ok=%v err=%v", ok, err)
	}
	if u.Username != "first" || u.PasswordHash != "hash-1" {
		t.Fatalf("account was overwritten: %q / %q", u.Username, u.PasswordHash)
	}
}

// TestCreateFirstUserConcurrent: however many race, exactly one wins.
func TestCreateFirstUserConcurrent(t *testing.T) {
	db := testDB(t)
	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			created, err := db.CreateFirstUser("racer", "hash")
			if err != nil {
				t.Errorf("CreateFirstUser: %v", err)
				return
			}
			if created {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d creates succeeded, want exactly one", wins)
	}
}

// TestClearSessionsExcept: everything but the named session goes.
func TestClearSessionsExcept(t *testing.T) {
	db := testDB(t)
	exp := time.Now().Add(time.Hour)
	for _, tok := range []string{"keep", "drop-1", "drop-2"} {
		if err := db.CreateSession(tok, exp); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ClearSessionsExcept("keep"); err != nil {
		t.Fatal(err)
	}
	for tok, want := range map[string]bool{"keep": true, "drop-1": false, "drop-2": false} {
		if ok, _ := db.SessionValid(tok, time.Now()); ok != want {
			t.Errorf("session %q valid = %v, want %v", tok, ok, want)
		}
	}
}
