package api

import (
	"strings"
	"testing"
	"time"
)

// rfcSecret is the shared secret from RFC 4226 Appendix D / RFC 6238 Appendix
// B — the ASCII string "12345678901234567890", base32-encoded.
//
// NOT A CREDENTIAL. Secret scanners flag this as a generic API key; it is
// published standards text, the fixture every TOTP implementation is checked
// against, and there is nothing behind it to rotate. It must not be replaced
// with a generated value: the point of the test is that our output matches the
// codes printed in the RFC, which only holds for this exact input.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" //nolint:gosec // RFC 4226 test vector

// TestTOTPCodeRFCVectors checks the HOTP core against the published test
// values in RFC 4226 Appendix D. TOTP is HOTP with the counter derived from
// the clock, so matching these pins the HMAC, the dynamic truncation and the
// decimal reduction all at once.
func TestTOTPCodeRFCVectors(t *testing.T) {
	want := []string{
		"755224", "287082", "359152", "969429", "338314",
		"254676", "287922", "162583", "399871", "520489",
	}
	for counter, expect := range want {
		got, err := totpCode(rfcSecret, int64(counter))
		if err != nil {
			t.Fatalf("counter %d: %v", counter, err)
		}
		if got != expect {
			t.Errorf("counter %d: got %s, want %s", counter, got, expect)
		}
	}
}

// TestTOTPStepDerivation: the counter is whole 30-second periods since the
// epoch, so codes change on the half-minute and not a second either side.
func TestTOTPStepDerivation(t *testing.T) {
	cases := []struct {
		unix int64
		want int64
	}{
		{0, 0}, {29, 0}, {30, 1}, {59, 1}, {60, 2}, {1234567890, 41152263},
	}
	for _, c := range cases {
		if got := totpStep(time.Unix(c.unix, 0)); got != c.want {
			t.Errorf("totpStep(%d) = %d, want %d", c.unix, got, c.want)
		}
	}
}

// TestVerifyTOTPSkew: a code from the adjacent step is accepted (phone clocks
// drift), but one two steps away is not.
func TestVerifyTOTPSkew(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cur := totpStep(now)

	for _, delta := range []int64{-1, 0, 1} {
		code, err := totpCode(rfcSecret, cur+delta)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := verifyTOTP(rfcSecret, code, now, 0); !ok {
			t.Errorf("code from step %+d should be accepted", delta)
		}
	}
	for _, delta := range []int64{-2, 2, 10} {
		code, err := totpCode(rfcSecret, cur+delta)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := verifyTOTP(rfcSecret, code, now, 0); ok {
			t.Errorf("code from step %+d should be rejected", delta)
		}
	}
}

// TestVerifyTOTPRejectsReplay: once a step is spent, the same code must not
// work again for the rest of its window. Without this a code seen in a
// screenshot or a proxy log stays usable for up to 30 seconds.
func TestVerifyTOTPRejectsReplay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	code, err := totpCode(rfcSecret, totpStep(now))
	if err != nil {
		t.Fatal(err)
	}
	step, ok := verifyTOTP(rfcSecret, code, now, 0)
	if !ok {
		t.Fatal("first use should be accepted")
	}
	if _, ok := verifyTOTP(rfcSecret, code, now, step); ok {
		t.Fatal("replaying a consumed code must be rejected")
	}
	// The step before the consumed one is also closed off, so an attacker
	// can't fall back to the skew window.
	prev, err := totpCode(rfcSecret, totpStep(now)-1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := verifyTOTP(rfcSecret, prev, now, step); ok {
		t.Fatal("a code older than the last consumed step must be rejected")
	}
}

// TestVerifyTOTPRejectsMalformed: wrong lengths, non-digits and an empty
// secret are turned away before any comparison happens.
func TestVerifyTOTPRejectsMalformed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, code := range []string{"", "12345", "1234567", "abcdef", "  "} {
		if _, ok := verifyTOTP(rfcSecret, code, now, 0); ok {
			t.Errorf("malformed code %q was accepted", code)
		}
	}
	good, _ := totpCode(rfcSecret, totpStep(now))
	if _, ok := verifyTOTP("", good, now, 0); ok {
		t.Error("an empty secret must never verify")
	}
}

// TestNormalizeTOTPSecret: users paste the secret with the display grouping
// and in whatever case they happened to copy.
func TestNormalizeTOTPSecret(t *testing.T) {
	want := rfcSecret
	for _, in := range []string{
		rfcSecret,
		strings.ToLower(rfcSecret),
		groupSecret(rfcSecret),
		"  " + rfcSecret + "  ",
		rfcSecret + "==",
	} {
		if got := normalizeTOTPSecret(in); got != want {
			t.Errorf("normalizeTOTPSecret(%q) = %q", in, got)
		}
	}
}

// TestNewTOTPSecretDecodes: generated secrets are valid base32 of the full
// length, and don't repeat.
func TestNewTOTPSecretDecodes(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		s, err := newTOTPSecret()
		if err != nil {
			t.Fatal(err)
		}
		if seen[s] {
			t.Fatal("newTOTPSecret returned a duplicate")
		}
		seen[s] = true
		b, err := totpBase32.DecodeString(s)
		if err != nil {
			t.Fatalf("secret %q does not decode: %v", s, err)
		}
		if len(b) != totpSecretBytes {
			t.Fatalf("secret decoded to %d bytes, want %d", len(b), totpSecretBytes)
		}
		if strings.Contains(s, "=") {
			t.Fatal("secret must be unpadded — padding breaks several authenticator apps")
		}
	}
}

// TestTOTPURI: the enrolment URI carries every parameter an authenticator
// needs, so no app has to fall back to its own defaults.
func TestTOTPURI(t *testing.T) {
	uri := totpURI("Yata", "mystery", rfcSecret)
	if !strings.HasPrefix(uri, "otpauth://totp/Yata:mystery?") {
		t.Fatalf("unexpected URI shape: %s", uri)
	}
	for _, want := range []string{
		"secret=" + rfcSecret, "issuer=Yata", "algorithm=SHA1", "digits=6", "period=30",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("URI missing %q: %s", want, uri)
		}
	}
}

// ── Recovery codes ───────────────────────────────────────────────────────────

// TestRecoveryCodeShape: codes use only the reduced alphabet and carry the
// grouping dash, so they can be read off a screen without ambiguity.
func TestRecoveryCodeShape(t *testing.T) {
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != recoveryCodeCount || len(hashes) != recoveryCodeCount {
		t.Fatalf("got %d codes / %d hashes, want %d each", len(codes), len(hashes), recoveryCodeCount)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Fatal("duplicate recovery code issued")
		}
		seen[c] = true
		if len(c) != recoveryCodeChars+1 || c[recoveryCodeChars/2] != '-' {
			t.Fatalf("malformed code %q", c)
		}
		for _, r := range strings.ReplaceAll(c, "-", "") {
			if !strings.ContainsRune(recoveryAlphabet, r) {
				t.Fatalf("code %q uses out-of-alphabet character %q", c, r)
			}
		}
		if !looksLikeRecoveryCode(c) {
			t.Fatalf("code %q not recognised as a recovery code", c)
		}
	}
}

// TestNormalizeRecoveryCode folds the characters the alphabet omits onto the
// ones they get mistaken for, so a transcription slip still works.
func TestNormalizeRecoveryCode(t *testing.T) {
	cases := map[string]string{
		"K4M9P-2XQ7B": "K4M9P2XQ7B",
		"k4m9p-2xq7b": "K4M9P2XQ7B",
		"K4M9P 2XQ7B": "K4M9P2XQ7B",
		" K4M9P2XQ7B": "K4M9P2XQ7B",
		"IL0OU-12345": "1100V12345", // I/L→1, O→0, U→V
	}
	for in, want := range cases {
		if got := normalizeRecoveryCode(in); got != want {
			t.Errorf("normalizeRecoveryCode(%q) = %q, want %q", in, got, want)
		}
	}
	// A hash is stable across every spelling of the same code.
	if hashRecoveryCode("K4M9P-2XQ7B") != hashRecoveryCode("k4m9p 2xq7b") {
		t.Error("hashing must be insensitive to case and grouping")
	}
	if hashRecoveryCode("K4M9P-2XQ7B") == hashRecoveryCode("K4M9P-2XQ7C") {
		t.Error("different codes must hash differently")
	}
}

// TestLooksLikeRecoveryCode: the login field takes either factor, so the two
// must be distinguishable — a six-digit authenticator code is never mistaken
// for a recovery code.
func TestLooksLikeRecoveryCode(t *testing.T) {
	if looksLikeRecoveryCode("123456") {
		t.Error("a six-digit TOTP code must not look like a recovery code")
	}
	if looksLikeRecoveryCode("") || looksLikeRecoveryCode("K4M9P") {
		t.Error("short input must not look like a recovery code")
	}
	if !looksLikeRecoveryCode("K4M9P-2XQ7B") {
		t.Error("a well-formed recovery code should be recognised")
	}
}
