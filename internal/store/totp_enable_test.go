package store

import (
	"testing"
	"time"
)

// Enabling 2FA is all or nothing, and only once per pending secret (QV-02).
func TestEnableTOTPIsAllOrNothing(t *testing.T) {
	db := testDB(t)
	if _, err := db.CreateFirstUser("alice", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetTOTPSecret("PENDING", false); err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	// A different pending secret (replaced by a newer start) changes nothing.
	if ok, err := db.EnableTOTP("OTHER", 1000, []string{"x1"}, now); err != nil || ok {
		t.Fatalf("enable with a stale secret = %v, %v; want false, nil", ok, err)
	}
	if u, _, _ := db.GetUser(); u.TOTPEnabled || u.TOTPLastStep != 0 {
		t.Fatalf("a refused enable changed the account: %+v", u)
	}
	if n, _ := db.RecoveryCodesLeft(); n != 0 {
		t.Fatalf("a refused enable stored %d recovery codes", n)
	}

	if ok, err := db.EnableTOTP("PENDING", 1000, []string{"a1", "a2"}, now); err != nil || !ok {
		t.Fatalf("enable = %v, %v; want true, nil", ok, err)
	}
	u, _, _ := db.GetUser()
	if !u.TOTPEnabled || u.TOTPLastStep != 1000 {
		t.Fatalf("after enable: enabled=%v last_step=%d, want true and the enrolment step", u.TOTPEnabled, u.TOTPLastStep)
	}

	// A second enable (a racing request) neither succeeds nor replaces the codes.
	if ok, err := db.EnableTOTP("PENDING", 1001, []string{"b1"}, now); err != nil || ok {
		t.Fatalf("second enable = %v, %v; want false, nil", ok, err)
	}
	if used, _ := db.UseRecoveryCode("a1", now); !used {
		t.Error("the first enable's recovery codes were replaced")
	}
	if used, _ := db.UseRecoveryCode("b1", now); used {
		t.Error("the losing enable's recovery code was stored")
	}
}
