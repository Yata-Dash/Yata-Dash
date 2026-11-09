package scrape

import "testing"

// Lowercasing can change a rune's byte length, so a label offset found in
// strings.ToLower(text) used to be applied to the original text: "Ⱥ" (2
// bytes → 3) pushed the offset past the end and panicked; "İ" (2 → 1)
// sliced mid-word and stored garbage as the value.
func TestExtractFromHTMLLengthChangingLowercase(t *testing.T) {
	cases := []struct {
		name, html, want string
	}{
		// The original reproducer: the second "total uploaded" sits at the
		// very end, so the shifted offset ran past len(text).
		{"growing rune at end", "<span>Total\nUploaded ȺȺ total uploaded</span>", ""},
		{"growing rune with value", "<span>Total\nUploaded ȺȺȺȺ total uploaded: 5 GiB</span>", "5 GiB"},
		{"shrinking rune with value", "<span>Total\nUploaded İİİİ total uploaded: 5 GiB</span>", "5 GiB"},
		{"invalid utf-8", "<span>Total\nUploaded \xff\xff total uploaded: 5 GiB</span>", "5 GiB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractFromHTML(tc.html, Spec{})["uploaded"]
			if got != tc.want {
				t.Fatalf("uploaded = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIndexLower(t *testing.T) {
	cases := []struct {
		s, sub     string
		start, end int
	}{
		{"Total Uploaded: 1", "uploaded", 6, 14},
		{"ȺȺ Uploaded", "uploaded", 5, 13},
		{"İİ Uploaded", "uploaded", 5, 13},
		{"ȺB", "ⱥb", 0, 3},
		{"İx", "ix", 0, 3},
		{"nothing here", "uploaded", -1, -1},
	}
	for _, tc := range cases {
		start, end := indexLower(tc.s, tc.sub)
		if start != tc.start || end != tc.end {
			t.Errorf("indexLower(%q, %q) = %d, %d; want %d, %d", tc.s, tc.sub, start, end, tc.start, tc.end)
		}
		if start >= 0 {
			_ = tc.s[start:end] // must be in range
		}
	}
}
