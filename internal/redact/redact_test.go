package redact

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// All credential-shaped values in this file are invented. Nothing here is a
// real key, cookie or account detail, and nothing here should ever be replaced
// with one to "make the test realistic".

func TestStringDropsQueryStrings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "api key in query",
			in:   `Get "https://tracker.example/api/user?api_token=abc123secret": dial tcp: timeout`,
			want: `Get "https://tracker.example/api/user?REDACTED": dial tcp: timeout`,
		},
		{
			name: "query kept out even with several params",
			in:   `https://tracker.example/x?a=1&passkey=zzz&b=2`,
			want: `https://tracker.example/x?REDACTED`,
		},
		{
			name: "no query is left alone",
			in:   `Get "https://tracker.example/api/user": connection refused`,
			want: `Get "https://tracker.example/api/user": connection refused`,
		},
		{
			name: "userinfo masked",
			in:   `https://someone:hunter2@tracker.example/profile`,
			want: `https://REDACTED@tracker.example/profile`,
		},
		{
			name: "telegram bot token in the path",
			in:   `Post "https://api.telegram.org/bot9876543:AAFakeTokenValue_x/sendMessage": timeout`,
			want: `Post "https://api.telegram.org/botREDACTED/sendMessage": timeout`,
		},
		{
			name: "discord webhook token in the path",
			in:   `Post "https://discord.com/api/webhooks/1234567890/aBcDeF-gh_ijKlMnOp.qrst": timeout`,
			want: `Post "https://discord.com/api/webhooks/1234567890/REDACTED": timeout`,
		},
		{
			name: "slack webhook secret in the path",
			in:   `Post "https://hooks.slack.com/services/T0A1B2C3/B4D5E6F7/xyz789secret": timeout`,
			want: `Post "https://hooks.slack.com/services/T0A1B2C3/B4D5E6F7/REDACTED": timeout`,
		},
		{
			name: "trailing sentence punctuation is not eaten",
			in:   `could not reach https://tracker.example/api?key=s3cret.`,
			want: `could not reach https://tracker.example/api?REDACTED.`,
		},
		{
			name: "text with no URL is untouched",
			in:   `scrape: Anthelion (abc123) failed — http_500`,
			want: `scrape: Anthelion (abc123) failed — http_500`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := String(c.in); got != c.want {
				t.Errorf("String(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

func TestStringHandlesMultipleURLsInOneLine(t *testing.T) {
	in := `redirect https://a.example/x?key=one -> https://b.example/y?key=two`
	got := String(in)
	if strings.Contains(got, "key=one") || strings.Contains(got, "key=two") {
		t.Fatalf("a secret survived: %q", got)
	}
	if n := strings.Count(got, Mask); n != 2 {
		t.Errorf("want both URLs masked, got %d masks in %q", n, got)
	}
}

func TestErrorNil(t *testing.T) {
	if got := Error(nil); got != "" {
		t.Errorf("Error(nil) = %q, want empty", got)
	}
}

// The regression that motivated the package: a real *url.Error from the
// standard library, not a hand-written string that happens to look like one.
func TestErrorRedactsRealTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close() // closed: the next request produces a genuine *url.Error

	_, err := http.Get(addr + "/api/user?api_token=abc123secret") //nolint:bodyclose
	if err == nil {
		t.Fatal("expected a transport error from a closed server")
	}
	if !strings.Contains(err.Error(), "abc123secret") {
		t.Skip("this Go version does not embed the URL in the error; nothing to redact")
	}
	if got := Error(err); strings.Contains(got, "abc123secret") {
		t.Errorf("API key survived redaction: %q", got)
	}
}

func TestErrorRedactsThroughWrapping(t *testing.T) {
	inner := fmt.Errorf(`Get "https://tracker.example/api?api_token=abc123secret": timeout`)
	wrapped := fmt.Errorf("fetch failed: %w", errors.Join(inner, errors.New("giving up")))
	if got := Error(wrapped); strings.Contains(got, "abc123secret") {
		t.Errorf("API key survived wrapping: %q", got)
	}
}

func TestJSONShapeKeepsKeysAndDropsValues(t *testing.T) {
	// Shaped like a tracker user endpoint, with invented personal fields.
	body := []byte(`{"username":"someone","email":"someone@example.invalid",
		"irc_key":"not-a-real-key","uploaded":12345,"seeding":true,
		"badges":[{"name":"first"},{"name":"second"}],"parent":null}`)
	got := JSONShape(body)

	for _, secret := range []string{"someone", "example.invalid", "not-a-real-key", "12345"} {
		if strings.Contains(got, secret) {
			t.Errorf("value %q survived JSONShape: %s", secret, got)
		}
	}
	for _, key := range []string{"username", "email", "irc_key", "uploaded", "badges"} {
		if !strings.Contains(got, key) {
			t.Errorf("key %q was lost, shape is useless for diagnosis: %s", key, got)
		}
	}
	// Arrays collapse to a single representative element.
	if strings.Count(got, `"name"`) != 1 {
		t.Errorf("array should collapse to one element: %s", got)
	}
}

func TestJSONShapeNonJSONReportsSizeOnly(t *testing.T) {
	body := []byte(`<!DOCTYPE html><html><body>Welcome back, someone</body></html>`)
	got := JSONShape(body)
	if strings.Contains(got, "someone") {
		t.Errorf("HTML body content leaked: %s", got)
	}
	if !strings.Contains(got, "non-JSON") {
		t.Errorf("want a non-JSON note, got %s", got)
	}
}

func TestJSONShapeIsDeterministic(t *testing.T) {
	body := []byte(`{"b":1,"a":"x","c":true}`)
	first := JSONShape(body)
	for range 20 {
		if got := JSONShape(body); got != first {
			t.Fatalf("JSONShape is not stable: %q vs %q", got, first)
		}
	}
}
