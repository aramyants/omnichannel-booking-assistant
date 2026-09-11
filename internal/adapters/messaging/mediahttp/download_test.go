package mediahttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDownloadsRejectRedirectsAndStreamingOversize(t *testing.T) {
	for _, kind := range []string{"redirect", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "redirect" {
					http.Redirect(w, r, "https://example.com/private", http.StatusFound)
					return
				}
				w.(http.Flusher).Flush()
				_, _ = w.Write([]byte("too-large"))
			}))
			defer srv.Close()
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
			if _, _, err := Download(srv.Client(), req, 4); err == nil {
				t.Fatal("unsafe download accepted")
			}
		})
	}
}
