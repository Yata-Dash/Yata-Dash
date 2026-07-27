package api

import (
	"errors"
	"fmt"
	"strings"
)

// A self-contained QR encoder, just large enough to render an otpauth:// URI
// for 2FA enrolment. Written rather than pulled in because it is the only
// thing 2FA would have needed a dependency for, and a build-time dependency
// that renders a secret is exactly the kind we would rather not have.
//
// Scope is deliberately narrow: byte mode, error-correction level M, versions
// 1–10 (up to ~200 characters). Anything a longer payload would need — other
// modes, other levels, versions 11+ — is absent because nothing here produces
// such a payload, and the unused generality would go untested.
//
// Output was checked against jsQR, an independent decoder, across the whole
// supported range (versions 1, 4, 7, 8 and 10 — covering the two-group block
// layouts and the version-information block that versions 7+ carry). The
// tests here cover the parts a decoder can't see: the BCH codes, the block
// layout table, and the fixed patterns.

// qrECLevel M is the only level implemented: it tolerates ~15% damage, which
// for a code displayed on a screen and scanned once is ample.
const qrFormatECBits = 0b00 // level M, per the format-information table

// qrVersionSpec describes one version at level M. Data codewords are split
// into blocks of two sizes (group 2 is empty for most versions); every block
// carries ecPerBlock error-correction codewords.
type qrVersionSpec struct {
	version    int
	ecPerBlock int
	group1     int // number of blocks
	group1Data int // data codewords per block
	group2     int
	group2Data int
	// alignment holds the alignment-pattern centre coordinates. Centres are
	// every pairing of these values, minus the three that collide with the
	// finder patterns.
	alignment []int
}

var qrVersions = []qrVersionSpec{
	{1, 10, 1, 16, 0, 0, nil},
	{2, 16, 1, 28, 0, 0, []int{6, 18}},
	{3, 26, 1, 44, 0, 0, []int{6, 22}},
	{4, 18, 2, 32, 0, 0, []int{6, 26}},
	{5, 24, 2, 43, 0, 0, []int{6, 30}},
	{6, 16, 4, 27, 0, 0, []int{6, 34}},
	{7, 18, 4, 31, 0, 0, []int{6, 22, 38}},
	{8, 22, 2, 38, 2, 39, []int{6, 24, 42}},
	{9, 22, 3, 36, 2, 37, []int{6, 26, 46}},
	{10, 26, 4, 43, 1, 44, []int{6, 28, 50}},
}

func (s qrVersionSpec) dataCodewords() int {
	return s.group1*s.group1Data + s.group2*s.group2Data
}

func (s qrVersionSpec) size() int { return 17 + 4*s.version }

// charCountBits is the width of the byte-mode character-count field: 8 bits
// for versions 1–9, 16 from version 10.
func (s qrVersionSpec) charCountBits() int {
	if s.version < 10 {
		return 8
	}
	return 16
}

// qrCapacity is how many payload bytes fit: the data codewords less the mode
// indicator and character count.
func (s qrVersionSpec) capacity() int {
	return s.dataCodewords() - (4+s.charCountBits())/8 - 1
}

// ── GF(256) arithmetic ───────────────────────────────────────────────────────

// Reed-Solomon over GF(256) with the QR primitive polynomial x^8+x^4+x^3+x^2+1.
var gfExp [512]byte
var gfLog [256]byte

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	// Duplicated tail so exponents can be added without wrapping by hand.
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// rsGenerator builds the degree-n generator polynomial, the product of
// (x - α^i) for i in [0, n).
func rsGenerator(n int) []byte {
	g := []byte{1}
	for i := 0; i < n; i++ {
		next := make([]byte, len(g)+1)
		for j, c := range g {
			next[j] ^= c
			next[j+1] ^= gfMul(c, gfExp[i])
		}
		g = next
	}
	return g
}

// rsEncode returns the n error-correction codewords for one data block.
func rsEncode(data []byte, n int) []byte {
	gen := rsGenerator(n)
	rem := make([]byte, len(data)+n)
	copy(rem, data)
	for i := 0; i < len(data); i++ {
		lead := rem[i]
		if lead == 0 {
			continue
		}
		for j, g := range gen {
			rem[i+j] ^= gfMul(g, lead)
		}
	}
	return rem[len(data):]
}

// ── Bit assembly ─────────────────────────────────────────────────────────────

type bitBuffer struct {
	bits []bool
}

func (b *bitBuffer) append(v, n int) {
	for i := n - 1; i >= 0; i-- {
		b.bits = append(b.bits, v&(1<<i) != 0)
	}
}

func (b *bitBuffer) bytes() []byte {
	out := make([]byte, (len(b.bits)+7)/8)
	for i, on := range b.bits {
		if on {
			out[i/8] |= 1 << (7 - i%8)
		}
	}
	return out
}

// qrEncodeData turns the payload into the final interleaved codeword stream.
func qrEncodeData(payload []byte, spec qrVersionSpec) []byte {
	var buf bitBuffer
	buf.append(0b0100, 4) // byte mode
	buf.append(len(payload), spec.charCountBits())
	for _, c := range payload {
		buf.append(int(c), 8)
	}
	total := spec.dataCodewords() * 8
	// Terminator: up to four zero bits, truncated if the capacity ends sooner.
	for i := 0; i < 4 && len(buf.bits) < total; i++ {
		buf.bits = append(buf.bits, false)
	}
	for len(buf.bits)%8 != 0 {
		buf.bits = append(buf.bits, false)
	}
	data := buf.bytes()
	// Fill the remaining codewords with the two alternating pad bytes the
	// specification fixes.
	pad := []byte{0xec, 0x11}
	for i := 0; len(data) < spec.dataCodewords(); i++ {
		data = append(data, pad[i%2])
	}

	// Split into blocks, error-correct each, then interleave: codeword i of
	// every block in turn, so a burst of damage is spread across blocks.
	type block struct{ data, ec []byte }
	blocks := make([]block, 0, spec.group1+spec.group2)
	pos := 0
	for i := 0; i < spec.group1; i++ {
		d := data[pos : pos+spec.group1Data]
		pos += spec.group1Data
		blocks = append(blocks, block{d, rsEncode(d, spec.ecPerBlock)})
	}
	for i := 0; i < spec.group2; i++ {
		d := data[pos : pos+spec.group2Data]
		pos += spec.group2Data
		blocks = append(blocks, block{d, rsEncode(d, spec.ecPerBlock)})
	}

	var out []byte
	maxData := spec.group1Data
	if spec.group2Data > maxData {
		maxData = spec.group2Data
	}
	for i := 0; i < maxData; i++ {
		for _, b := range blocks {
			if i < len(b.data) {
				out = append(out, b.data[i])
			}
		}
	}
	for i := 0; i < spec.ecPerBlock; i++ {
		for _, b := range blocks {
			out = append(out, b.ec[i])
		}
	}
	return out
}

// ── Matrix construction ──────────────────────────────────────────────────────

type qrMatrix struct {
	size int
	mod  []bool // module is dark
	fn   []bool // module is a function pattern (never carries data, never masked)
}

func newQRMatrix(size int) *qrMatrix {
	return &qrMatrix{size: size, mod: make([]bool, size*size), fn: make([]bool, size*size)}
}

func (m *qrMatrix) at(r, c int) bool     { return m.mod[r*m.size+c] }
func (m *qrMatrix) isFn(r, c int) bool   { return m.fn[r*m.size+c] }
func (m *qrMatrix) set(r, c int, v bool) { m.mod[r*m.size+c] = v }

func (m *qrMatrix) setFn(r, c int, v bool) {
	m.mod[r*m.size+c] = v
	m.fn[r*m.size+c] = true
}

// drawFunctionPatterns lays down everything whose position is fixed by the
// specification: finders, separators, timing, alignment, the dark module, and
// the reserved format/version areas.
func (m *qrMatrix) drawFunctionPatterns(spec qrVersionSpec) {
	n := m.size
	// Three finder patterns with their one-module separators.
	for _, p := range [][2]int{{0, 0}, {0, n - 7}, {n - 7, 0}} {
		for dr := -1; dr <= 7; dr++ {
			for dc := -1; dc <= 7; dc++ {
				r, c := p[0]+dr, p[1]+dc
				if r < 0 || r >= n || c < 0 || c >= n {
					continue
				}
				// The finder is a 7×7 ring: dark border, light gap, dark 3×3 core.
				d := max(abs(dr-3), abs(dc-3))
				m.setFn(r, c, d != 2 && d <= 3)
			}
		}
	}
	// Timing patterns — alternating modules joining the finders.
	for i := 8; i < n-8; i++ {
		m.setFn(6, i, i%2 == 0)
		m.setFn(i, 6, i%2 == 0)
	}
	// Alignment patterns at every pairing of the centre coordinates, except
	// the three that would sit on a finder.
	for _, r := range spec.alignment {
		for _, c := range spec.alignment {
			if (r == 6 && c == 6) || (r == 6 && c == n-7) || (r == n-7 && c == 6) {
				continue
			}
			for dr := -2; dr <= 2; dr++ {
				for dc := -2; dc <= 2; dc++ {
					m.setFn(r+dr, c+dc, max(abs(dr), abs(dc)) != 1)
				}
			}
		}
	}
	// Reserve the format-information areas (written after masking). The first
	// copy wraps the top-left finder; the second is split between the top-right
	// row (8 modules) and the bottom-left column (7 — the eighth position in
	// that column is the dark module, not format data).
	for i := 0; i < 9; i++ {
		if !m.isFn(8, i) {
			m.setFn(8, i, false)
		}
		if !m.isFn(i, 8) {
			m.setFn(i, 8, false)
		}
	}
	for i := 0; i < 8; i++ {
		m.setFn(8, n-1-i, false)
		if i < 7 {
			m.setFn(n-1-i, 8, false)
		}
	}
	// The dark module: always set, always here. Written after the reservations
	// so their sweep can't clear it.
	m.setFn(n-8, 8, true)
	// Version information, present from version 7 onward.
	if spec.version >= 7 {
		v := qrVersionBits(spec.version)
		for i := 0; i < 18; i++ {
			bit := v&(1<<i) != 0
			m.setFn(i/3, n-11+i%3, bit)
			m.setFn(n-11+i%3, i/3, bit)
		}
	}
}

// placeData walks the two-module-wide columns from the bottom right, upward
// then downward, skipping function modules and the vertical timing column.
func (m *qrMatrix) placeData(codewords []byte) {
	n := m.size
	bit := 0
	upward := true
	for right := n - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5 // column 6 is the timing pattern; shift past it
		}
		for i := 0; i < n; i++ {
			r := i
			if upward {
				r = n - 1 - i
			}
			for _, c := range []int{right, right - 1} {
				if m.isFn(r, c) {
					continue
				}
				on := false
				if bit < len(codewords)*8 {
					on = codewords[bit/8]&(1<<(7-bit%8)) != 0
				}
				m.set(r, c, on)
				bit++
			}
		}
		upward = !upward
	}
}

// qrMaskFn reports whether the mask inverts the module at (r, c).
func qrMaskFn(mask, r, c int) bool {
	switch mask {
	case 0:
		return (r+c)%2 == 0
	case 1:
		return r%2 == 0
	case 2:
		return c%3 == 0
	case 3:
		return (r+c)%3 == 0
	case 4:
		return (r/2+c/3)%2 == 0
	case 5:
		return (r*c)%2+(r*c)%3 == 0
	case 6:
		return ((r*c)%2+(r*c)%3)%2 == 0
	default:
		return ((r+c)%2+(r*c)%3)%2 == 0
	}
}

func (m *qrMatrix) applyMask(mask int) {
	for r := 0; r < m.size; r++ {
		for c := 0; c < m.size; c++ {
			if !m.isFn(r, c) && qrMaskFn(mask, r, c) {
				m.set(r, c, !m.at(r, c))
			}
		}
	}
}

// qrFormatBits builds the 15-bit format information: 5 data bits (EC level and
// mask) extended by a BCH(15,5) code and XORed with the fixed mask that stops
// an all-zero format from being valid.
func qrFormatBits(mask int) int {
	data := qrFormatECBits<<3 | mask
	v := data << 10
	for i := 4; i >= 0; i-- {
		if v&(1<<(i+10)) != 0 {
			v ^= 0b10100110111 << i
		}
	}
	return (data<<10 | v) ^ 0b101010000010010
}

// qrVersionBits builds the 18-bit version information for versions 7+: the
// version number extended by a BCH(18,6) code. The generator is the degree-12
// polynomial x^12+x^11+x^10+x^9+x^8+x^5+x^2+1.
func qrVersionBits(version int) int {
	const gen = 0x1f25
	v := version << 12
	for i := 5; i >= 0; i-- {
		if v&(1<<(i+12)) != 0 {
			v ^= gen << i
		}
	}
	return version<<12 | v
}

func (m *qrMatrix) drawFormat(mask int) {
	n := m.size
	bits := qrFormatBits(mask)
	for i := 0; i < 15; i++ {
		on := bits&(1<<i) != 0
		// Copy one wraps the top-left finder: the low bits run down column 8,
		// the high bits run left along row 8, hopping the timing module that
		// sits at index 6 on both axes.
		switch {
		case i < 6:
			m.setFn(i, 8, on)
		case i == 6:
			m.setFn(7, 8, on)
		case i == 7:
			m.setFn(8, 8, on)
		case i == 8:
			m.setFn(8, 7, on)
		default:
			m.setFn(8, 14-i, on)
		}
		// Copy two: split between the bottom-left and top-right corners.
		if i < 8 {
			m.setFn(8, n-1-i, on)
		} else {
			m.setFn(n-15+i, 8, on)
		}
	}
}

// qrPenalty scores a masked matrix by the four rules in the specification;
// the mask with the lowest score is the one that gets used. The rules exist
// to avoid patterns a scanner could mistake for structure — long same-colour
// runs, solid blocks, and anything resembling a finder.
func (m *qrMatrix) penalty() int {
	n, score := m.size, 0

	// Rule 1 — runs of five or more identical modules in a row or column.
	runScore := func(get func(i int) bool) {
		run, prev := 1, get(0)
		for i := 1; i < n; i++ {
			if cur := get(i); cur == prev {
				run++
			} else {
				if run >= 5 {
					score += 3 + (run - 5)
				}
				run, prev = 1, cur
			}
		}
		if run >= 5 {
			score += 3 + (run - 5)
		}
	}
	for i := 0; i < n; i++ {
		r := i
		runScore(func(c int) bool { return m.at(r, c) })
		runScore(func(rr int) bool { return m.at(rr, i) })
	}

	// Rule 2 — every 2×2 block of one colour.
	for r := 0; r < n-1; r++ {
		for c := 0; c < n-1; c++ {
			v := m.at(r, c)
			if m.at(r, c+1) == v && m.at(r+1, c) == v && m.at(r+1, c+1) == v {
				score += 3
			}
		}
	}

	// Rule 3 — the finder-like 1:1:3:1:1 sequence with four light modules on
	// either side, in any row or column.
	patterns := [][]bool{
		{true, false, true, true, true, false, true, false, false, false, false},
		{false, false, false, false, true, false, true, true, true, false, true},
	}
	matches := func(get func(i int) bool, start int, pat []bool) bool {
		for k, want := range pat {
			if get(start+k) != want {
				return false
			}
		}
		return true
	}
	for i := 0; i < n; i++ {
		row := func(c int) bool { return m.at(i, c) }
		col := func(r int) bool { return m.at(r, i) }
		for start := 0; start+11 <= n; start++ {
			for _, p := range patterns {
				if matches(row, start, p) {
					score += 40
				}
				if matches(col, start, p) {
					score += 40
				}
			}
		}
	}

	// Rule 4 — deviation of the dark-module proportion from 50%.
	dark := 0
	for _, v := range m.mod {
		if v {
			dark++
		}
	}
	pct := dark * 100 / len(m.mod)
	dev := abs(pct-50) / 5
	score += dev * 10
	return score
}

// ── Entry point ──────────────────────────────────────────────────────────────

// qrEncode builds the module matrix for a payload, choosing the smallest
// version that fits and the mask with the lowest penalty.
func qrEncode(payload string) (*qrMatrix, error) {
	data := []byte(payload)
	var spec qrVersionSpec
	found := false
	for _, s := range qrVersions {
		if len(data) <= s.capacity() {
			spec, found = s, true
			break
		}
	}
	if !found {
		return nil, errors.New("qr: payload too long")
	}

	codewords := qrEncodeData(data, spec)
	best, bestScore := (*qrMatrix)(nil), -1
	for mask := 0; mask < 8; mask++ {
		m := newQRMatrix(spec.size())
		m.drawFunctionPatterns(spec)
		m.placeData(codewords)
		m.applyMask(mask)
		m.drawFormat(mask)
		if s := m.penalty(); bestScore < 0 || s < bestScore {
			best, bestScore = m, s
		}
	}
	return best, nil
}

// qrSVG renders a payload as a standalone SVG. Dark modules are drawn as one
// path so the markup stays small, and the light background is explicit —
// scanners need the quiet zone and the contrast, and a transparent background
// over a dark page theme would invert the code and make it unreadable.
func qrSVG(payload string) (string, error) {
	m, err := qrEncode(payload)
	if err != nil {
		return "", err
	}
	const quiet = 4 // modules of mandatory margin
	dim := m.size + quiet*2

	var path strings.Builder
	for r := 0; r < m.size; r++ {
		for c := 0; c < m.size; c++ {
			if m.at(r, c) {
				fmt.Fprintf(&path, "M%d %dh1v1h-1z", c+quiet, r+quiet)
			}
		}
	}
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="2FA enrolment QR code">`+
			`<rect width="%d" height="%d" fill="#ffffff"/><path d="%s" fill="#000000"/></svg>`,
		dim, dim, dim, dim, path.String()), nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
