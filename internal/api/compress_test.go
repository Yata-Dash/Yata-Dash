package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return out
}

// TestJSONResponsesCompressed: an API response is gzipped for a client that
// asks for it and is byte-for-byte the plain JSON once decoded; a client that
// doesn't ask gets it uncompressed.
func TestJSONResponsesCompressed(t *testing.T) {
	d := testDeps(t)
	router := NewRouter(d)

	plain := httptest.NewRecorder()
	router.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if plain.Code != http.StatusOK {
		t.Fatalf("plain GET = %d", plain.Code)
	}
	if ce := plain.Header().Get("Content-Encoding"); ce != "" {
		t.Fatalf("no Accept-Encoding, yet Content-Encoding = %q", ce)
	}
	var v map[string]string
	if err := json.Unmarshal(plain.Body.Bytes(), &v); err != nil || v["version"] == "" {
		t.Fatalf("plain body not the version JSON: %v %q", err, plain.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	gz := httptest.NewRecorder()
	router.ServeHTTP(gz, req)
	if gz.Code != http.StatusOK {
		t.Fatalf("gzip GET = %d", gz.Code)
	}
	if ce := gz.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", ce)
	}
	if !strings.Contains(gz.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding", gz.Header().Get("Vary"))
	}
	if got := gunzip(t, gz.Body.Bytes()); !bytes.Equal(got, plain.Body.Bytes()) {
		t.Errorf("decoded body %q != plain body %q", got, plain.Body.String())
	}
}

// TestStaticCompressionAndRanges: the static file server still compresses a
// whole-file response, but a Range request is answered uncompressed with the
// exact bytes asked for — a gzipped 206 would match neither the range nor a
// decodable stream. Plain-text downloads (the log file, the CSV export) are
// not in the compressible list, so they keep their Content-Length.
func TestStaticCompressionAndRanges(t *testing.T) {
	d := testDeps(t)
	d.BaseDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(d.BaseDir, "static"), 0o755); err != nil {
		t.Fatal(err)
	}
	js := []byte(strings.Repeat("console.log('yata');\n", 200))
	if err := os.WriteFile(filepath.Join(d.BaseDir, "static", "app.js"), js, 0o644); err != nil {
		t.Fatal(err)
	}
	txt := []byte(strings.Repeat("a log line\n", 200))
	if err := os.WriteFile(filepath.Join(d.BaseDir, "static", "notes.txt"), txt, 0o644); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(d)

	get := func(path, rng string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

	full := get("/static/app.js", "")
	if full.Code != http.StatusOK || full.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("full static GET = %d, Content-Encoding %q; want 200 gzip", full.Code, full.Header().Get("Content-Encoding"))
	}
	if got := gunzip(t, full.Body.Bytes()); !bytes.Equal(got, js) {
		t.Fatal("decoded static file differs from the file on disk")
	}

	part := get("/static/app.js", "bytes=0-9")
	if part.Code != http.StatusPartialContent {
		t.Fatalf("range GET = %d, want 206", part.Code)
	}
	if ce := part.Header().Get("Content-Encoding"); ce != "" {
		t.Fatalf("range response Content-Encoding = %q, want none", ce)
	}
	if !bytes.Equal(part.Body.Bytes(), js[:10]) {
		t.Fatalf("range body = %q, want %q", part.Body.Bytes(), js[:10])
	}

	text := get("/static/notes.txt", "")
	if ce := text.Header().Get("Content-Encoding"); ce != "" {
		t.Fatalf("text/plain response Content-Encoding = %q, want none", ce)
	}
	if text.Header().Get("Content-Length") == "" || !bytes.Equal(text.Body.Bytes(), txt) {
		t.Fatal("text/plain response lost its Content-Length or its body")
	}
}

// TestHistoryCSVNotCompressed: the CSV export is text/csv, outside the
// compressible list, so it goes out as written.
func TestHistoryCSVNotCompressed(t *testing.T) {
	d := testDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/api/history/export.csv", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	NewRouter(d).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET export.csv = %d: %s", rr.Code, rr.Body.String())
	}
	if ce := rr.Header().Get("Content-Encoding"); ce != "" {
		t.Fatalf("CSV Content-Encoding = %q, want none", ce)
	}
	if !strings.HasPrefix(rr.Body.String(), "date,tracker") {
		t.Fatalf("CSV body = %q, want the header row", rr.Body.String())
	}
}

// BenchmarkCompressHistorySeries pushes a /api/history/series-shaped payload
// (20 trackers × 5 fields × 672 points — a week at 15-minute resolution, about
// what the dashboard loads) through the middleware and reports the bytes on
// the wire with and without gzip.
func BenchmarkCompressHistorySeries(b *testing.B) {
	var series []historySeries
	for tr := 0; tr < 20; tr++ {
		for f := 0; f < 5; f++ {
			s := historySeries{TrackerID: "tracker-" + strings.Repeat("x", tr%7), Field: "field", Unit: "GiB"}
			v := 1000.0 * float64(tr+1)
			for p := 0; p < 672; p++ {
				v += float64((p*7+tr*13+f)%17) * 0.0137
				s.Points = append(s.Points, [2]float64{float64(1_760_000_000 + p*900), v})
			}
			series = append(series, s)
		}
	}
	h := compressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, map[string]any{"series": series})
	}))
	run := func(b *testing.B, enc string) {
		var n int
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest(http.MethodGet, "/api/history/series", nil)
			if enc != "" {
				req.Header.Set("Accept-Encoding", enc)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			n = rr.Body.Len()
		}
		b.ReportMetric(float64(n), "wire-bytes")
	}
	b.Run("identity", func(b *testing.B) { run(b, "") })
	b.Run("gzip", func(b *testing.B) { run(b, "gzip") })
}
