package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RFC 6238 time-based one-time passwords, implemented on the standard library
// so 2FA costs the project no new dependency. The parameters below are the
// ones every authenticator app assumes by default (SHA-1, 6 digits, 30s) —
// they are weak-looking but fixed by interoperability, not by choice, and the
// security of the scheme rests on the secret rather than the hash.
const (
	totpDigits = 6
	totpPeriod = 30 * time.Second
	// totpSkew is how many steps either side of "now" are accepted, covering
	// ordinary clock drift between the phone and the server.
	totpSkew = 1
	// totpSecretBytes is the shared secret size. RFC 4226 requires at least
	// 128 bits and recommends 160 — the SHA-1 output length.
	totpSecretBytes = 20
)

// totpBase32 is the encoding authenticator apps expect: standard base32,
// unpadded (padding in an otpauth:// URI trips several popular apps).
var totpBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret generates a fresh base32-encoded shared secret.
func newTOTPSecret() (string, error) {
	b := make([]byte, totpSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return totpBase32.EncodeToString(b), nil
}

// totpStep is the RFC 6238 counter for a moment in time.
func totpStep(t time.Time) int64 { return t.Unix() / int64(totpPeriod.Seconds()) }

// totpCode computes the code for one counter value (the RFC 4226 HOTP
// construction: HMAC, then dynamic truncation to `totpDigits` decimal digits).
func totpCode(secret string, step int64) (string, error) {
	key, err := totpBase32.DecodeString(normalizeTOTPSecret(secret))
	if err != nil {
		return "", err
	}
	var ctr [8]byte
	binary.BigEndian.PutUint64(ctr[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(ctr[:])
	sum := mac.Sum(nil)
	// Dynamic truncation: the low nibble of the last byte picks the offset of
	// the 4-byte window that becomes the code.
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, v%mod), nil
}

// normalizeTOTPSecret makes a manually typed secret forgiving: base32 is
// case-insensitive and users paste it with the grouping spaces still in.
func normalizeTOTPSecret(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "")
	return strings.TrimRight(s, "=")
}

// verifyTOTP checks a submitted code against the secret, accepting ±totpSkew
// steps. It returns the step the code belongs to so the caller can record it
// and refuse a replay.
//
// lastStep is the most recently consumed step: any code at or before it is
// rejected outright. Without that, a code stays valid for its whole window
// and anyone who observed it — over a shoulder, in a screenshot, in a proxy
// log — can reuse it.
func verifyTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits || secret == "" {
		return 0, false
	}
	cur := totpStep(now)
	for d := -totpSkew; d <= totpSkew; d++ {
		step := cur + int64(d)
		if step <= lastStep {
			continue
		}
		want, err := totpCode(secret, step)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// totpURI builds the otpauth:// enrolment URI — the payload behind the QR and
// the thing every authenticator app knows how to import.
func totpURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(int(totpPeriod.Seconds())))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// groupSecret splits the secret into four-character groups. Manual entry means
// reading 32 characters off a screen and typing them into a phone; grouping is
// the difference between that being tolerable and being a source of typos.
func groupSecret(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ── Recovery codes ───────────────────────────────────────────────────────────

// recoveryCodeCount is how many single-use codes are issued per generation.
const recoveryCodeCount = 10

// recoveryAlphabet is Crockford base32: no I, L, O or U, so a code read off a
// screen and typed back can't be lost to 1/I or 0/O confusion.
const recoveryAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// recoveryCodeChars is the length of one code, giving 5 bits per character —
// 50 bits total. Far beyond reach of an online guess against the login
// lockout, and these are checked with a fast hash rather than bcrypt.
const recoveryCodeChars = 10

// newRecoveryCode returns one formatted code, e.g. "K4M9P-2XQ7B".
func newRecoveryCode() (string, error) {
	b := make([]byte, recoveryCodeChars)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, v := range b {
		if i == recoveryCodeChars/2 {
			sb.WriteByte('-')
		}
		// len(recoveryAlphabet) is 32, so the modulo is exact — no sampling bias.
		sb.WriteByte(recoveryAlphabet[int(v)%len(recoveryAlphabet)])
	}
	return sb.String(), nil
}

// newRecoveryCodes generates a full set along with the hashes to store.
func newRecoveryCodes() (codes []string, hashes []string, err error) {
	seen := map[string]bool{}
	for len(codes) < recoveryCodeCount {
		c, err := newRecoveryCode()
		if err != nil {
			return nil, nil, err
		}
		if seen[c] {
			continue // astronomically unlikely, but a duplicate would waste a slot
		}
		seen[c] = true
		codes = append(codes, c)
		hashes = append(hashes, hashRecoveryCode(c))
	}
	return codes, hashes, nil
}

// hashRecoveryCode hashes a code for storage and lookup. SHA-256 rather than
// bcrypt: the codes are full-entropy random rather than user-chosen, so there
// is no dictionary to slow down, and a direct hash lookup means checking one
// row instead of running bcrypt against every unused code on every attempt.
func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}

// normalizeRecoveryCode makes entry forgiving: case-insensitive, grouping
// dashes and spaces ignored, and the four characters the alphabet omits are
// folded onto the ones they're mistaken for.
func normalizeRecoveryCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	r := strings.NewReplacer("-", "", " ", "", "I", "1", "L", "1", "O", "0", "U", "V")
	return r.Replace(s)
}

// looksLikeRecoveryCode distinguishes a recovery code from a TOTP code at the
// login prompt, so one field can accept either.
func looksLikeRecoveryCode(s string) bool {
	return len(normalizeRecoveryCode(s)) == recoveryCodeChars
}
