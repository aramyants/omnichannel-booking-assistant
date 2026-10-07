package meta

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aramyants/omnichannel-booking-assistant/internal/domain/messaging"
)

func TestDirectHealthInvalidTokensPermissionsAndScopedIDs(t *testing.T) {
	for _, tc := range []struct {
		name, body       string
		status           int
		provider         messaging.Provider
		blocked, wantErr bool
	}{
		{"instagram alias", `{"id":"app-scoped-id"}`, 200, messaging.ProviderInstagram, false, false},
		{"messenger subscription", `{"data":[{"id":"app-id"}]}`, 200, messaging.ProviderMessenger, false, false},
		{"invalid token", `{"error":{"code":190,"message":"session invalidated"}}`, 400, messaging.ProviderInstagram, true, false},
		{"metadata permission", `{"error":{"code":100,"message":"missing permission"}}`, 400, messaging.ProviderMessenger, false, true},
		{"temporary failure", `{}`, 503, messaging.ProviderInstagram, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("health probe sent a message")
				}
				if tc.provider == messaging.ProviderMessenger && r.URL.Path != "/v25.0/account/subscribed_apps" {
					t.Errorf("wrong health read: %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c, err := newDirectClient("token", "account", tc.provider, WithBaseURL(srv.URL), WithGraphVersion("v25.0"))
			if err != nil {
				t.Fatal(err)
			}
			h, err := c.ReadHealth(t.Context())
			if (err != nil) != tc.wantErr || h.Blocked != tc.blocked {
				t.Fatalf("health=%+v error=%v", h, err)
			}
		})
	}
}

func TestDirectAuthRejectionTripsCircuitWithoutRetryingDelivery(t *testing.T) {
	posts, blocks := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"code":190,"message":"session invalidated"}}`))
	}))
	defer srv.Close()
	c, _ := NewInstagramClient("token", "account", WithBaseURL(srv.URL))
	c.SetAuthRejected(func(context.Context) error { blocks++; return nil })
	err := c.Send(t.Context(), messaging.Outgoing{Provider: messaging.ProviderInstagram, ExternalThreadID: "customer", Text: "studio help"})
	if posts != 1 || blocks != 1 || !errors.Is(err, ErrAuthorization) || !errors.Is(err, messaging.ErrDeliveryRejected) {
		t.Fatalf("posts=%d blocks=%d error=%v", posts, blocks, err)
	}
}
