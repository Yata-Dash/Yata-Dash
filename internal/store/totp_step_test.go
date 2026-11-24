package store

import (
	"sync"
	"sync/atomic"
	"testing"
)

// A TOTP step is spent exactly once, however many requests race for it (SR-07).
func TestConsumeTOTPStepIsAtomic(t *testing.T) {
	db := testDB(t)
	if _, err := db.CreateFirstUser("alice", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetTOTPSecret("SECRET", true); err != nil {
		t.Fatal(err)
	}

	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := db.ConsumeTOTPStep(1000)
			if err != nil {
				t.Error(err)
			}
			if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := wins.Load(); n != 1 {
		t.Fatalf("step 1000 was spent %d times, want exactly 1", n)
	}

	// An earlier step can't be spent after a later one, and a later one can.
	if ok, _ := db.ConsumeTOTPStep(999); ok {
		t.Error("an earlier step was accepted after step 1000 was spent")
	}
	if ok, _ := db.ConsumeTOTPStep(1001); !ok {
		t.Error("the next step was refused")
	}
}
