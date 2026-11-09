package notify

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Yata-Dash/Yata-Dash/internal/models"
)

// Gotify's app token is a custom header, which net/http copies onto every
// redirect hop. A destination answering with a redirect elsewhere must not
// pass the token on; one that redirects within itself still gets it.
func TestGotifyTokenDoesNotFollowACrossOriginRedirect(t *testing.T) {
	var leaked atomic.Value
	leaked.Store("")
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(r.Header.Get("X-Gotify-Key"))
	}))
	defer elsewhere.Close()
	gotify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/message", http.StatusFound)
	}))
	defer gotify.Close()

	_ = Send(models.NotifyDestination{Type: "gotify", URL: gotify.URL, Token: "app-token"}, "t", "m")
	if k := leaked.Load().(string); k != "" {
		t.Errorf("the redirect target received X-Gotify-Key %q", k)
	}
}

func TestGotifyTokenSurvivesASameOriginRedirect(t *testing.T) {
	var got atomic.Value
	got.Store("")
	mux := http.NewServeMux()
	mux.HandleFunc("/message", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/gotify/message", http.StatusPermanentRedirect)
	})
	mux.HandleFunc("/gotify/message", func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("X-Gotify-Key"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := Send(models.NotifyDestination{Type: "gotify", URL: srv.URL, Token: "app-token"}, "t", "m"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if k := got.Load().(string); k != "app-token" {
		t.Errorf("X-Gotify-Key after a same-origin redirect = %q, want app-token", k)
	}
}
