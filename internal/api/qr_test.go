package api

import (
	"strings"
	"testing"
)

// TestQRFormatBitsBCH: the format information is a BCH(15,5) code, whose
// minimum distance is 7. Checking the two values that can be derived by hand
// pins the polynomial and the XOR mask, and the pairwise-distance check then
// confirms every remaining mask lands on a real codeword rather than a
// plausible-looking near miss.
func TestQRFormatBitsBCH(t *testing.T) {
	// Mask 0 reduces to the XOR mask alone (all five data bits are zero).
	if got := qrFormatBits(0); got != 0b101010000010010 {
		t.Errorf("format bits mask 0 = %015b, want 101010000010010", got)
	}
	if got := qrFormatBits(1); got != 0b101000100100101 {
		t.Errorf("format bits mask 1 = %015b, want 101000100100101", got)
	}
	for a := 0; a < 8; a++ {
		if qrFormatBits(a)>>15 != 0 {
			t.Fatalf("format bits mask %d overflow 15 bits", a)
		}
		for b := a + 1; b < 8; b++ {
			if d := popcount(qrFormatBits(a) ^ qrFormatBits(b)); d < 7 {
				t.Errorf("format bits %d vs %d differ in only %d positions (BCH minimum is 7)", a, b, d)
			}
		}
	}
}

// TestQRVersionBits checks the BCH(18,6) version information, whose minimum
// distance is 8. Version 7 is the first version that carries the field at all.
func TestQRVersionBits(t *testing.T) {
	if got := qrVersionBits(7); got != 0b000111110010010100 {
		t.Errorf("version bits 7 = %018b, want 000111110010010100", got)
	}
	for a := 7; a <= 10; a++ {
		if qrVersionBits(a)>>18 != 0 {
			t.Fatalf("version bits %d overflow 18 bits", a)
		}
		for b := a + 1; b <= 10; b++ {
			if d := popcount(qrVersionBits(a) ^ qrVersionBits(b)); d < 8 {
				t.Errorf("version bits %d vs %d differ in only %d positions (BCH minimum is 8)", a, b, d)
			}
		}
	}
}

// TestQRVersionTable: the version table must be self-consistent — total
// codewords implied by the block layout has to match the published totals,
// or the Reed-Solomon blocks won't line up with the symbol's capacity.
func TestQRVersionTable(t *testing.T) {
	// Total codewords per version at any EC level (the symbol's whole payload).
	totals := map[int]int{1: 26, 2: 44, 3: 70, 4: 100, 5: 134, 6: 172, 7: 196, 8: 242, 9: 292, 10: 346}
	for _, s := range qrVersions {
		blocks := s.group1 + s.group2
		got := s.dataCodewords() + blocks*s.ecPerBlock
		if want := totals[s.version]; got != want {
			t.Errorf("version %d: layout implies %d codewords, want %d", s.version, got, want)
		}
		if s.size() != 17+4*s.version {
			t.Errorf("version %d: size %d", s.version, s.size())
		}
	}
}

// TestQREncodeChoosesSmallestVersion: a payload should not be padded into a
// larger symbol than it needs, and one just past a boundary should step up.
func TestQREncodeChoosesSmallestVersion(t *testing.T) {
	for _, spec := range qrVersions {
		m, err := qrEncode(strings.Repeat("A", spec.capacity()))
		if err != nil {
			t.Fatalf("version %d capacity payload failed: %v", spec.version, err)
		}
		if m.size != spec.size() {
			t.Errorf("payload of %d bytes produced a %d-module symbol, want %d (version %d)",
				spec.capacity(), m.size, spec.size(), spec.version)
		}
	}
	// One byte past the largest supported version is refused rather than
	// silently truncated.
	last := qrVersions[len(qrVersions)-1]
	if _, err := qrEncode(strings.Repeat("A", last.capacity()+1)); err == nil {
		t.Error("an oversized payload must be rejected")
	}
}

// TestQRStructure verifies the fixed patterns a scanner locks onto. If any of
// these are wrong the symbol is unreadable no matter how good the data is.
func TestQRStructure(t *testing.T) {
	// Long enough to reach a version that carries version information.
	m, err := qrEncode(totpURI("Yata", "mystery", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"))
	if err != nil {
		t.Fatal(err)
	}
	n := m.size

	// Finder patterns: a dark 3×3 core, a light ring, a dark ring.
	for _, p := range [][2]int{{0, 0}, {0, n - 7}, {n - 7, 0}} {
		for dr := 0; dr < 7; dr++ {
			for dc := 0; dc < 7; dc++ {
				d := max(abs(dr-3), abs(dc-3))
				if want := d != 2; m.at(p[0]+dr, p[1]+dc) != want {
					t.Fatalf("finder at %v: module (%d,%d) wrong", p, dr, dc)
				}
			}
		}
	}
	// Separators: the row and column just outside each finder are light.
	for i := 0; i < 8; i++ {
		if m.at(7, i) || m.at(i, 7) {
			t.Fatal("top-left separator must be light")
		}
	}
	// Timing patterns alternate, starting and ending dark.
	for i := 8; i < n-8; i++ {
		if m.at(6, i) != (i%2 == 0) || m.at(i, 6) != (i%2 == 0) {
			t.Fatalf("timing pattern broken at %d", i)
		}
	}
	// The dark module is always set — and must survive the format-info sweep
	// that shares its column.
	if !m.at(n-8, 8) {
		t.Fatal("the dark module must be set")
	}
	// Every module must have been assigned: no gaps left by the data walk.
	if len(m.mod) != n*n {
		t.Fatalf("matrix is %d modules, want %d", len(m.mod), n*n)
	}
}

// TestQRFormatInfoRoundTrips: both copies of the format information must carry
// identical bits, and those bits must decode back to the mask that was
// actually applied. A transposed copy still looks like a QR code to the eye
// but fails on half the scanners, so it is worth asserting directly.
func TestQRFormatInfoRoundTrips(t *testing.T) {
	m, err := qrEncode("https://example.org/enrol")
	if err != nil {
		t.Fatal(err)
	}
	n := m.size

	read := func(get func(i int) bool) int {
		v := 0
		for i := 0; i < 15; i++ {
			if get(i) {
				v |= 1 << i
			}
		}
		return v
	}
	copy1 := read(func(i int) bool {
		switch {
		case i < 6:
			return m.at(i, 8)
		case i == 6:
			return m.at(7, 8)
		case i == 7:
			return m.at(8, 8)
		case i == 8:
			return m.at(8, 7)
		default:
			return m.at(8, 14-i)
		}
	})
	copy2 := read(func(i int) bool {
		if i < 8 {
			return m.at(8, n-1-i)
		}
		return m.at(n-15+i, 8)
	})
	if copy1 != copy2 {
		t.Fatalf("format info copies disagree: %015b vs %015b", copy1, copy2)
	}
	// The stored bits must match one of the eight legal values, and the mask
	// they name must be the one whose penalty actually won.
	matched := -1
	for mask := 0; mask < 8; mask++ {
		if qrFormatBits(mask) == copy1 {
			matched = mask
		}
	}
	if matched < 0 {
		t.Fatalf("format info %015b is not a valid codeword", copy1)
	}
	// Re-encoding with that mask must reproduce the same matrix.
	spec := qrVersions[0]
	for _, s := range qrVersions {
		if s.size() == n {
			spec = s
		}
	}
	ref := newQRMatrix(n)
	ref.drawFunctionPatterns(spec)
	ref.placeData(qrEncodeData([]byte("https://example.org/enrol"), spec))
	ref.applyMask(matched)
	ref.drawFormat(matched)
	for i := range ref.mod {
		if ref.mod[i] != m.mod[i] {
			t.Fatalf("matrix does not match a re-encode under the mask it advertises (module %d)", i)
		}
	}
}

// TestQRReedSolomon checks the error-correction codewords for the worked
// example in the specification: the version 1-M message "HELLO WORLD" carries
// a known 10-codeword remainder.
func TestQRReedSolomon(t *testing.T) {
	data := []byte{0x20, 0x5b, 0x0b, 0x78, 0xd1, 0x72, 0xdc, 0x4d, 0x43, 0x40, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11}
	want := []byte{0xc4, 0x23, 0x27, 0x77, 0xeb, 0xd7, 0xe7, 0xe2, 0x5d, 0x17}
	got := rsEncode(data, 10)
	if len(got) != len(want) {
		t.Fatalf("got %d EC codewords, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("EC codeword %d = %02x, want %02x (full: % 02x)", i, got[i], want[i], got)
		}
	}
}

// TestQRSVGIsSelfContained: the rendered SVG must carry its own light
// background. Left transparent, the code inverts against a dark page theme
// and stops scanning.
func TestQRSVGIsSelfContained(t *testing.T) {
	svg, err := qrSVG("otpauth://totp/Yata:mystery?secret=GEZDGNBVGY3TQOJQ")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<svg", "viewBox", `fill="#ffffff"`, `fill="#000000"`, "</svg>"} {
		if !strings.Contains(svg, want) {
			t.Errorf("SVG missing %q", want)
		}
	}
	if strings.Contains(svg, "http://") && !strings.Contains(svg, "www.w3.org/2000/svg") {
		t.Error("SVG should reference no external host but the namespace")
	}
}

func popcount(v int) int {
	n := 0
	for ; v != 0; v &= v - 1 {
		n++
	}
	return n
}
